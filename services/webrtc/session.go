package webrtc

import (
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/stats"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	log "github.com/sirupsen/logrus"
)

// ClientReport is the optional stats beacon reported by the browser through
// POST /api/metrics/webrtc. It only enriches the admin viewer table; the server
// never relies on it for capacity or correctness.
type ClientReport struct {
	DecodedWidth    int     `json:"decodedWidth"`
	DecodedHeight   int     `json:"decodedHeight"`
	FPS             float64 `json:"fps"`
	PacketsLost     int64   `json:"packetsLost"`
	PacketsReceived int64   `json:"packetsReceived"`
	// PacketLossPct is the loss percentage exactly as the player's own stats
	// panel shows it. When it is absent or invalid the server derives a
	// percentage from PacketsLost/(PacketsLost+PacketsReceived).
	PacketLossPct   *float64 `json:"packetLossPct,omitempty"`
	JitterMs        float64  `json:"jitterMs"`
	RoundTripTimeMs float64  `json:"roundTripTimeMs"`
	// Transport is the client-observed selected ICE candidate pair protocol
	// ("udp" or "tcp"). It is only a fallback: server-side detection of the
	// nominated candidate pair wins when available.
	Transport string `json:"transport,omitempty"`
	// VideoBitrateKbps is the player's measured inbound video bitrate.
	VideoBitrateKbps *float64 `json:"videoBitrateKbps,omitempty"`
}

// Client-report sanity bounds. The beacon is untrusted input: values outside
// these bounds are ignored rather than shown to the operator.
const (
	maxClientFPS               = 240.0
	minClientResolution        = 16
	maxClientBitrateKbps       = 1_000_000.0
	maxClientRoundTripMillisec = 60_000.0
)

// ViewerSnapshot is one WHEP viewer row for the admin API. Server-measured
// fields are nil when unknown; the handler renders them as N/A (spec 6.3).
type ViewerSnapshot struct {
	SessionID       string        `json:"sessionId"`
	ClientID        string        `json:"clientID"`
	Variant         int           `json:"variant"`
	VariantName     string        `json:"variantName"`
	IPAddress       string        `json:"ipAddress"`
	UserAgent       string        `json:"userAgent"`
	Protocol        string        `json:"protocol"`
	ConnectedAt     time.Time     `json:"connectedAt"`
	DurationSeconds float64       `json:"durationSeconds"`
	BitrateKbps     *float64      `json:"bitrateKbps"`
	Resolution      string        `json:"resolution"`
	FPS             *float64      `json:"fps"`
	PacketLossPct   *float64      `json:"packetLossPct"`
	JitterMs        *float64      `json:"jitterMs"`
	RoundTripTimeMs *float64      `json:"roundTripTimeMs"`
	ICEState        string        `json:"iceState"`
	ICETransport    string        `json:"iceTransport,omitempty"`
	DTLSState       string        `json:"dtlsState"`
	PLICount        uint64        `json:"pliCount"`
	FIRCount        uint64        `json:"firCount"`
	NACKCount       uint64        `json:"nackCount"`
	Source          string        `json:"source"`
	Client          *ClientReport `json:"client,omitempty"`
}

type sessionStats struct {
	bitrateKbps     *float64
	packetLossPct   *float64
	jitterMs        *float64
	roundTripTimeMs *float64
	iceState        string
	iceTransport    string
	dtlsState       string
	lastBytesSent   uint64
	lastSampleAt    time.Time
}

type session struct {
	id      string
	variant *variantTracks
	pc      *webrtc.PeerConnection
	getter  stats.Getter
	// estimator is only non-nil when bandwidthEstimation is enabled; it is
	// exposed for monitoring only (spec 14.1: no encoder adaptation).
	estimator cc.BandwidthEstimator

	ip         string
	userAgent  string
	createdAt  time.Time
	lastActive atomic.Int64

	videoSender *webrtc.RTPSender
	audioSender *webrtc.RTPSender

	pliCount  atomic.Uint64
	firCount  atomic.Uint64
	nackCount atomic.Uint64

	statsMu sync.Mutex
	stats   sessionStats

	clientReport atomic.Pointer[ClientReport]

	closeOnce sync.Once
	closed    chan struct{}
	reason    atomic.Value
	wg        sync.WaitGroup
}

