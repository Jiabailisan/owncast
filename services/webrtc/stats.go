package webrtc

import (
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	statsInterval   = 2 * time.Second
	samplesPerStats = int(statsInterval / janitorInterval)
)

// sampleStats refreshes the server-measured metrics of every viewer. The data
// comes from two Pion sources: the interceptor stats Getter (outbound RTP and
// the browser's RTCP receiver reports) and PeerConnection.GetStats (ICE/DTLS
// transport state). See docs/design/phase0/03-webrtc-dependency-and-design.md
// section 4.7.
func (s *Service) sampleStats(now time.Time) {
	for _, sess := range s.sessions.all() {
		s.sampleSession(sess, now)
	}
}

func (s *Service) sampleSession(sess *session, now time.Time) {
	sampled := s.sampleOutboundStats(sess, now)
	s.sampleTransportStats(sess, &sampled)
	s.mergeSessionStats(sess, &sampled)

	if sampled.bitrateKbps != nil && *sampled.bitrateKbps > 0 {
		sess.touch()
	}
}

// sampleOutboundStats reads the interceptor stats getter: outbound RTP counters
// plus the browser's RTCP receiver reports (RTT/jitter/loss).
func (s *Service) sampleOutboundStats(sess *session, now time.Time) sessionStats {
	sampled := sessionStats{}
	if sess.getter == nil {
		return sampled
	}
	ssrc := senderSSRC(sess.videoSender)
	if ssrc == 0 {
		return sampled
	}
	streamStats := sess.getter.Get(ssrc)
	if streamStats == nil {
		return sampled
	}

	remoteInbound := streamStats.RemoteInboundRTPStreamStats
	outbound := streamStats.OutboundRTPStreamStats
	if remoteInbound.RoundTripTimeMeasurements > 0 {
		roundTripMs := float64(remoteInbound.RoundTripTime.Microseconds()) / 1000.0
		jitterMs := remoteInbound.Jitter * 1000
		lossPct := remoteInbound.FractionLost * 100
		sampled.roundTripTimeMs = &roundTripMs
		sampled.jitterMs = &jitterMs
		sampled.packetLossPct = &lossPct
	}

	sess.statsMu.Lock()
	previousBytes := sess.stats.lastBytesSent
	previousAt := sess.stats.lastSampleAt
	sess.statsMu.Unlock()

	sampled.lastBytesSent = outbound.BytesSent
	sampled.lastSampleAt = now
	if !previousAt.IsZero() && outbound.BytesSent > previousBytes {
		elapsed := now.Sub(previousAt).Seconds()
		if elapsed > 0 {
			bitrate := float64(outbound.BytesSent-previousBytes) * 8 / elapsed / 1000
			sampled.bitrateKbps = &bitrate
		}
	}

	return sampled
}

// sampleTransportStats reads ICE/DTLS state and the selected ICE transport
// from PeerConnection.GetStats().
func (s *Service) sampleTransportStats(sess *session, sampled *sessionStats) {
	if sess.pc == nil {
		return
	}
	report := sess.pc.GetStats()
	sampled.iceState = sess.pc.ICEConnectionState().String()
	for _, entry := range report {
		if transport, ok := entry.(webrtc.TransportStats); ok {
			sampled.dtlsState = transport.DTLSState.String()
			sampled.iceState = transport.ICEState.String()
		}
	}
	sampled.iceTransport = iceTransportFromStats(report)
}

// iceTransportFromStats returns the protocol ("udp" or "tcp") of the selected
// ICE candidate pair: the nominated pair that succeeded, the nominated pair
// while the checklist is still settling, or the highest-volume succeeded pair.
// Empty when no usable pair is present yet.
func iceTransportFromStats(report webrtc.StatsReport) string {
	candidates := make(map[string]string)
	var pairs []webrtc.ICECandidatePairStats
	for _, entry := range report {
		switch stat := entry.(type) {
		case webrtc.ICECandidateStats:
			candidates[stat.ID] = stat.Protocol
		case webrtc.ICECandidatePairStats:
			pairs = append(pairs, stat)
		}
	}

	selected, ok := selectedCandidatePair(pairs)
	if !ok {
		return ""
	}
	if protocol := normalizeICETransport(candidates[selected.LocalCandidateID]); protocol != "" {
		return protocol
	}

	return normalizeICETransport(candidates[selected.RemoteCandidateID])
}

// selectedCandidatePair picks the pair the ICE agent uses for media. A
// nominated+succeeded pair is authoritative; while nomination has not been
// observed yet a nominated pair is used, then the busiest succeeded pair.
func selectedCandidatePair(pairs []webrtc.ICECandidatePairStats) (webrtc.ICECandidatePairStats, bool) {
	var nominated *webrtc.ICECandidatePairStats
	var busiestSucceeded *webrtc.ICECandidatePairStats
	for index := range pairs {
		pair := &pairs[index]
		if pair.Nominated && pair.State == webrtc.StatsICECandidatePairStateSucceeded {
			return *pair, true
		}
		if pair.Nominated && nominated == nil {
			nominated = pair
		}
		if pair.State == webrtc.StatsICECandidatePairStateSucceeded &&
			(busiestSucceeded == nil || pair.PacketsSent > busiestSucceeded.PacketsSent) {
			busiestSucceeded = pair
		}
	}
	if nominated != nil {
		return *nominated, true
	}
	if busiestSucceeded != nil {
		return *busiestSucceeded, true
	}

	return webrtc.ICECandidatePairStats{}, false
}

// mergeSessionStats keeps the previous bitrate sample when this tick had no
// measurable delta and publishes the new snapshot.
func (s *Service) mergeSessionStats(sess *session, sampled *sessionStats) {
	sess.statsMu.Lock()
	defer sess.statsMu.Unlock()

	if sampled.bitrateKbps == nil {
		sampled.bitrateKbps = sess.stats.bitrateKbps
	}
	if sampled.lastSampleAt.IsZero() {
		sampled.lastSampleAt = sess.stats.lastSampleAt
		sampled.lastBytesSent = sess.stats.lastBytesSent
	}
	if sampled.iceTransport == "" {
		sampled.iceTransport = sess.stats.iceTransport
	}
	sess.stats = *sampled
}

func senderSSRC(sender *webrtc.RTPSender) uint32 {
	if sender == nil {
		return 0
	}
	parameters := sender.GetParameters()
	if len(parameters.Encodings) == 0 {
		return 0
	}

	return uint32(parameters.Encodings[0].SSRC)
}
