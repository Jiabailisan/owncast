package srt

import (
	"fmt"
	"strings"
	"sync/atomic"

	gosrt "github.com/datarhei/gosrt"
	log "github.com/sirupsen/logrus"
)

// gosrtLogger adapts the pinned gosrt Logger interface to Owncast's logrus
// logger. Topics can be swapped at runtime, which makes the logTopics setting a
// live setting instead of requiring a listener restart.
//
// gosrt emits packet dumps, handshake CIF dumps and connection:new messages.
// Those can contain the raw streamid (which embeds the stream key) and media
// payload bytes, so this adapter redacts them while still reporting that the
// topic fired.
type gosrtLogger struct {
	logger *log.Logger
	topics atomic.Pointer[map[string]struct{}]
}

func newGosrtLogger(logger *log.Logger) *gosrtLogger {
	if logger == nil {
		logger = log.StandardLogger()
	}
	l := &gosrtLogger{logger: logger}
	empty := map[string]struct{}{}
	l.topics.Store(&empty)
	return l
}

// SetTopics replaces the enabled gosrt topics. An empty list disables all
// gosrt internal logging.
func (l *gosrtLogger) SetTopics(topics []string) {
	if l == nil {
		return
	}
	next := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		next[topic] = struct{}{}
	}
	l.topics.Store(&next)
}

// HasTopic implements gosrt.Logger. It mirrors gosrt's own parent-topic
// matching: a configured "connection" matches "connection:error".
func (l *gosrtLogger) HasTopic(topic string) bool {
	if l == nil {
		return false
	}
	topics := l.topics.Load()
	if topics == nil || len(*topics) == 0 {
		return false
	}
	if _, ok := (*topics)[topic]; ok {
		return true
	}
	remaining := topic
	for {
		index := strings.LastIndexByte(remaining, ':')
		if index < 0 {
			return false
		}
		remaining = remaining[:index]
		if _, ok := (*topics)[remaining]; ok {
			return true
		}
	}
}

// Print implements gosrt.Logger. The message closure is only evaluated when
// the topic is enabled, matching the library contract.
func (l *gosrtLogger) Print(topic string, socketID uint32, _ int, message func() string) {
	if l == nil || !l.HasTopic(topic) {
		return
	}

	text := "[redacted: may contain stream material]"
	if !isRedactedTopic(topic) {
		if message != nil {
			text = message()
		} else {
			text = ""
		}
	}

	l.logger.WithFields(log.Fields{
		"topic":     topic,
		"socket_id": fmt.Sprintf("%08x", socketID),
	}).Debug("gosrt: " + text)
}

// Listen implements gosrt.Logger. The service consumes logs through Print, so
// there is no channel consumer; returning nil is safe because gosrt never
// blocks on Listen.
func (l *gosrtLogger) Listen() <-chan gosrt.Log {
	return nil
}

// Close implements gosrt.Logger.
func (l *gosrtLogger) Close() {}

// isRedactedTopic reports whether the topic may contain application data
// (raw streamid, media payload, crypto key material) that must not be logged.
func isRedactedTopic(topic string) bool {
	if strings.HasSuffix(topic, ":dump") || strings.HasSuffix(topic, ":cif") {
		return true
	}
	switch topic {
	case "connection:new":
		return true
	default:
		return false
	}
}
