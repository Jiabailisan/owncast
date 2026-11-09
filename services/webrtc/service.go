package webrtc

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/webrtc/v4"
	log "github.com/sirupsen/logrus"
)

const (
	reservationTTL  = 10 * time.Second
	janitorInterval = 1 * time.Second
)

// Deps is the explicit dependency contract for the WebRTC delivery service.
// Everything is injected by main.go (the composition root); the service has no
// package-level state and no config-repository dependency of its own.
type Deps struct {
	// Config is the current persisted configuration snapshot.
	Config models.WebRTCConfig

	// ClientIP optionally resolves the signaling client address (rate limiting
	// and viewer records). When nil, RemoteAddr is used and X-Forwarded-For is
	// not trusted.
	ClientIP func(r *http.Request) string
}

// StatusSnapshot is the admin Configuration page status block for WebRTC.
type StatusSnapshot struct {
	Enabled        bool                  `json:"enabled"`
	Listening      string                `json:"listeningAddress,omitempty"`
	ActiveSessions int                   `json:"activeSessions"`
	Capacity       models.WebRTCCapacity `json:"capacity"`
	Warnings       []string              `json:"warnings"`
}

// Service owns WebRTC delivery: the Pion engine, WHEP sessions, capacity and
// the loopback media legs used by the transcoder. Construct it in main.go with
// New(Deps) and run it with Start; Stop tears everything down.
type Service struct {
	deps Deps

	mu       sync.RWMutex
	cfg      models.WebRTCConfig
	started  bool
	engine   *engine
	variants map[int]*variantTracks
	legs     map[int]*MediaLeg

	capacity *capacityGate
	limiter  *signalingLimiter
	sessions *sessionRegistry

	runCtx context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New constructs an idle WebRTC service. Start must be called before the HTTP
// handler serves traffic.
func New(deps Deps) *Service {
	cfg := NormalizeConfig(deps.Config)

	return &Service{
		deps:     deps,
		cfg:      cfg,
		variants: map[int]*variantTracks{},
		legs:     map[int]*MediaLeg{},
		capacity: newCapacityGate(cfg.MaxViewers, cfg.MaxViewersPerIP, reservationTTL, time.Now),
		limiter:  newSignalingLimiter(cfg.SignalingRateLimit, time.Now),
		sessions: newSessionRegistry(),
	}
}

// Start validates the restart-service configuration and brings up the Pion
// engine, the mux sockets and the janitor. Restart-service settings apply only
// on restart: call Stop first, then Start.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil
	}

	cfg := s.cfg
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	s.capacity.setLimits(cfg.MaxViewers, cfg.MaxViewersPerIP)
	s.limiter.setLimit(cfg.SignalingRateLimit)

	if cfg.Enabled {
		eng, err := newEngine(ctx, cfg)
		if err != nil {
			return err
		}
		s.engine = eng
		log.Infof("webrtc: delivery engine started (udpMode=%s networkTypes=%v)", cfg.UDPMode, cfg.NetworkTypes)
	} else {
		log.Infoln("webrtc: delivery disabled; WHEP endpoint will answer 503")
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.runCtx = runCtx
	s.cancel = cancel
	s.wg.Add(1)
	go s.janitor(runCtx)
	s.started = true

	return nil
}

// Stop closes every session and leg, releases the mux sockets and waits for the
// service goroutines. The context bounds the wait.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()

		return nil
	}
	s.started = false
	cancel := s.cancel
	eng := s.engine
	s.engine = nil
	s.cancel = nil
	s.runCtx = nil
	sessions := s.sessions.drain()
	legs := s.takeLegsLocked()
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, sess := range sessions {
		sess.close("service stopping")
		s.capacity.releaseSession(sess.id)
	}
	for _, leg := range legs {
		_ = leg.Close()
	}
	if eng != nil {
		eng.close()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("webrtc stop: %w", ctx.Err())
	}
}

