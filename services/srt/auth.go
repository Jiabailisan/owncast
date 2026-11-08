package srt

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	gosrt "github.com/datarhei/gosrt"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// authConfig is the immutable configuration snapshot read by the accept
// callback. It holds everything needed to authenticate a connection so the
// callback never takes the service mutex or touches the database (the key
// validator resolves its own cache).
type authConfig struct {
	formats            []string
	requireStreamID    bool
	enforcedEncryption bool
	passphrase         string
	cidr               *cidrSet
	debugInterval      time.Duration
}

func newAuthConfig(cfg models.SRTConfig, passphrase string) (*authConfig, error) {
	cidr, err := newCIDRSet(cfg.AllowedCIDRs, cfg.DeniedCIDRs)
	if err != nil {
		return nil, err
	}
	return &authConfig{
		formats:            cfg.StreamIDFormats,
		requireStreamID:    cfg.RequireStreamID,
		enforcedEncryption: cfg.EnforcedEncryption,
		passphrase:         passphrase,
		cidr:               cidr,
		debugInterval:      msDuration(cfg.DebugStatsIntervalMs),
	}, nil
}

// authenticate runs the ordered accept pipeline:
//
//  1. CIDR deny/allow.
//  2. streamid parsing and format/mode validation.
//  3. passphrase enforcement (gosrt does not verify the listener passphrase
//     itself; ConnRequest.SetPassphrase is the enforcement point).
//  4. per-IP authentication failure rate limiting.
//  5. shared stream-key validation.
//  6. single-publisher arbiter acquisition, then ConnRequest.Accept.
//
// It returns the bound session on success. A false return means the request
// has already been rejected (or the handshake failed after Accept started).
func (s *Service) authenticate(req gosrt.ConnRequest) (*session, bool) {
	remote := req.RemoteAddr()
	fields := log.Fields{"remote": remoteString(remote)}

	ip, ok := remoteIP(remote)
	if !ok {
		s.reject(req, wireRejectGeneric, reasonInternal, fields)
		return nil, false
	}
	fields["remote_ip"] = ip.String()

	auth := s.auth.Load()
	if auth == nil {
		s.reject(req, wireRejectSystem, reasonInternal, fields)
		return nil, false
	}

	if decision := auth.cidr.Check(ip); !decision.allowed {
		fields["reason"] = decision.reason
		s.reject(req, wireRejectGeneric, decision.reason, fields)
		return nil, false
	}

	parsed, err := ParseStreamID(req.StreamId(), auth.formats)
	if err != nil {
		reason := streamIDFailureReason(err, auth.requireStreamID)
		fields["reason"] = reason
		s.recordAuthFailure(ip)
		s.reject(req, wireRejectGeneric, reason, fields)
		return nil, false
	}
	fields["format"] = string(parsed.Format)
	fields["app"] = parsed.App

	if req.IsEncrypted() {
		if err := req.SetPassphrase(auth.passphrase); err != nil {
			fields["reason"] = reasonBadPassphrase
			s.recordAuthFailure(ip)
			s.reject(req, wireRejectBadSecret, reasonBadPassphrase, fields)
			return nil, false
		}
	} else if auth.enforcedEncryption && auth.passphrase != "" {
		fields["reason"] = reasonEncryptionRequired
		s.recordAuthFailure(ip)
		s.reject(req, wireRejectUnsecure, reasonEncryptionRequired, fields)
		return nil, false
	}
	fields["encrypted"] = req.IsEncrypted()

	if !s.limits.Allow(ip) {
		fields["reason"] = reasonRateLimited
		s.reject(req, wireRejectGeneric, reasonRateLimited, fields)
		return nil, false
	}

	keyID, valid := s.deps.StreamKeys.ValidateKey(parsed.Key)
	if !valid {
		fields["reason"] = reasonBadStreamKey
		fields["key_fingerprint"] = ingest.FingerprintKey(parsed.Key)
		s.recordAuthFailure(ip)
		s.reject(req, wireRejectGeneric, reasonBadStreamKey, fields)
		return nil, false
	}
	fields["key_id"] = keyID

	var sess *session
	sess = newSession(remote, keyID, req.Version(), req.IsEncrypted(), func(reason error) {
		s.sessionClosed(sess, reason)
	})

	if err := s.deps.Sessions.TryAcquire(sess); err != nil {
		if errors.Is(err, ingest.ErrPublisherActive) {
			fields["reason"] = reasonPublisherActive
			s.reject(req, wireRejectResource, reasonPublisherActive, fields)
			return nil, false
		}
		fields["reason"] = reasonInternal
		s.reject(req, wireRejectSystem, reasonInternal, fields)
		return nil, false
	}

	conn, err := req.Accept()
	if err != nil {
		s.deps.Sessions.Release(sess)
		s.logger.WithError(err).WithFields(fields).Warn("SRT accept failed")
		return nil, false
	}

	sess.bind(conn)
	fields["session_id"] = sess.ID()
	fields["version"] = req.Version()
	s.logger.WithFields(fields).Info("SRT session accepted")
	return sess, true
}

