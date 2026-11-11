package srt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	gosrt "github.com/datarhei/gosrt"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// State is the lifecycle state of the SRT service.
type State string

const (
	// StateStopped means Start has not been called yet or Stop completed.
	StateStopped State = "stopped"
	// StateDisabled means the configured listener is disabled.
	StateDisabled State = "disabled"
	// StateRunning means the listener is bound and accepting connections.
	StateRunning State = "running"
	// StateFailed means the listener could not be bound or died unexpectedly.
	StateFailed State = "failed"
)

// Deps is the explicit dependency contract of the SRT service. It is
// constructed once in main.go (the composition root).
type Deps struct {
	// ConfigProvider returns the current persisted SRT configuration. It is
	// called on Start and by ApplyConfig callers, never from the accept
	// callback.
	ConfigProvider func() models.SRTConfig
	// StreamKeys is the shared stream-key validator (same instance as RTMP).
	StreamKeys *ingest.StreamKeyValidator
	// Sessions is the cross-protocol single-publisher arbiter.
	Sessions *ingest.Arbiter

	// IsOnline reports whether a stream is live or still being torn down. The
	// arbiter slot is released when the previous session closes, before the stream
	// engine finishes its teardown, so without this a publisher that reconnects in
	// that window is accepted and then killed by the teardown it ran into.
	IsOnline func() bool
	// Handler starts the media pipeline for an accepted session. It must
	// return promptly; a non-nil error closes the session.
	Handler func(ingest.Session) error
	// Logger is the service logger. A nil logger uses the standard logger.
	Logger *log.Logger
}

// SessionStatus is the live view of the currently connected SRT publisher.
type SessionStatus struct {
	ID           string
	RemoteAddr   string
	StreamKeyID  string
	InputFormat  string
	StartedAt    time.Time
	Duration     time.Duration
	PayloadBytes int64
	Version      uint32
	Encrypted    bool
	Stats        ingest.Stats
}

// Status is a point-in-time snapshot of the SRT service for the admin live
// status API. It is read-only and safe to call at any time.
type Status struct {
	Enabled        bool
	State          State
	Listening      bool
	Port           int
	BoundAddress   string
	StartedAt      time.Time
	PendingRestart bool
	LastError      string
	Session        *SessionStatus
}

// Service is the SRT ingest service: listener lifecycle, authentication
// pipeline, session adapters and configuration reload semantics. It has no
// package-level state.
type Service struct {
	deps   Deps
	logger *log.Logger

	gosrtLogger       *gosrtLogger
	limits            *authFailureLimiter
	pendingHandshakes *pendingCounter

	// lifecycleMu serializes Start, Stop, ApplyConfig and the deferred
	// pending-restart application so listener rebinds never interleave.
	lifecycleMu sync.Mutex

	// mu guards the fields below. It is never held while waiting for
	// goroutines.
	mu           sync.Mutex
	state        State
	cfg          models.SRTConfig
	pendingCfg   *models.SRTConfig
	listener     gosrt.Listener
	listenerDone chan struct{}
	boundAddr    string
	lastError    string
	startedAt    time.Time
	ctx          context.Context
	cancel       context.CancelFunc
	stopping     bool
	wg           *sync.WaitGroup

	auth atomic.Pointer[authConfig]

	activeMu sync.Mutex
	active   *session
}

// New constructs an idle SRT service. Call Start to bind the listener.
func New(deps Deps) *Service {
	logger := deps.Logger
	if logger == nil {
		logger = log.StandardLogger()
	}

	defaults := models.DefaultSRTConfig()
	service := &Service{
		deps:              deps,
		logger:            logger,
		gosrtLogger:       newGosrtLogger(logger),
		limits:            newAuthFailureLimiter(defaults.AuthFailureRateLimit),
		pendingHandshakes: newPendingCounter(defaults.MaxPendingHandshakes),
		wg:                &sync.WaitGroup{},
		state:             StateStopped,
		cfg:               defaults,
	}
	if err := service.applyRuntime(defaults, defaults.Passphrase); err != nil {
		service.lastError = err.Error()
	}
	return service
}

