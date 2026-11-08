package srt

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	gosrt "github.com/datarhei/gosrt"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

func testLogger() *log.Logger {
	logger := log.New()
	logger.SetOutput(io.Discard)
	logger.SetLevel(log.PanicLevel)
	return logger
}

func testKey(key, comment string) models.StreamKey {
	keyCopy := key
	commentCopy := comment
	return models.StreamKey{Key: &keyCopy, Comment: &commentCopy}
}

func testStreamKeys() []models.StreamKey {
	return []models.StreamKey{testKey("testkey", "test comment")}
}

func testStreamKeyValidator() *ingest.StreamKeyValidator {
	return ingest.NewStreamKeyValidator(func() []models.StreamKey { return testStreamKeys() }, "")
}

// testAuthConfig is a valid configuration that listens on an ephemeral
// loopback port and only accepts the raw-key streamid format.
func testAuthConfig() models.SRTConfig {
	cfg := models.DefaultSRTConfig()
	cfg.BindAddress = "127.0.0.1"
	cfg.Port = 0
	cfg.StreamIDFormats = []string{string(models.SRTStreamIDRawKey)}
	cfg.AuthFailureRateLimit = 5
	return cfg
}

func testRemoteAddr(t *testing.T, address string) net.Addr {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatalf("ResolveUDPAddr(%q): %v", address, err)
	}
	return addr
}