// Enabled reports whether WebRTC delivery is currently enabled.
func (s *Service) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.cfg.Enabled
}

// Config returns a copy of the current configuration snapshot.
func (s *Service) Config() models.WebRTCConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.cfg
}

// UpdateConfig applies the live settings immediately and returns the keys whose
// new values need a service restart. The caller (admin config handler) is
// responsible for actually restarting the service for those keys, and for
// surfacing the returned error as a validation failure.
func (s *Service) UpdateConfig(cfg models.WebRTCConfig) ([]string, error) {
	cfg = NormalizeConfig(cfg)
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	s.mu.Lock()
	oldCfg := s.cfg
	s.cfg = cfg
	s.capacity.setLimits(cfg.MaxViewers, cfg.MaxViewersPerIP)
	s.limiter.setLimit(cfg.SignalingRateLimit)
	s.mu.Unlock()

	pending := RestartRequiredFields(oldCfg, cfg)
	if len(pending) > 0 {
		log.Infof("webrtc: settings changed that require a service restart: %v", pending)
	}

	return pending, nil
}

// SetVariants rebuilds the per-variant track sets from the configured output
// variants. Track sets with unchanged delivery parameters are reused so
// existing viewers stay connected across plan rebuilds. Sessions and media legs
// attached to a removed or changed variant are closed.
func (s *Service) SetVariants(variants []models.StreamOutputVariant) error {
	cfg := s.Config()
	h264Fmtp, err := canonicalH264Fmtp(cfg.H264Profile)
	if err != nil {
		return err
	}

	s.mu.Lock()
	previous := s.variants
	next := make(map[int]*variantTracks, len(variants))
	changedVariants := make([]int, 0)
	for index := range variants {
		variant := variants[index]
		if !variant.WebRTCEnabled() {
			continue
		}
		meta := Variant{
			Index:  index,
			Name:   variant.GetName(),
			Width:  variant.ScaledWidth,
			Height: variant.ScaledHeight,
			FPS:    variant.GetFramerate(),
		}
		if existing, ok := previous[index]; ok && existing.compatible(meta, h264Fmtp) {
			next[index] = existing
			continue
		}
		tracks, trackErr := newVariantTracks(meta, h264Fmtp)
		if trackErr != nil {
			s.mu.Unlock()

			return fmt.Errorf("webrtc: creating tracks for variant %d: %w", index, trackErr)
		}
		next[index] = tracks
		changedVariants = append(changedVariants, index)
	}
	for index := range previous {
		if _, ok := next[index]; !ok {
			changedVariants = append(changedVariants, index)
		}
	}
	s.variants = next
	s.mu.Unlock()

	for _, sess := range s.sessions.all() {
		current := next[sess.variant.variant.Index]
		if current == nil || current != sess.variant {
			s.dropSession(sess, "variant reconfigured")
		}
	}
	for _, index := range changedVariants {
		if err := s.CloseMediaLeg(index); err != nil {
			log.Warnf("webrtc: closing media leg for changed variant %d: %v", index, err)
		}
	}

	return nil
}

// Variants returns the WebRTC-enabled variants in index order.
func (s *Service) Variants() []Variant {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Variant, 0, len(s.variants))
	for index := 0; index < len(s.variants); index++ {
		tracks, ok := s.variants[index]
		if !ok {
			continue
		}
		out = append(out, tracks.variant)
	}

	return out
}

// Handler returns the WHEP HTTP handler. Mount it at /webrtc/ (the handler
// expects the /webrtc prefix and also tolerates a stripped prefix).
func (s *Service) Handler() http.Handler {
	return newWHEPHandler(s)
}

// ViewerCount returns the number of active WebRTC viewers.
func (s *Service) ViewerCount() int {
	return s.sessions.len()
}