// Start reads the configuration and binds the SRT listener when enabled. When
// the feature is disabled it is a no-op that records the disabled state.
func (s *Service) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.validateDeps(); err != nil {
		s.setFailure(err)
		return err
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	if s.state == StateRunning {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	cfg := s.deps.ConfigProvider()
	if err := ValidateConfig(cfg); err != nil {
		s.setFailure(err)
		return err
	}
	if err := s.applyRuntime(cfg, cfg.Passphrase); err != nil {
		s.setFailure(err)
		return err
	}

	serviceCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.cfg = cfg
	s.ctx = serviceCtx
	s.cancel = cancel
	s.stopping = false
	s.wg = &sync.WaitGroup{}
	s.startedAt = time.Now()
	s.lastError = ""
	s.pendingCfg = nil
	if !cfg.Enabled {
		s.state = StateDisabled
		s.mu.Unlock()
		s.logger.Info("SRT ingest is disabled")
		return nil
	}
	s.mu.Unlock()

	s.spawn(func() { s.limits.RunSweeper(serviceCtx) })

	if err := s.bindAndServe(serviceCtx, cfg); err != nil {
		cancel()
		s.mu.Lock()
		s.ctx = nil
		s.cancel = nil
		s.mu.Unlock()
		return err
	}

	return nil
}

// Stop closes the listener and waits for every service goroutine to exit. The
// context bounds the wait; a timeout still transitions the service to stopped
// and returns the context error.
func (s *Service) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	if s.state == StateStopped {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	cancel := s.cancel
	listener := s.listener
	listenerDone := s.listenerDone
	wg := s.wg
	s.listener = nil
	s.listenerDone = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		listener.Close()
		if listenerDone != nil {
			waitForChannel(listenerDone, acceptLoopShutdownTimeout)
		}
	}

	var waitErr error
	if wg != nil {
		waitErr = waitForGroup(ctx, wg)
		if waitErr != nil {
			s.logger.WithError(waitErr).Warn("SRT service stop timed out waiting for goroutines")
		}
	}

	s.mu.Lock()
	s.state = StateStopped
	s.ctx = nil
	s.cancel = nil
	s.listener = nil
	s.listenerDone = nil
	s.boundAddr = ""
	s.pendingCfg = nil
	s.stopping = false
	s.startedAt = time.Time{}
	s.mu.Unlock()

	return waitErr
}

// Disconnect force-disconnects the active SRT publisher. It returns false when
// no SRT session is connected (an RTMP publisher is left untouched).
func (s *Service) Disconnect() bool {
	if s.deps.Sessions == nil {
		return false
	}
	current := s.deps.Sessions.Current()
	if current == nil || current.Protocol() != ingest.ProtocolSRT {
		return false
	}
	current.Close(ErrAdminDisconnect)
	return true
}

// ApplyConfig validates and applies a new SRT configuration. Live settings
// take effect immediately. Listener settings require a rebind: they are applied
// immediately when no publisher is connected, otherwise they are deferred until
// the current stream ends (see Status.PendingRestart).
func (s *Service) ApplyConfig(cfg models.SRTConfig) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	deferred, err := s.applyConfigLocked(cfg)
	if err != nil {
		return err
	}
	if !deferred {
		s.mu.Lock()
		s.pendingCfg = nil
		s.mu.Unlock()
	}
	return nil
}

// applyConfigLocked implements ApplyConfig; the caller holds lifecycleMu. It
// reports whether the listener setting was deferred until the current stream
// ends.
func (s *Service) applyConfigLocked(cfg models.SRTConfig) (bool, error) {
	s.mu.Lock()
	currentState := s.state
	currentCfg := s.cfg
	s.mu.Unlock()

	if !listenerSettingsChanged(currentCfg, cfg) {
		return s.applyLiveSettings(cfg)
	}
	if currentState == StateRunning && hasActiveSession(s) {
		return s.deferListenerSettings(currentCfg, cfg)
	}
	if err := s.applyRuntime(cfg, cfg.Passphrase); err != nil {
		return false, err
	}
	return s.applyListenerSettings(currentState, currentCfg, cfg)
}

// applyLiveSettings swaps the runtime configuration for settings that never
// require rebinding, leaving the listener and any publisher untouched.
func (s *Service) applyLiveSettings(cfg models.SRTConfig) (bool, error) {
	if err := s.applyRuntime(cfg, cfg.Passphrase); err != nil {
		return false, err
	}
	s.storeConfig(cfg)
	return false, nil
}

// deferListenerSettings keeps the bound listener (and its passphrase)
// consistent until the current publisher disconnects while applying the live
// settings immediately.
func (s *Service) deferListenerSettings(currentCfg, cfg models.SRTConfig) (bool, error) {
	if err := s.applyRuntime(cfg, currentCfg.Passphrase); err != nil {
		return false, err
	}
	s.mu.Lock()
	pending := cfg
	s.pendingCfg = &pending
	s.mu.Unlock()
	s.logger.Info("SRT listener configuration change deferred until the current stream ends")
	return true, nil
}

