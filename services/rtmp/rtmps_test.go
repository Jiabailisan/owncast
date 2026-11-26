package rtmp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
)

func generateTestCertPEM(t *testing.T, commonName string) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestResolveCertFilesFormats(t *testing.T) {
	certPEM, keyPEM := generateTestCertPEM(t, "test.local")

	t.Run("directory with fullchain and privkey", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "fullchain.pem"), certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "privkey.pem"), keyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCertPath(dir); err != nil {
			t.Fatalf("ValidateCertPath(dir) failed: %v", err)
		}
	})

	t.Run("combined PEM file", func(t *testing.T) {
		dir := t.TempDir()
		combined := filepath.Join(dir, "bundle.pem")
		if err := os.WriteFile(combined, append(certPEM, keyPEM...), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCertPath(combined); err != nil {
			t.Fatalf("ValidateCertPath(combined) failed: %v", err)
		}
	})

	t.Run("cert file with sibling key", func(t *testing.T) {
		dir := t.TempDir()
		certPath := filepath.Join(dir, "stream.crt")
		keyPath := filepath.Join(dir, "stream.key")
		if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCertPath(certPath); err != nil {
			t.Fatalf("ValidateCertPath(certFile) failed: %v", err)
		}
	})

	t.Run("missing key returns error", func(t *testing.T) {
		dir := t.TempDir()
		certPath := filepath.Join(dir, "only.crt")
		if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCertPath(certPath); err == nil {
			t.Fatal("expected error when private key is missing")
		}
	})
}

func TestCertReloaderReloadsOnMtimeChange(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "fullchain.pem")
	keyFile := filepath.Join(dir, "privkey.pem")

	cert1, key1 := generateTestCertPEM(t, "v1.local")
	if err := os.WriteFile(certFile, cert1, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, key1, 0o600); err != nil {
		t.Fatal(err)
	}

	reloader, err := newCertReloader(dir)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	got1, err := reloader.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate #1: %v", err)
	}
	leaf1, err := x509.ParseCertificate(got1.Certificate[0])
	if err != nil || leaf1.Subject.CommonName != "v1.local" {
		t.Fatalf("leaf1 CN = %q, err = %v", leaf1.Subject.CommonName, err)
	}

	// Simulate Lucky renewing the certificate on disk with an advanced mtime.
	cert2, key2 := generateTestCertPEM(t, "v2.local")
	if err := os.WriteFile(certFile, cert2, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, key2, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(certFile, future, future)
	_ = os.Chtimes(keyFile, future, future)

	got2, err := reloader.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate #2: %v", err)
	}
	leaf2, err := x509.ParseCertificate(got2.Certificate[0])
	if err != nil || leaf2.Subject.CommonName != "v2.local" {
		t.Fatalf("leaf2 CN = %q, want v2.local (err=%v)", leaf2.Subject.CommonName, err)
	}
}

type fakeRTMPEngineConfig struct {
	mu        sync.Mutex
	rtmpPort  int
	rtmpsPort int
	certPath  string
	bindAddr  string
}

func (f *fakeRTMPEngineConfig) GetRTMPPortNumber() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rtmpPort
}

func (f *fakeRTMPEngineConfig) GetRTMPSPortNumber() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rtmpsPort
}

func (f *fakeRTMPEngineConfig) GetRTMPSCertPath() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.certPath
}

func (f *fakeRTMPEngineConfig) GetRTMPBindAddress() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bindAddr
}

func (f *fakeRTMPEngineConfig) GetFfMpegPath() string                            { return "ffmpeg" }
func (f *fakeRTMPEngineConfig) GetVideoCodec() string                            { return "libx264" }
func (f *fakeRTMPEngineConfig) GetS3Config() models.S3                           { return models.S3{} }
func (f *fakeRTMPEngineConfig) GetStreamLatencyLevel() models.LatencyLevel       { return models.GetLatencyLevel(2) }
func (f *fakeRTMPEngineConfig) GetStreamOutputVariants() []models.StreamOutputVariant { return nil }
func (f *fakeRTMPEngineConfig) GetVideoServingEndpoint() string                  { return "" }
func (f *fakeRTMPEngineConfig) GetStreamKeys() []models.StreamKey                { return nil }

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestRTMPSDisabledWhenUnsetAndLiveReconfigure(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCertPEM(t, "rtmps.local")
	if err := os.WriteFile(filepath.Join(dir, "fullchain.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "privkey.pem"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	rtmpPort := freeTCPPort(t)
	rtmpsPort := freeTCPPort(t)
	cfg := &fakeRTMPEngineConfig{
		rtmpPort:  rtmpPort,
		rtmpsPort: 0,
		certPath:  "",
		bindAddr:  "127.0.0.1",
	}

	svc := New(Deps{ConfigRepository: cfg})
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Start(nil, nil)
	}()
	t.Cleanup(func() {
		svc.Stop()
		<-done
	})

	// Wait until plain RTMP listener is bound.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		svc.listenerMu.Lock()
		ready := svc.listener != nil
		svc.listenerMu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// With empty certPath / port 0, RTMPS must NOT listen.
	svc.listenerMu.Lock()
	if svc.rtmpsListener != nil {
		svc.listenerMu.Unlock()
		t.Fatal("RTMPS listener must stay nil when port/certPath are unset")
	}
	svc.listenerMu.Unlock()

	// Setting only port (no cert path) must STILL not listen.
	cfg.mu.Lock()
	cfg.rtmpsPort = rtmpsPort
	cfg.mu.Unlock()
	if err := svc.ReconfigureRTMPS(); err != nil {
		t.Fatalf("ReconfigureRTMPS with empty cert path failed: %v", err)
	}
	svc.listenerMu.Lock()
	if svc.rtmpsListener != nil {
		svc.listenerMu.Unlock()
		t.Fatal("RTMPS listener must stay nil when certPath is empty")
	}
	svc.listenerMu.Unlock()

	// Setting both port and certPath starts the TLS listener immediately.
	cfg.mu.Lock()
	cfg.certPath = dir
	cfg.mu.Unlock()
	if err := svc.ReconfigureRTMPS(); err != nil {
		t.Fatalf("ReconfigureRTMPS enable failed: %v", err)
	}

	// Verify real TLS handshake succeeds over loopback.
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconvItoa(rtmpsPort)), &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // self-signed test cert
	})
	if err != nil {
		t.Fatalf("TLS dial to RTMPS port failed: %v", err)
	}
	if len(conn.ConnectionState().PeerCertificates) == 0 ||
		conn.ConnectionState().PeerCertificates[0].Subject.CommonName != "rtmps.local" {
		_ = conn.Close()
		t.Fatal("unexpected TLS peer certificate from RTMPS listener")
	}
	_ = conn.Close()

	// Clearing certPath immediately stops the RTMPS listener.
	cfg.mu.Lock()
	cfg.certPath = ""
	cfg.mu.Unlock()
	if err := svc.ReconfigureRTMPS(); err != nil {
		t.Fatalf("ReconfigureRTMPS disable failed: %v", err)
	}
	svc.listenerMu.Lock()
	if svc.rtmpsListener != nil {
		svc.listenerMu.Unlock()
		t.Fatal("RTMPS listener must stop when certPath is cleared")
	}
	svc.listenerMu.Unlock()
}

func strconvItoa(v int) string {
	return net.JoinHostPort("", big.NewInt(int64(v)).String())[1:]
}