func newSession(id string, tracks *variantTracks, pc *webrtc.PeerConnection, getter stats.Getter,
	estimator cc.BandwidthEstimator, ip, userAgent string, videoSender, audioSender *webrtc.RTPSender,
) *session {
	s := &session{
		id:          id,
		variant:     tracks,
		pc:          pc,
		getter:      getter,
		estimator:   estimator,
		ip:          ip,
		userAgent:   userAgent,
		createdAt:   time.Now(),
		closed:      make(chan struct{}),
		videoSender: videoSender,
		audioSender: audioSender,
	}
	s.touch()

	return s
}

func (s *session) touch() {
	s.lastActive.Store(time.Now().UnixNano())
}

func (s *session) lastActiveAt() time.Time {
	return time.Unix(0, s.lastActive.Load())
}

func (s *session) idleFor(now time.Time) time.Duration {
	return now.Sub(s.lastActiveAt())
}

func (s *session) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// close tears down the PeerConnection and stops the RTCP drain goroutines. It
// is idempotent and safe to call from the janitor and the DELETE handler.
func (s *session) close(reason string) {
	s.closeOnce.Do(func() {
		s.reason.Store(reason)
		if s.pc != nil {
			if err := s.pc.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				log.Debugf("webrtc: closing session %s: %v", s.id, err)
			}
		}
		close(s.closed)
		s.wg.Wait()
		log.Debugf("webrtc: session %s closed (%s)", s.id, reason)
	})
}

// startRTCPDrains starts one RTCP read loop per outgoing track. The Pion
// interceptor pipeline (NACK responder, stats recorder, RTCP reports) only runs
// while the application drains ReadRTCP; without these goroutines a video
// stream that never drains RTCP loses retransmission and per-viewer stats
// silently. The loops exit when the PeerConnection is closed.
func (s *session) startRTCPDrains() {
	for _, sender := range []*webrtc.RTPSender{s.videoSender, s.audioSender} {
		if sender == nil {
			continue
		}
		s.wg.Add(1)
		go func(sender *webrtc.RTPSender) {
			defer s.wg.Done()
			s.drainRTCP(sender)
		}(sender)
	}
}

func (s *session) drainRTCP(sender *webrtc.RTPSender) {
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		s.touch()
		for _, packet := range packets {
			switch packet.(type) {
			case *rtcp.PictureLossIndication:
				s.pliCount.Add(1)
			case *rtcp.FullIntraRequest:
				s.firCount.Add(1)
			case *rtcp.TransportLayerNack:
				s.nackCount.Add(1)
			}
		}
	}
}

func (s *session) recordClientReport(report ClientReport) {
	sanitized := sanitizedClientReport(report)
	s.clientReport.Store(&sanitized)
	s.touch()
}

// sanitizedClientReport clears client measurements that are out of bounds so
// an absurd or malicious beacon can never reach the admin table.
func sanitizedClientReport(report ClientReport) ClientReport {
	if !validClientResolution(report.DecodedWidth, report.DecodedHeight) {
		report.DecodedWidth = 0
		report.DecodedHeight = 0
	}
	if !validClientFPS(report.FPS) {
		report.FPS = 0
	}
	if report.PacketsLost < 0 {
		report.PacketsLost = 0
	}
	if report.PacketsReceived < 0 {
		report.PacketsReceived = 0
	}
	report.JitterMs = sanitizeNonNegative(report.JitterMs)
	report.RoundTripTimeMs = sanitizeNonNegativeBounded(report.RoundTripTimeMs, maxClientRoundTripMillisec)
	if report.PacketLossPct != nil && !validPacketLossPct(*report.PacketLossPct) {
		report.PacketLossPct = nil
	}
	if report.PacketLossPct == nil {
		report.PacketLossPct = derivePacketLossPct(report)
	}
	report.Transport = normalizeICETransport(report.Transport)
	if report.VideoBitrateKbps != nil && !validClientBitrate(*report.VideoBitrateKbps) {
		report.VideoBitrateKbps = nil
	}

	return report
}