// applyListenerSettings applies a listener configuration change that is not
// deferred: it rebinds, disables or restores the listener as appropriate.
func (s *Service) applyListenerSettings(currentState State, currentCfg, cfg models.SRTConfig) (bool, error) {
	switch {
	case currentState == StateRunning && cfg.Enabled:
		ctx := s.serviceContext()
		if ctx == nil {
			return false, ErrServiceNotRunning
		}
		return false, s.rebindWithRollback(ctx, currentCfg, cfg)

	case currentState == StateRunning && !cfg.Enabled:
		s.disableListener(cfg)
		return false, nil

	case currentState == StateDisabled && cfg.Enabled:
		ctx := s.serviceContext()
		if ctx == nil {
			return false, ErrServiceNotRunning
		}
		return false, s.bindAndServe(ctx, cfg)

	default:
		s.storeConfig(cfg)
		return false, nil
	}
}

// serviceContext returns the service context created by Start.
func (s *Service) serviceContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

// disableListener closes the bound listener and records the disabled state.
func (s *Service) disableListener(cfg models.SRTConfig) {
	s.stopListenerLocked()
	s.mu.Lock()
	s.cfg = cfg
	s.state = StateDisabled
	s.boundAddr = ""
	s.mu.Unlock()
	s.logger.Info("SRT ingest is disabled")
}

// storeConfig records the applied configuration without touching the listener.
func (s *Service) storeConfig(cfg models.SRTConfig) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// Snapshot returns a point-in-time view for the admin live status API.
func (s *Service) Snapshot() Status {
	s.mu.Lock()
	status := Status{
		Enabled:        s.cfg.Enabled,
		State:          s.state,
		Listening:      s.state == StateRunning && s.listener != nil,
		Port:           s.cfg.Port,
		BoundAddress:   s.boundAddr,
		StartedAt:      s.startedAt,
		PendingRestart: s.pendingCfg != nil,
		LastError:      s.lastError,
	}
	s.mu.Unlock()

	if status.Listening && status.BoundAddress != "" {
		if _, portText, err := net.SplitHostPort(status.BoundAddress); err == nil {
			if port, err := strconv.Atoi(portText); err == nil {
				status.Port = port
			}
		}
	}

	s.activeMu.Lock()
	active := s.active
	s.activeMu.Unlock()
	if active != nil {
		status.Session = &SessionStatus{
			ID:           active.ID(),
			RemoteAddr:   remoteString(active.RemoteAddr()),
			StreamKeyID:  active.StreamKeyID(),
			InputFormat:  active.InputFormat().String(),
			StartedAt:    active.StartedAt(),
			Duration:     active.Age(),
			PayloadBytes: active.PayloadBytes(),
			Version:      active.Version(),
			Encrypted:    active.Encrypted(),
			Stats:        active.Stats(),
		}
	}

	return status
}

// bindAndServe binds a listener and starts its accept loop. The caller holds
// lifecycleMu; ctx is the service context.
func (s *Service) bindAndServe(ctx context.Context, cfg models.SRTConfig) error {
	listener, err := s.bindListener(cfg)
	if err != nil {
		s.setFailure(err)
		return err
	}

	done := make(chan struct{})
	s.mu.Lock()
	s.listener = listener
	s.listenerDone = done
	s.cfg = cfg
	s.boundAddr = listener.Addr().String()
	s.state = StateRunning
	s.startedAt = time.Now()
	s.lastError = ""
	s.mu.Unlock()

	if !s.spawn(func() {
		defer close(done)
		s.acceptLoop(ctx, listener)
	}) {
		listener.Close()
		return ErrServiceNotRunning
	}

	s.logger.WithField("address", s.boundAddress()).Info("SRT listener started")
	return nil
}

