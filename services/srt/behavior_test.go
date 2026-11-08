package srt

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	gosrt "github.com/datarhei/gosrt"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// startLoopbackService starts a running SRT service on an ephemeral loopback
// port and returns it with the shared arbiter and the accepted-sessions
// channel. The service is stopped in test cleanup.
func startLoopbackService(t *testing.T, cfg models.SRTConfig) (*Service, *ingest.Arbiter, <-chan ingest.Session) {
	t.Helper()

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
	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = svc.Stop(stopCtx)
	})

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return svc, arbiter, sessions
}

func serviceAddress(t *testing.T, svc *Service) string {
	t.Helper()
	status := svc.Snapshot()
	if status.Port == 0 {
		t.Fatalf("service has no bound port: %+v", status)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(status.Port))
}

func dialSRTWithTimeout(t *testing.T, address, streamID, passphrase string, timeout time.Duration) (gosrt.Conn, error) {
	t.Helper()
	cfg := gosrt.DefaultConfig()
	cfg.StreamId = streamID
	cfg.Passphrase = passphrase
	cfg.ConnectionTimeout = timeout
	cfg.PeerIdleTimeout = 10 * time.Second
	return gosrt.Dial("srt", address, cfg)
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve UDP port: %v", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// T10/T11: a configured passphrase is enforced by the accept pipeline, and a
// wrong passphrase is rejected with the predefined bad-secret code.
func TestServicePassphraseEnforcement(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Enabled = true
	cfg.Passphrase = "testpassphrase123"
	cfg.EnforcedEncryption = true

	svc, _, sessions := startLoopbackService(t, cfg)
	address := serviceAddress(t, svc)

	if conn, err := dialSRT(t, address, "testkey", ""); err == nil {
		_ = conn.Close()
		t.Fatal("expected an unencrypted publisher to be rejected when a passphrase is configured")
	}
	if conn, err := dialSRT(t, address, "testkey", "wrongpassphrase99"); err == nil {
		_ = conn.Close()
		t.Fatal("expected a wrong passphrase to be rejected")
	}

	conn, err := dialSRT(t, address, "testkey", "testpassphrase123")
	if err != nil {
		t.Fatalf("dial with the configured passphrase: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case <-sessions:
	case <-time.After(5 * time.Second):
		t.Fatal("the correctly encrypted publisher was not accepted")
	}
}

// T10: CIDR deny entries are evaluated before authentication.
func TestServiceCIDRDeny(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Enabled = true
	cfg.DeniedCIDRs = []string{"127.0.0.1/32"}

	svc, arbiter, _ := startLoopbackService(t, cfg)
	address := serviceAddress(t, svc)

	if conn, err := dialSRT(t, address, "testkey", ""); err == nil {
		_ = conn.Close()
		t.Fatal("expected a denied source address to be rejected")
	}

	status := svc.Snapshot()
	if !status.Listening || status.State != StateRunning {
		t.Fatalf("the listener must stay up after a CIDR rejection: %+v", status)
	}
	if status.Session != nil || arbiter.Current() != nil {
		t.Fatal("a CIDR-denied connection must not own an ingest session")
	}
}

// T10: the per-IP auth-failure window rejects even a valid key once the budget
// is exhausted, and the window expires.
func TestServiceRateLimitWindow(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Enabled = true
	cfg.AuthFailureRateLimit = 2

	svc, _, sessions := startLoopbackService(t, cfg)
	address := serviceAddress(t, svc)

	clock := &fakeClock{now: time.Now()}
	svc.limits.setClock(clock.Now)

	for i := 0; i < 2; i++ {
		if conn, err := dialSRT(t, address, "wrongkey", ""); err == nil {
			_ = conn.Close()
			t.Fatal("expected an unknown stream key to be rejected")
		}
	}

	if conn, err := dialSRT(t, address, "testkey", ""); err == nil {
		_ = conn.Close()
		t.Fatal("expected the valid key to be rejected while the IP is rate limited")
	}
	if svc.limits.Allow(netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("the limiter should report the IP as blocked")
	}

	clock.Advance(authFailureWindow + time.Second)

	conn, err := dialSRT(t, address, "testkey", "")
	if err != nil {
		t.Fatalf("expected the valid key to be accepted after the window expired: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case <-sessions:
	case <-time.After(5 * time.Second):
		t.Fatal("the publisher was not accepted after the rate-limit window expired")
	}
}

// T11: a live-only setting applies immediately without setting PendingRestart
// and without disturbing the connected publisher.
func TestServiceLiveSettingAppliesImmediately(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Enabled = true

	svc, _, sessions := startLoopbackService(t, cfg)
	address := serviceAddress(t, svc)

	conn, err := dialSRT(t, address, "testkey", "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	select {
	case <-sessions:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher was not accepted")
	}

	next := cfg
	next.AuthFailureRateLimit = 0
	next.LogTopics = []string{"connection"}
	next.AllowedCIDRs = []string{"127.0.0.0/8"}
	if err := svc.ApplyConfig(next); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	status := svc.Snapshot()
	if status.PendingRestart {
		t.Fatal("live-only settings must not require a listener rebind")
	}
	if status.Session == nil {
		t.Fatal("the connected publisher must be unaffected by a live settings change")
	}
	if got := svc.limits.limitValue(); got != 0 {
		t.Errorf("auth failure limit = %d, want 0", got)
	}
	if !svc.gosrtLogger.HasTopic("connection") {
		t.Error("log topics were not applied live")
	}
}

// T11: a listener setting (port) applies immediately when no publisher is live.
func TestServicePortChangeAfterApplyConfig(t *testing.T) {
	firstPort := freeUDPPort(t)
	secondPort := freeUDPPort(t)

	cfg := testAuthConfig()
	cfg.Enabled = true
	cfg.Port = firstPort

	svc, _, _ := startLoopbackService(t, cfg)
	if got := svc.Snapshot().Port; got != firstPort {
		t.Fatalf("bound port = %d, want %d", got, firstPort)
	}

	firstConn, err := dialSRT(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(firstPort)), "testkey", "")
	if err != nil {
		t.Fatalf("dial the first port: %v", err)
	}
	_ = firstConn.Close()
	waitFor(t, 5*time.Second, func() bool { return svc.Snapshot().Session == nil }, "first session teardown")

	next := cfg
	next.Port = secondPort
	if err := svc.ApplyConfig(next); err != nil {
		t.Fatalf("ApplyConfig with a new port: %v", err)
	}

	status := svc.Snapshot()
	if !status.Listening || status.Port != secondPort || status.PendingRestart {
		t.Fatalf("expected a re-bound listener on port %d: %+v", secondPort, status)
	}
	if status.LastError != "" {
		t.Errorf("LastError = %q, want empty", status.LastError)
	}

	secondConn, err := dialSRT(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(secondPort)), "testkey", "")
	if err != nil {
		t.Fatalf("dial the new port: %v", err)
	}
	_ = secondConn.Close()

	// The old port must be released: binding it again succeeds.
	packetConn, err := net.ListenPacket("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(firstPort)))
	if err != nil {
		t.Fatalf("old port was not released: %v", err)
	}
	_ = packetConn.Close()
}

// T11: a disabled listener must not bind its UDP port at all.
func TestServiceDisabledDoesNotBindSocket(t *testing.T) {
	port := freeUDPPort(t)

	cfg := models.DefaultSRTConfig()
	cfg.Enabled = false
	cfg.BindAddress = "127.0.0.1"
	cfg.Port = port

	svc := New(Deps{
		ConfigProvider: func() models.SRTConfig { return cfg },
		StreamKeys:     testStreamKeyValidator(),
		Sessions:       ingest.NewArbiter(),
		Handler:        func(ingest.Session) error { return nil },
		Logger:         testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = svc.Stop(stopCtx)
	}()

	status := svc.Snapshot()
	if status.State != StateDisabled || status.Listening {
		t.Fatalf("expected a disabled, non-listening service: %+v", status)
	}

	socket, err := net.ListenPacket("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("disabled service bound the configured UDP port: %v", err)
	}
	_ = socket.Close()
}
