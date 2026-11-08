package srt

import (
	"context"
	"time"

	gosrt "github.com/datarhei/gosrt"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/services/ingest"
)

const (
	// statsSampleInterval is how often a session samples gosrt counters. It is
	// independent of admin polling so the interval counters stay meaningful.
	statsSampleInterval = time.Second
	// idleCloseGrace is added to the configured peer idle timeout before the
	// sampler closes a session that has stopped delivering payload bytes. It
	// is a safety net for the case where nothing reads the connection (for
	// example a handler that fails before starting the transcoder).
	idleCloseGrace = time.Second
)

// statsSampler periodically snapshots gosrt connection statistics, maps them
// onto the protocol-neutral ingest.Stats shape, and stores the snapshot in the
// session. One sampler runs per SRT session.
type statsSampler struct {
	conn          gosrt.Conn
	base          *ingest.SessionBase
	payloadBytes  func() int64
	lastReadAt    func() time.Time
	idleTimeout   time.Duration
	debugInterval time.Duration
	logger        *log.Logger
	onIdle        func(error)
	onSample      func()

	cancel context.CancelFunc
	done   chan struct{}
}

func newStatsSampler(
	conn gosrt.Conn,
	base *ingest.SessionBase,
	payloadBytes func() int64,
	lastReadAt func() time.Time,
	idleTimeout time.Duration,
	debugInterval time.Duration,
	logger *log.Logger,
	onIdle func(error),
	onSample func(),
) *statsSampler {
	return &statsSampler{
		conn:          conn,
		base:          base,
		payloadBytes:  payloadBytes,
		lastReadAt:    lastReadAt,
		idleTimeout:   idleTimeout,
		debugInterval: debugInterval,
		logger:        logger,
		onIdle:        onIdle,
		onSample:      onSample,
		done:          make(chan struct{}),
	}
}

// start launches the sampler through the service spawn function so the
// goroutine is tracked by the service lifecycle. It is a no-op when the
// sampler has no connection.
func (s *statsSampler) start(parent context.Context, spawn func(func()) bool) {
	if s == nil || s.conn == nil || spawn == nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	spawn(func() {
		s.run(ctx)
	})
}

// stop cancels the sampler. It does not wait for the goroutine because stop is
// called from the sampler's own idle callback as well.
func (s *statsSampler) stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *statsSampler) run(ctx context.Context) {
	defer close(s.done)

	var previous gosrt.Statistics
	previousPayload := int64(0)
	previousAt := time.Now()
	debugElapsed := time.Duration(0)

	ticker := time.NewTicker(statsSampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			var current gosrt.Statistics
			s.conn.Stats(&current)

			payload := s.payloadBytes()
			elapsed := now.Sub(previousAt)
			s.base.SetStats(mapStatistics(previous, current, previousPayload, payload, elapsed))
			if s.onSample != nil {
				s.onSample()
			}

			if s.debugInterval > 0 {
				debugElapsed += elapsed
				if debugElapsed >= s.debugInterval {
					debugElapsed = 0
					s.logDebugStats(now)
				}
			}

			previous = current
			previousPayload = payload
			previousAt = now

			if s.idleTimeout > 0 && now.Sub(s.lastReadAt()) > s.idleTimeout+idleCloseGrace {
				if s.logger != nil {
					s.logger.WithField("idle_timeout", s.idleTimeout.String()).Warn("SRT session idle; closing connection")
				}
				if s.onIdle != nil {
					s.onIdle(ErrIdleTimeout)
				}
				return
			}
		}
	}
}

func (s *statsSampler) logDebugStats(now time.Time) {
	if s.logger == nil {
		return
	}
	stats := s.base.Stats()
	fields := log.Fields{
		"payload_bytes": s.payloadBytes(),
		"sampled_at":    now.UTC().Format(time.RFC3339),
	}
	if stats.BitrateKbps != nil {
		fields["bitrate_kbps"] = *stats.BitrateKbps
	}
	if stats.PacketLossPct != nil {
		fields["packet_loss_pct"] = *stats.PacketLossPct
	}
	if stats.RTTMs != nil {
		fields["rtt_ms"] = *stats.RTTMs
	}
	if stats.RetransmittedPackets != nil {
		fields["retransmitted_packets"] = *stats.RetransmittedPackets
	}
	if stats.DroppedPackets != nil {
		fields["dropped_packets"] = *stats.DroppedPackets
	}
	s.logger.WithFields(fields).Debug("SRT session statistics")
}

// mapStatistics converts two consecutive gosrt samples into the ingest.Stats
// shape. The first sample (zero previous counters, zero previous payload) still
// yields a valid bitrate because the payload counter starts at session start.
// Loss and rate fields are nil when there is nothing to measure, which the UI
// must render as N/A.
func mapStatistics(previous, current gosrt.Statistics, previousPayload, currentPayload int64, elapsed time.Duration) ingest.Stats {
	payloadDelta := currentPayload - previousPayload
	if payloadDelta < 0 {
		payloadDelta = 0
	}

	var bitrateKbps *float64
	if elapsed > 0 {
		value := float64(payloadDelta) * 8 / 1000 / elapsed.Seconds()
		bitrateKbps = &value
	}

	received := subtractCounter(current.Accumulated.PktRecv, previous.Accumulated.PktRecv)
	lost := subtractCounter(current.Accumulated.PktRecvLoss, previous.Accumulated.PktRecvLoss)

	var packetLossPct *float64
	if received+lost > 0 {
		value := float64(lost) / float64(received+lost) * 100
		packetLossPct = &value
	}

	retransmitted := int64(subtractCounter(current.Accumulated.PktRecvRetrans, previous.Accumulated.PktRecvRetrans))
	dropped := int64(subtractCounter(current.Accumulated.PktRecvDrop, previous.Accumulated.PktRecvDrop))
	rtt := current.Instantaneous.MsRTT

	extra := map[string]float64{
		"recv_latency_ms":     float64(current.Instantaneous.MsRecvTsbPdDelay),
		"send_latency_ms":     float64(current.Instantaneous.MsSendTsbPdDelay),
		"flow_window_pkts":    float64(current.Instantaneous.PktFlowWindow),
		"recv_buf_ms":         float64(current.Instantaneous.MsRecvBuf),
		"link_capacity_mbps":  current.Instantaneous.MbpsLinkCapacity,
		"estimated_recv_mbps": current.Instantaneous.MbpsRecvRate,
		"pkt_sent_nak":        float64(subtractCounter(current.Accumulated.PktSentNAK, previous.Accumulated.PktSentNAK)),
		"pkt_recv_loss_rate":  current.Instantaneous.PktRecvLossRate,
		"pkt_recv_undecrypt":  float64(subtractCounter(current.Accumulated.PktRecvUndecrypt, previous.Accumulated.PktRecvUndecrypt)),
		"pkt_recv_unique":     float64(subtractCounter(current.Accumulated.PktRecvUnique, previous.Accumulated.PktRecvUnique)),
		"payload_bytes_total": float64(currentPayload),
		"stats_interval_ms":   float64(elapsed.Milliseconds()),
	}

	return ingest.Stats{
		BitrateKbps:          bitrateKbps,
		PacketLossPct:        packetLossPct,
		RTTMs:                &rtt,
		RetransmittedPackets: &retransmitted,
		DroppedPackets:       &dropped,
		Extra:                extra,
	}
}

// subtractCounter returns current - previous without underflowing when a
// counter resets or a sample is reordered.
func subtractCounter(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}
