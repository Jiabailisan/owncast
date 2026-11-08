package webrtc

import (
	"sync"

	"github.com/pion/logging"
	log "github.com/sirupsen/logrus"
)

// levelMapping adapts Pion's leveled logger interface onto the repository
// logger so Pion diagnostics land in Owncast's normal log stream instead of
// raw stderr.
type pionLogger struct {
	scope string
}

// Pion's per-connection diagnostics (gathering, connectivity checks, "failed
// to ping without candidate pairs") are demoted to debug level: they are normal
// during viewer teardown. Only genuine Pion errors surface at warn level; the
// service itself logs the user-visible session lifecycle.
func (l pionLogger) Trace(msg string) { log.WithField("pion", l.scope).Trace(msg) }
func (l pionLogger) Tracef(format string, args ...any) {
	log.WithField("pion", l.scope).Tracef(format, args...)
}
func (l pionLogger) Debug(msg string) { log.WithField("pion", l.scope).Debug(msg) }
func (l pionLogger) Debugf(format string, args ...any) {
	log.WithField("pion", l.scope).Debugf(format, args...)
}
func (l pionLogger) Info(msg string) { log.WithField("pion", l.scope).Debug(msg) }
func (l pionLogger) Infof(format string, args ...any) {
	log.WithField("pion", l.scope).Debugf(format, args...)
}
func (l pionLogger) Warn(msg string) { log.WithField("pion", l.scope).Debug(msg) }
func (l pionLogger) Warnf(format string, args ...any) {
	log.WithField("pion", l.scope).Debugf(format, args...)
}
func (l pionLogger) Error(msg string) { log.WithField("pion", l.scope).Warn(msg) }
func (l pionLogger) Errorf(format string, args ...any) {
	log.WithField("pion", l.scope).Warnf(format, args...)
}

var _ logging.LeveledLogger = pionLogger{}

// pionLoggerFactory is shared by the SettingEngine, ICE muxes and TURN. It
// keeps a single object per scope so Pion's internal child loggers reuse it.
type pionLoggerFactory struct {
	mu      sync.Mutex
	loggers map[string]logging.LeveledLogger
}

func newPionLoggerFactory() *pionLoggerFactory {
	return &pionLoggerFactory{loggers: map[string]logging.LeveledLogger{}}
}

func (f *pionLoggerFactory) NewLogger(scope string) logging.LeveledLogger {
	f.mu.Lock()
	defer f.mu.Unlock()

	if l, ok := f.loggers[scope]; ok {
		return l
	}
	l := pionLogger{scope: scope}
	f.loggers[scope] = l

	return l
}

var _ logging.LoggerFactory = (*pionLoggerFactory)(nil)