// validClientResolution reports whether the decoded frame size is plausible.
func validClientResolution(width, height int) bool {
	return width >= minClientResolution && height >= minClientResolution
}

// validClientFPS reports whether a client frame rate is plausible. The player
// has been observed sending absurd values (571 fps); those are rejected.
func validClientFPS(fps float64) bool {
	return !math.IsNaN(fps) && !math.IsInf(fps, 0) && fps > 0 && fps <= maxClientFPS
}

// validPacketLossPct reports whether a client loss percentage is possible.
func validPacketLossPct(loss float64) bool {
	return !math.IsNaN(loss) && !math.IsInf(loss, 0) && loss >= 0 && loss <= 100
}

// validClientBitrate reports whether a measured inbound bitrate is plausible.
func validClientBitrate(kbps float64) bool {
	return !math.IsNaN(kbps) && !math.IsInf(kbps, 0) && kbps > 0 && kbps <= maxClientBitrateKbps
}

// derivePacketLossPct computes the loss percentage from the client counters
// when the player did not send a percentage itself.
func derivePacketLossPct(report ClientReport) *float64 {
	total := report.PacketsLost + report.PacketsReceived
	if total <= 0 {
		return nil
	}
	loss := float64(report.PacketsLost) / float64(total) * 100
	if !validPacketLossPct(loss) {
		return nil
	}

	return &loss
}

func sanitizeNonNegative(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}

	return value
}

func sanitizeNonNegativeBounded(value, maxValue float64) float64 {
	value = sanitizeNonNegative(value)
	if value > maxValue {
		return 0
	}

	return value
}

// normalizeICETransport maps a candidate protocol to the admin vocabulary.
func normalizeICETransport(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "udp":
		return "udp"
	case "tcp":
		return "tcp"
	default:
		return ""
	}
}

func (s *session) snapshot(now time.Time) ViewerSnapshot {
	source := "server"
	client := s.clientReport.Load()
	if client != nil {
		source = "client"
	}

	sessionID := s.id
	snapshot := ViewerSnapshot{
		SessionID:       sessionID,
		ClientID:        sessionID,
		Variant:         s.variant.variant.Index,
		VariantName:     s.variant.variant.Name,
		IPAddress:       s.ip,
		UserAgent:       s.userAgent,
		Protocol:        "webrtc",
		ConnectedAt:     s.createdAt,
		DurationSeconds: now.Sub(s.createdAt).Seconds(),
		Resolution:      s.variant.resolution(),
		PLICount:        s.pliCount.Load(),
		FIRCount:        s.firCount.Load(),
		NACKCount:       s.nackCount.Load(),
		Source:          source,
		Client:          client,
	}

	s.statsMu.Lock()
	snapshot.BitrateKbps = s.stats.bitrateKbps
	snapshot.PacketLossPct = s.stats.packetLossPct
	snapshot.JitterMs = s.stats.jitterMs
	snapshot.RoundTripTimeMs = s.stats.roundTripTimeMs
	snapshot.ICEState = s.stats.iceState
	snapshot.ICETransport = s.stats.iceTransport
	snapshot.DTLSState = s.stats.dtlsState
	s.statsMu.Unlock()

	return snapshot
}

// sessionRegistry is the in-memory viewer registry. Sessions are never
// persisted; the registry is the single source for capacity counters and the
// admin viewer list.
type sessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*session
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: map[string]*session{}}
}

func (r *sessionRegistry) add(s *session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[s.id]; exists {
		return false
	}
	r.sessions[s.id] = s

	return true
}

func (r *sessionRegistry) get(id string) (*session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]

	return s, ok
}

func (r *sessionRegistry) remove(id string) *session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return nil
	}
	delete(r.sessions, id)

	return s
}

func (r *sessionRegistry) all() []*session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}

	return out
}

func (r *sessionRegistry) drain() []*session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*session, 0, len(r.sessions))
	for id, s := range r.sessions {
		out = append(out, s)
		delete(r.sessions, id)
	}

	return out
}

func (r *sessionRegistry) len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.sessions)
}
