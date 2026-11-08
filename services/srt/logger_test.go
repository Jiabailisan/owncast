package srt

import (
	"bytes"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
)

func newCapturedLogger() (*log.Logger, *bytes.Buffer) {
	buffer := &bytes.Buffer{}
	logger := log.New()
	logger.SetOutput(buffer)
	logger.SetLevel(log.DebugLevel)
	logger.SetFormatter(&log.TextFormatter{
		DisableColors:    true,
		DisableTimestamp: true,
	})
	return logger, buffer
}

func TestGosrtLoggerTopicParentMatching(t *testing.T) {
	logger, _ := newCapturedLogger()
	adapter := newGosrtLogger(logger)

	if adapter.HasTopic("connection") {
		t.Fatal("no topics configured yet")
	}

	adapter.SetTopics([]string{"connection"})
	if !adapter.HasTopic("connection") {
		t.Error("exact topic should match")
	}
	if !adapter.HasTopic("connection:new") {
		t.Error("parent topic should match a child topic")
	}
	if adapter.HasTopic("handshake:recv:cif") {
		t.Error("unrelated topic should not match")
	}

	adapter.SetTopics([]string{"handshake"})
	if adapter.HasTopic("connection") {
		t.Error("topics should be live-swappable")
	}
	if !adapter.HasTopic("handshake:recv:cif") {
		t.Error("new topic should match")
	}
}

func TestGosrtLoggerPrintOnlyEvaluatesEnabledTopics(t *testing.T) {
	logger, buffer := newCapturedLogger()
	adapter := newGosrtLogger(logger)
	adapter.SetTopics([]string{"listen"})

	called := false
	adapter.Print("connection", 0, 0, func() string {
		called = true
		return "should not be evaluated"
	})
	if called {
		t.Error("message closure must not be evaluated for a disabled topic")
	}
	if buffer.Len() != 0 {
		t.Errorf("disabled topic wrote output: %q", buffer.String())
	}

	called = false
	adapter.Print("listen", 0, 0, func() string {
		called = true
		return "listener ready"
	})
	if !called {
		t.Error("message closure should be evaluated for an enabled topic")
	}
	if !strings.Contains(buffer.String(), "listener ready") {
		t.Errorf("enabled topic did not write output: %q", buffer.String())
	}
}

func TestGosrtLoggerRedactsStreamMaterial(t *testing.T) {
	logger, buffer := newCapturedLogger()
	adapter := newGosrtLogger(logger)
	adapter.SetTopics([]string{"connection", "handshake", "data"})

	adapter.Print("connection:new", 7, 0, func() string {
		return "00000007 (publish:/live/supersecretkey)"
	})
	adapter.Print("handshake:recv:cif", 7, 0, func() string {
		return "streamid=publish:/live/supersecretkey"
	})
	adapter.Print("data:recv:dump", 7, 0, func() string {
		return "01020304"
	})

	output := buffer.String()
	if strings.Contains(output, "supersecretkey") {
		t.Fatalf("gosrt log adapter leaked a stream key: %q", output)
	}
	if !strings.Contains(output, "[redacted") {
		t.Fatalf("expected redaction markers, got: %q", output)
	}

	// Safe topics still surface their message.
	buffer.Reset()
	adapter.Print("connection:error", 7, 0, func() string {
		return "lost packets"
	})
	if !strings.Contains(buffer.String(), "lost packets") {
		t.Errorf("safe topic should not be redacted: %q", buffer.String())
	}
}

func TestGosrtLoggerNilSafety(t *testing.T) {
	var adapter *gosrtLogger
	adapter.SetTopics([]string{"connection"})
	if adapter.HasTopic("connection") {
		t.Error("nil adapter must not report topics")
	}
	adapter.Print("connection", 0, 0, nil)
	adapter.Close()
	if adapter.Listen() != nil {
		t.Error("nil adapter Listen should return nil")
	}
}