// LiveViewers maps the WebRTC viewer registry onto the admin viewer rows.
func (s *Service) LiveViewers() []models.LiveViewer {
	now := time.Now()
	sessions := s.sessions.all()
	rows := make([]models.LiveViewer, 0, len(sessions))
	for _, sess := range sessions {
		snapshot := sess.snapshot(now)
		var serverMeasuredFPS *float64
		if leg := s.mediaLeg(snapshot.Variant); leg != nil {
			if measured := leg.Stats().MeasuredVideoFPS; measured > 0 {
				serverMeasuredFPS = &measured
			}
		}
		rows = append(rows, liveViewerRow(snapshot, serverMeasuredFPS))
	}

	return rows
}

// liveViewerRow maps one WebRTC viewer snapshot to an admin row. Validated
// client measurements take precedence for the fields the operator sees
// (bitrate, resolution, fps, packet loss); server measurements are the
// fallback. The row is only labelled "client" when a client value was
// actually used, so a report that failed validation cannot mislabel
// server-derived values as player-reported.
func liveViewerRow(snapshot ViewerSnapshot, serverMeasuredFPS *float64) models.LiveViewer {
	row := models.LiveViewer{
		ClientID:        snapshot.ClientID,
		IPAddress:       snapshot.IPAddress,
		UserAgent:       snapshot.UserAgent,
		Protocol:        "webrtc",
		ICETransport:    snapshot.ICETransport,
		BitrateKbps:     snapshot.BitrateKbps,
		Resolution:      snapshot.Resolution,
		PacketLossPct:   snapshot.PacketLossPct,
		JitterMs:        snapshot.JitterMs,
		ConnectedAt:     snapshot.ConnectedAt,
		DurationSeconds: snapshot.DurationSeconds,
		Source:          string(models.PlaybackSourceServer),
	}

	client := snapshot.Client
	usedClientValue := false
	if client != nil {
		if validClientResolution(client.DecodedWidth, client.DecodedHeight) {
			row.Resolution = fmt.Sprintf("%dx%d", client.DecodedWidth, client.DecodedHeight)
			usedClientValue = true
		}
		if validClientFPS(client.FPS) {
			clientFPS := client.FPS
			row.FPS = &clientFPS
			usedClientValue = true
		}
		if client.VideoBitrateKbps != nil && validClientBitrate(*client.VideoBitrateKbps) {
			clientBitrate := *client.VideoBitrateKbps
			row.BitrateKbps = &clientBitrate
			usedClientValue = true
		}
		if client.PacketLossPct != nil && validPacketLossPct(*client.PacketLossPct) {
			clientLoss := *client.PacketLossPct
			row.PacketLossPct = &clientLoss
			usedClientValue = true
		}
		// Server-detected candidate-pair transport wins; the client can only
		// supply the fallback when the server could not detect it.
		if row.ICETransport == "" {
			row.ICETransport = client.Transport
		}
	}

	if row.FPS == nil && serverMeasuredFPS != nil {
		row.FPS = serverMeasuredFPS
	}
	if usedClientValue {
		row.Source = string(models.PlaybackSourceClient)
	}

	return row
}

// Capacity returns the viewer capacity counters for the admin API.
func (s *Service) Capacity() models.WebRTCCapacity {
	snapshot := s.capacity.snapshot()

	return models.WebRTCCapacity{Used: snapshot.Used, Limit: snapshot.Limit}
}

// Warnings returns the current non-fatal WebRTC configuration and runtime
// warnings for the admin UI.
func (s *Service) Warnings() []string {
	cfg := s.Config()
	warnings := Warnings(cfg)
	if !cfg.Enabled {
		return warnings
	}
	if len(s.Variants()) == 0 {
		warnings = append(warnings, "webrtc_no_variants: no output variant has WebRTC delivery enabled")
	}
	if !s.IsStarted() {
		warnings = append(warnings, "webrtc_not_started: the WebRTC service is not running")
	}

	return warnings
}

// IsStarted reports whether the service lifecycle is running.
func (s *Service) IsStarted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.started
}