// rebindWithRollback stops the current listener and binds the new
// configuration. If the new bind fails and previousCfg was a working listener
// configuration, it re-binds previousCfg so the service keeps serving the
// previous working configuration; the original error is still returned (the
// admin API reports 409 and keeps the persisted configuration) and is recorded
// in Snapshot.LastError for the admin live status page.
func (s *Service) rebindWithRollback(ctx context.Context, previousCfg, nextCfg models.SRTConfig) error {
	s.stopListenerLocked()

	if err := s.bindAndServe(ctx, nextCfg); err == nil {
		return nil
	} else if !previousCfg.Enabled {
		return err
	} else {
		bindErr := err

		if restoreErr := s.applyRuntime(previousCfg, previousCfg.Passphrase); restoreErr != nil {
			s.setFailure(fmt.Errorf("srt: rebind failed (%w) and restoring runtime failed: %w", bindErr, restoreErr))
			return bindErr
		}
		if restoreErr := s.bindAndServe(ctx, previousCfg); restoreErr != nil {
			s.setFailure(fmt.Errorf("srt: rebind failed (%w) and restoring the previous listener failed: %w", bindErr, restoreErr))
			return bindErr
		}

		s.mu.Lock()
		s.lastError = bindErr.Error()
		s.mu.Unlock()
		s.logger.WithError(bindErr).Warn("SRT listener rebind failed; restored the previous working listener")
		return bindErr
	}
}

// stopListenerLocked closes the bound listener and waits for its accept loop.
// The caller holds lifecycleMu. The listener config is left in place; callers
// update it explicitly.
func (s *Service) stopListenerLocked() {
	s.mu.Lock()
	listener := s.listener
	listenerDone := s.listenerDone
	s.listener = nil
	s.listenerDone = nil
	s.mu.Unlock()

	if listener == nil {
		return
	}
	listener.Close()
	if listenerDone != nil {
		waitForChannel(listenerDone, acceptLoopShutdownTimeout)
	}
}

// applyRuntime swaps the atomic authentication snapshot and the live limits.
// passphrase is supplied separately because it must stay consistent with the
// bound gosrt listener while a rebind is pending.
func (s *Service) applyRuntime(cfg models.SRTConfig, passphrase string) error {
	auth, err := newAuthConfig(cfg, passphrase)
	if err != nil {
		return err
	}
	s.auth.Store(auth)
	s.limits.SetLimit(cfg.AuthFailureRateLimit)
	s.pendingHandshakes.SetLimit(cfg.MaxPendingHandshakes)
	s.gosrtLogger.SetTopics(cfg.LogTopics)
	return nil
}

// maybeApplyPending schedules the deferred listener rebind if a
// listener-settings change is waiting and the stream just ended.
func (s *Service) maybeApplyPending() {
	s.mu.Lock()
	hasPending := s.pendingCfg != nil
	running := s.state == StateRunning
	s.mu.Unlock()
	if !hasPending || !running {
		return
	}
	s.spawn(s.applyPending)
}

func (s *Service) applyPending() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	next := s.pendingCfg
	s.mu.Unlock()
	if next == nil {
		return
	}

	s.logger.Info("applying deferred SRT listener configuration")
	deferred, err := s.applyConfigLocked(*next)
	if err != nil {
		s.mu.Lock()
		s.pendingCfg = nil
		s.mu.Unlock()
		s.logger.WithError(err).Error("failed to apply deferred SRT listener configuration; previous listener kept")
		return
	}
	if !deferred {
		s.mu.Lock()
		s.pendingCfg = nil
		s.mu.Unlock()
	}
}

// spawn starts fn in a goroutine tracked by the service WaitGroup. It returns
// false when the service is stopping, in which case fn must not run.
func (s *Service) spawn(fn func()) bool {
	s.mu.Lock()
	if s.stopping || s.wg == nil {
		s.mu.Unlock()
		return false
	}
	wg := s.wg
	wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer wg.Done()
		fn()
	}()
	return true
}

func (s *Service) validateDeps() error {
	if s.deps.ConfigProvider == nil {
		return errors.New("srt: ConfigProvider is required")
	}
	if s.deps.Handler == nil {
		return ErrNoHandler
	}
	if s.deps.StreamKeys == nil {
		return errors.New("srt: StreamKeys is required")
	}
	if s.deps.Sessions == nil {
		return errors.New("srt: Sessions is required")
	}
	return nil
}

func (s *Service) setFailure(err error) {
	s.mu.Lock()
	s.state = StateFailed
	s.lastError = err.Error()
	s.mu.Unlock()
}

// clearListener releases the listener reference so the status stops reporting a
// listener that is no longer accepting.
func (s *Service) clearListener() {
	s.mu.Lock()
	s.listener = nil
	s.listenerDone = nil
	s.mu.Unlock()
}

func (s *Service) boundAddress() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boundAddr
}

func hasActiveSession(s *Service) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return s.active != nil
}

// waitForGroup waits for wg or the context deadline.
func waitForGroup(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitForChannel waits for ch to close or the timeout to elapse.
func waitForChannel(ch <-chan struct{}, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}
}