// reject sends a predefined SRT rejection code to the caller and records the
// detailed, non-secret reason in the Owncast log.
func (s *Service) reject(req gosrt.ConnRequest, wire gosrt.RejectionReason, reason string, fields log.Fields) {
	if fields == nil {
		fields = log.Fields{}
	}
	fields["reason"] = reason
	fields["rejection_code"] = uint32(wire)
	if req.RemoteAddr() != nil {
		fields["remote"] = remoteString(req.RemoteAddr())
	}
	s.logger.WithFields(fields).Warn("SRT connection rejected")
	req.Reject(wire)
}

func (s *Service) recordAuthFailure(ip netip.Addr) {
	s.limits.RecordFailure(ip)
}

// sessionClosed runs exactly once when a session terminates. It releases the
// arbiter slot, clears the active-session snapshot, logs the summary and
// schedules a pending listener rebind if one is waiting for this stream to end.
func (s *Service) sessionClosed(sess *session, reason error) {
	s.deps.Sessions.Release(sess)

	s.activeMu.Lock()
	if s.active == sess {
		s.active = nil
	}
	s.activeMu.Unlock()

	reasonText := "unknown"
	if reason != nil {
		reasonText = reason.Error()
	}

	fields := log.Fields{
		"session_id":    sess.ID(),
		"remote":        remoteString(sess.RemoteAddr()),
		"key_id":        sess.StreamKeyID(),
		"duration":      time.Since(sess.StartedAt()).String(),
		"payload_bytes": sess.PayloadBytes(),
		"reason":        reasonText,
	}
	stats := sess.Stats()
	if stats.BitrateKbps != nil {
		fields["bitrate_kbps"] = *stats.BitrateKbps
	}
	if stats.PacketLossPct != nil {
		fields["packet_loss_pct"] = *stats.PacketLossPct
	}
	if stats.RTTMs != nil {
		fields["rtt_ms"] = *stats.RTTMs
	}
	s.logger.WithFields(fields).Info("SRT session ended")

	s.maybeApplyPending()
}

// startSession starts the statistics sampler, publishes the active-session
// snapshot and hands the session to the ingest handler.
func (s *Service) startSession(sess *session) {
	s.mu.Lock()
	ctx := s.ctx
	peerIdleTimeout := msDuration(s.cfg.PeerIdleTimeoutMs)
	auth := s.auth.Load()
	s.mu.Unlock()

	if ctx == nil {
		sess.Close(ErrServiceStop)
		return
	}

	debugInterval := time.Duration(0)
	if auth != nil {
		debugInterval = auth.debugInterval
	}
	sess.startSampler(ctx, s.spawn, peerIdleTimeout, debugInterval, s.logger)

	s.activeMu.Lock()
	s.active = sess
	s.activeMu.Unlock()

	if err := s.deps.Handler(sess); err != nil {
		s.logger.WithError(err).Error("SRT ingest handler failed")
		sess.Close(fmt.Errorf("%w: %w", ErrHandlerFailed, err))
	}
}

// streamIDFailureReason maps a parser error to a stable, non-secret log reason.
func streamIDFailureReason(err error, requireStreamID bool) string {
	switch {
	case errors.Is(err, ErrStreamIDEmpty):
		if requireStreamID {
			return reasonStreamIDRequired
		}
		return reasonStreamIDEmpty
	case errors.Is(err, ErrStreamIDSubscriber):
		return reasonSubscriberMode
	case errors.Is(err, ErrStreamIDFormatDisabled):
		return string(ErrStreamIDFormatDisabled.Error())
	case errors.Is(err, ErrStreamIDMalformed):
		return string(ErrStreamIDMalformed.Error())
	case errors.Is(err, ErrStreamIDTooLong):
		return string(ErrStreamIDTooLong.Error())
	case errors.Is(err, ErrStreamIDInvalidKey):
		return string(ErrStreamIDInvalidKey.Error())
	default:
		return reasonBadStreamKey
	}
}

func remoteIP(addr net.Addr) (netip.Addr, bool) {
	if addr == nil {
		return netip.Addr{}, false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return netip.Addr{}, false
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return parsed.Unmap(), true
}

func remoteString(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}