// Snapshot returns the admin Configuration page status block.
func (s *Service) Snapshot() StatusSnapshot {
	cfg := s.Config()

	return StatusSnapshot{
		Enabled:        cfg.Enabled,
		Listening:      listeningAddress(cfg),
		ActiveSessions: s.sessions.len(),
		Capacity:       s.Capacity(),
		Warnings:       s.Warnings(),
	}
}

// ApplyClientReport stores the optional browser-reported playback stats for a
// session. It returns false when the session does not exist (the public
// endpoint still answers 204 to avoid leaking session validity).
func (s *Service) ApplyClientReport(sessionID string, report ClientReport) bool {
	sess, ok := s.sessions.get(sessionID)
	if !ok {
		return false
	}
	sess.recordClientReport(report)

	return true
}

// MediaLegStats returns the media path health for a variant, if its leg is open.
func (s *Service) MediaLegStats(variantIndex int) (MediaLegStats, bool) {
	leg := s.mediaLeg(variantIndex)
	if leg == nil {
		return MediaLegStats{}, false
	}

	return leg.Stats(), true
}

func (s *Service) mediaLeg(variantIndex int) *MediaLeg {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.legs[variantIndex]
}

// janitor expires capacity reservations, prunes the rate limiter, closes idle
// sessions and periodically samples per-viewer statistics.
func (s *Service) janitor(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()

	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			tick++
			s.capacity.expire(now)
			s.limiter.sweep()
			s.reapSessions(now)
			if tick%samplesPerStats == 0 {
				s.sampleStats(now)
			}
		}
	}
}

func (s *Service) reapSessions(now time.Time) {
	idleTimeout := time.Duration(s.Config().SessionIdleTimeoutSeconds) * time.Second
	for _, sess := range s.sessions.all() {
		if sess.isClosed() {
			s.dropSession(sess, "closed")

			continue
		}
		switch sess.pc.ConnectionState() {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			s.dropSession(sess, "peer connection "+sess.pc.ConnectionState().String())

			continue
		}
		if sess.idleFor(now) > idleTimeout {
			s.dropSession(sess, "idle timeout")
		}
	}
}

func (s *Service) dropSession(sess *session, reason string) {
	if removed := s.sessions.remove(sess.id); removed == nil {
		return
	}
	sess.close(reason)
	s.capacity.releaseSession(sess.id)
	log.Debugf("webrtc: dropped session %s (%s)", sess.id, reason)
}

// dropSessionByID is used by the DELETE handler.
func (s *Service) dropSessionByID(id, reason string) bool {
	sess, ok := s.sessions.get(id)
	if !ok {
		return false
	}
	s.dropSession(sess, reason)

	return true
}

func (s *Service) takeLegsLocked() []*MediaLeg {
	legs := make([]*MediaLeg, 0, len(s.legs))
	for index, leg := range s.legs {
		legs = append(legs, leg)
		delete(s.legs, index)
	}

	return legs
}

// variantFor returns the shared track set for a variant index.
func (s *Service) variantFor(index int) (*variantTracks, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tracks, ok := s.variants[index]

	return tracks, ok
}

// firstVariant returns the lowest WebRTC-enabled variant index.
func (s *Service) firstVariant() (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	best := -1
	for index := range s.variants {
		if best == -1 || index < best {
			best = index
		}
	}
	if best == -1 {
		return 0, false
	}

	return best, true
}

func listeningAddress(cfg models.WebRTCConfig) string {
	if !cfg.Enabled {
		return ""
	}
	if cfg.UDPMode == udpModeRange {
		return fmt.Sprintf("udp://%s:%d-%d", cfg.BindAddress, cfg.UDPPortMin, cfg.UDPPortMax)
	}
	if cfg.TCPEnabled {
		return fmt.Sprintf("udp://%s:%d, tcp://%s:%d", cfg.BindAddress, cfg.UDPPort, cfg.BindAddress, cfg.TCPPort)
	}

	return fmt.Sprintf("udp://%s:%d", cfg.BindAddress, cfg.UDPPort)
}