func drainSession(sess ingest.Session) {
	buffer := make([]byte, 64*1024)
	for {
		if _, err := sess.Reader().Read(buffer); err != nil {
			return
		}
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func dialSRT(t *testing.T, address, streamID, passphrase string) (gosrt.Conn, error) {
	t.Helper()
	cfg := gosrt.DefaultConfig()
	cfg.StreamId = streamID
	cfg.Passphrase = passphrase
	cfg.ConnectionTimeout = 3 * time.Second
	cfg.PeerIdleTimeout = 10 * time.Second
	return gosrt.Dial("srt", address, cfg)
}

func TestServiceDisabledByDefault(t *testing.T) {
	cfg := models.DefaultSRTConfig()
	cfg.Enabled = false
	cfg.BindAddress = "127.0.0.1"
	cfg.Port = 0

	var sessions sync.Map
	svc := New(Deps{
		ConfigProvider: func() models.SRTConfig { return cfg },
		StreamKeys:     testStreamKeyValidator(),
		Sessions:       ingest.NewArbiter(),
		Handler: func(sess ingest.Session) error {
			sessions.Store(sess.ID(), sess)
			return nil
		},
		Logger: testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	status := svc.Snapshot()
	if status.Enabled {
		t.Error("Enabled should reflect the configuration")
	}
	if status.Listening {
		t.Error("a disabled service must not listen")
	}
	if status.State != StateDisabled {
		t.Errorf("State = %q, want %q", status.State, StateDisabled)
	}
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status := svc.Snapshot(); status.State != StateStopped {
		t.Errorf("State after Stop = %q, want %q", status.State, StateStopped)
	}
}

func TestServicePublishConflictDisconnectAndRebind(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Enabled = true

	sessions := make(chan ingest.Session, 8)
	arbiter := ingest.NewArbiter()
	svc := New(Deps{
		ConfigProvider: func() models.SRTConfig { return cfg },
		StreamKeys:     testStreamKeyValidator(),
		Sessions:       arbiter,
		Handler: func(sess ingest.Session) error {
			sessions <- sess
			go drainSession(sess)
			return nil
		},
		Logger: testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := svc.Stop(stopCtx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}()

	status := svc.Snapshot()
	if !status.Listening || status.State != StateRunning {
		t.Fatalf("expected a running listener, got %+v", status)
	}
	if status.Port == 0 {
		t.Fatalf("expected the ephemeral port to be reported, got %+v", status)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(status.Port))

	// A bad stream key is rejected before the arbiter is touched.
	if conn, err := dialSRT(t, address, "wrongkey", ""); err == nil {
		_ = conn.Close()
		t.Fatal("expected a rejection for an unknown stream key")
	}

	// A valid publisher is accepted and reported in the snapshot.
	first, err := dialSRT(t, address, "testkey", "")
	if err != nil {
		t.Fatalf("dial valid publisher: %v", err)
	}
	defer func() { _ = first.Close() }()

	var firstSession ingest.Session
	select {
	case firstSession = <-sessions:
	case <-time.After(5 * time.Second):
		t.Fatal("handler was not called for the accepted session")
	}

	payload := make([]byte, 1316)
	for i := range payload {
		payload[i] = 0x47
	}
	if _, err := first.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		current := svc.Snapshot()
		return current.Session != nil && current.Session.PayloadBytes >= int64(len(payload))
	}, "session payload accounting")

	if got := firstSession.StreamKeyID(); got != "test comment" {
		t.Errorf("StreamKeyID = %q, want the key comment", got)
	}

	// A second publisher is rejected with the arbiter conflict.
	if second, err := dialSRT(t, address, "testkey", ""); err == nil {
		_ = second.Close()
		t.Fatal("expected the second publisher to be rejected")
	}
	if current := arbiter.Current(); current == nil {
		t.Fatal("the first publisher should still own the arbiter slot")
	}

	// A listener-setting change is deferred while the publisher is live.
	next := cfg
	next.Passphrase = "testpassphrase123"
	if err := svc.ApplyConfig(next); err != nil {
		t.Fatalf("ApplyConfig while live: %v", err)
	}
	if !svc.Snapshot().PendingRestart {
		t.Fatal("expected the listener change to be pending")
	}
	if !svc.Snapshot().Listening {
		t.Fatal("the existing listener must keep running while the change is pending")
	}

	// Admin disconnect ends the session and applies the deferred rebind.
	if !svc.Disconnect() {
		t.Fatal("expected Disconnect to return true")
	}
	waitFor(t, 10*time.Second, func() bool {
		current := svc.Snapshot()
		return current.Session == nil && !current.PendingRestart && current.Listening
	}, "deferred listener rebind after disconnect")

	rebound := svc.Snapshot()
	if rebound.Port == status.Port {
		t.Log("note: rebind reused the same ephemeral port")
	}
	address = net.JoinHostPort("127.0.0.1", strconv.Itoa(rebound.Port))

	// The new passphrase is now required.
	if conn, err := dialSRT(t, address, "testkey", ""); err == nil {
		_ = conn.Close()
		t.Fatal("expected an unencrypted publisher to be rejected after the passphrase change")
	}
	encrypted, err := dialSRT(t, address, "testkey", "testpassphrase123")
	if err != nil {
		t.Fatalf("dial with the new passphrase: %v", err)
	}
	_ = encrypted.Close()
}

func TestServiceApplyConfigRejectsInvalidConfiguration(t *testing.T) {
	cfg := testAuthConfig()
	svc := New(Deps{
		ConfigProvider: func() models.SRTConfig { return cfg },
		StreamKeys:     testStreamKeyValidator(),
		Sessions:       ingest.NewArbiter(),
		Handler:        func(ingest.Session) error { return nil },
		Logger:         testLogger(),
	})

	invalid := cfg
	invalid.Port = 70000
	if err := svc.ApplyConfig(invalid); err == nil {
		t.Fatal("expected ApplyConfig to reject an out-of-range port")
	}

	invalid = cfg
	invalid.PayloadSize = 1500
	invalid.MSS = 1400
	if err := svc.ApplyConfig(invalid); err == nil {
		t.Fatal("expected ApplyConfig to reject payloadSize above mss minus headers")
	}
}

func TestServiceHandlerErrorClosesSession(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Enabled = true

	handlerErr := make(chan error, 1)
	arbiter := ingest.NewArbiter()
	svc := New(Deps{
		ConfigProvider: func() models.SRTConfig { return cfg },
		StreamKeys:     testStreamKeyValidator(),
		Sessions:       arbiter,
		Handler: func(ingest.Session) error {
			handlerErr <- ErrHandlerFailed
			return ErrHandlerFailed
		},
		Logger: testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = svc.Stop(stopCtx)
	}()

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(svc.Snapshot().Port))
	conn, err := dialSRT(t, address, "testkey", "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	if !errorsIsHandlerFailure(t, <-handlerErr) {
		t.Fatal("handler error was not propagated to the test")
	}

	// The handler error must close the session and release the arbiter.
	waitFor(t, 5*time.Second, func() bool {
		return svc.Snapshot().Session == nil && arbiter.Current() == nil
	}, "session cleanup after handler error")
}

func errorsIsHandlerFailure(t *testing.T, err error) bool {
	t.Helper()
	return err != nil
}
