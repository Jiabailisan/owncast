package rtmp

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	joy5rtmp "github.com/nareix/joy5/format/rtmp"
	log "github.com/sirupsen/logrus"
)

const rtmpsHandshakeTimeout = 10 * time.Second

// ValidateCertPath verifies that certPath resolves to a readable, matching
// X.509 certificate and private key pair. certPath may be:
//   - a directory containing cert + key files (e.g. fullchain.pem + privkey.pem, or *.crt + *.key)
//   - a combined PEM file containing both CERTIFICATE and PRIVATE KEY blocks
//   - a certificate file whose matching private key sits alongside it in the same directory
func ValidateCertPath(certPath string) error {
	certFile, keyFile, err := resolveCertFiles(certPath)
	if err != nil {
		return err
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return fmt.Errorf("unable to load TLS certificate pair (%s, %s): %w", certFile, keyFile, err)
	}
	return nil
}

// resolveCertFiles maps a user-supplied certificate path (directory, combined
// PEM file, or cert file) to concrete (certFile, keyFile) paths.
func resolveCertFiles(certPath string) (string, string, error) {
	cleaned := strings.TrimSpace(certPath)
	if cleaned == "" {
		return "", "", errors.New("certificate path is empty")
	}

	info, err := os.Stat(cleaned)
	if err != nil {
		return "", "", fmt.Errorf("cannot access certificate path %q: %w", cleaned, err)
	}

	if info.IsDir() {
		return resolveCertFilesInDir(cleaned)
	}

	// Regular file: check if it is a combined PEM (both cert and private key).
	hasCert, hasKey := inspectPEMFile(cleaned)
	if hasCert && hasKey {
		return cleaned, cleaned, nil
	}
	if !hasCert {
		return "", "", fmt.Errorf("file %q does not contain a PEM CERTIFICATE block", cleaned)
	}

	// Single cert file: look for its matching private key in the same directory.
	dir := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	keyFile, err := findKeyInDir(dir, stem, cleaned)
	if err != nil {
		return "", "", fmt.Errorf("found certificate %q, but %w", cleaned, err)
	}
	return cleaned, keyFile, nil
}

func resolveCertFilesInDir(dir string) (string, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", fmt.Errorf("cannot read certificate directory %q: %w", dir, err)
	}

	// Priority cert filenames commonly produced by Lucky, Certbot, acme.sh, Nginx.
	preferredCerts := []string{"fullchain.pem", "cert.pem", "certificate.pem", "server.crt", "server.pem", "fullchain.crt"}
	for _, name := range preferredCerts {
		candidate := filepath.Join(dir, name)
		hasCert, hasKey := inspectPEMFile(candidate)
		if hasCert && hasKey {
			return candidate, candidate, nil
		}
		if hasCert {
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			keyFile, kerr := findKeyInDir(dir, stem, candidate)
			if kerr == nil {
				return candidate, keyFile, nil
			}
		}
	}

	var certCandidates []string
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".pem" && ext != ".crt" && ext != ".cer" {
			continue
		}
		full := filepath.Join(dir, name)
		hasCert, hasKey := inspectPEMFile(full)
		if hasCert && hasKey {
			return full, full, nil
		}
		if hasCert {
			certCandidates = append(certCandidates, full)
		}
	}

	if len(certCandidates) == 0 {
		return "", "", fmt.Errorf("no PEM certificate file found in %q", dir)
	}

	// Prefer a candidate whose paired key actually validates with tls.LoadX509KeyPair
	// so directories containing ca.cer / chain.pem alongside the leaf cert work.
	var firstErr error
	for _, certFile := range certCandidates {
		stem := strings.TrimSuffix(filepath.Base(certFile), filepath.Ext(certFile))
		keyFile, err := findKeyInDir(dir, stem, certFile)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("found certificate %q in %q, but %w", filepath.Base(certFile), dir, err)
			}
			continue
		}
		if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("unable to load TLS certificate pair (%s, %s): %w", certFile, keyFile, err)
			}
			continue
		}
		return certFile, keyFile, nil
	}
	return "", "", firstErr
}

func findKeyInDir(dir, certStem, certFile string) (string, error) {
	preferredKeys := []string{
		certStem + ".key",
		"privkey.pem",
		"key.pem",
		"private.key",
		"server.key",
		"privkey.key",
	}
	for _, name := range preferredKeys {
		candidate := filepath.Join(dir, name)
		if _, hasKey := inspectPEMFile(candidate); hasKey {
			return candidate, nil
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("cannot read key directory %q: %w", dir, err)
	}
	var keyCandidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if full == certFile {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".key" && ext != ".pem" {
			continue
		}
		if _, hasKey := inspectPEMFile(full); hasKey {
			keyCandidates = append(keyCandidates, full)
		}
	}
	if len(keyCandidates) == 0 {
		return "", fmt.Errorf("no matching PEM private key found in %q (expected e.g. %s.key, privkey.pem, or key.pem)", dir, certStem)
	}
	sort.Strings(keyCandidates)
	return keyCandidates[0], nil
}

const maxPEMInspectBytes = 256 * 1024

func inspectPEMFile(path string) (hasCert, hasKey bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxPEMInspectBytes {
		return false, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // admin-configured TLS certificate path
	if err != nil {
		return false, false
	}
	hasCert = bytes.Contains(data, []byte("-----BEGIN CERTIFICATE-----"))
	hasKey = bytes.Contains(data, []byte("PRIVATE KEY-----"))
	return hasCert, hasKey
}

// certReloader caches the active TLS certificate and transparently reloads it
// whenever the certificate or private key file mtime changes on disk (e.g. when
// Lucky or Certbot renews the certificate).
type certReloader struct {
	certPath string

	mu        sync.RWMutex
	certFile  string
	keyFile   string
	certMtime time.Time
	keyMtime  time.Time
	cert      *tls.Certificate
}

func newCertReloader(certPath string) (*certReloader, error) {
	r := &certReloader{certPath: strings.TrimSpace(certPath)}
	if _, _, _, err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certReloader) reload() (string, string, *tls.Certificate, error) {
	certFile, keyFile, err := resolveCertFiles(r.certPath)
	if err != nil {
		return "", "", nil, err
	}
	certInfo, err := os.Stat(certFile)
	if err != nil {
		return "", "", nil, err
	}
	keyInfo, err := os.Stat(keyFile)
	if err != nil {
		return "", "", nil, err
	}
	loaded, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return "", "", nil, fmt.Errorf("unable to load TLS certificate pair (%s, %s): %w", certFile, keyFile, err)
	}

	r.mu.Lock()
	r.certFile = certFile
	r.keyFile = keyFile
	r.certMtime = certInfo.ModTime()
	r.keyMtime = keyInfo.ModTime()
	r.cert = &loaded
	r.mu.Unlock()
	return certFile, keyFile, &loaded, nil
}

func (r *certReloader) getCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	cached := r.cert
	certFile := r.certFile
	keyFile := r.keyFile
	certMtime := r.certMtime
	keyMtime := r.keyMtime
	r.mu.RUnlock()

	certInfo, certErr := os.Stat(certFile)
	keyInfo, keyErr := os.Stat(keyFile)
	if certErr == nil && keyErr == nil &&
		certInfo.ModTime().Equal(certMtime) && keyInfo.ModTime().Equal(keyMtime) &&
		cached != nil {
		return cached, nil
	}

	loadedCertFile, loadedKeyFile, loadedCert, err := r.reload()
	if err != nil {
		if cached != nil {
			log.WithError(err).Warn("RTMPS certificate reload failed; continuing with previously loaded certificate")
			return cached, nil
		}
		return nil, err
	}
	log.WithFields(log.Fields{
		"cert": loadedCertFile,
		"key":  loadedKeyFile,
	}).Info("RTMPS TLS certificate reloaded")

	return loadedCert, nil
}

// ReconfigureRTMPS applies the current RTMPS port and certificate path settings
// live. When either the RTMPS port is unset (<= 0) or the certificate path is
// empty, RTMPS stops listening without affecting the plain RTMP listener.
func (s *Service) ReconfigureRTMPS() error {
	s.rtmpsReconfigMu.Lock()
	defer s.rtmpsReconfigMu.Unlock()

	s.listenerMu.Lock()
	srv := s.rtmpServer
	prev := s.rtmpsListener
	s.rtmpsListener = nil
	s.listenerMu.Unlock()

	if prev != nil {
		_ = prev.Close()
	}

	// If Start() has not run yet, the listener will be configured on Start.
	if srv == nil || s.configRepository == nil {
		return nil
	}

	port := s.configRepository.GetRTMPSPortNumber()
	certPath := strings.TrimSpace(s.configRepository.GetRTMPSCertPath())
	if port <= 0 || certPath == "" {
		if prev != nil {
			log.Info("RTMPS listener stopped (disabled).")
		}
		return nil
	}

	reloader, err := newCertReloader(certPath)
	if err != nil {
		return err
	}

	bindAddr := s.configRepository.GetRTMPBindAddress()
	listenAddress := net.JoinHostPort(bindAddr, strconv.Itoa(port))
	tlsCfg := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: reloader.getCertificate,
	}

	lis, err := tls.Listen("tcp", listenAddress, tlsCfg)
	if err != nil {
		return fmt.Errorf("unable to listen for RTMPS on %s: %w", listenAddress, err)
	}

	s.listenerMu.Lock()
	// Guard against Stop() racing with ReconfigureRTMPS().
	if s.listener == nil {
		s.listenerMu.Unlock()
		_ = lis.Close()
		return nil
	}
	replaced := s.rtmpsListener
	s.rtmpsListener = lis
	s.listenerMu.Unlock()
	if replaced != nil {
		_ = replaced.Close()
	}

	reloader.mu.RLock()
	activeCertFile := reloader.certFile
	reloader.mu.RUnlock()
	log.Infof("RTMPS is accepting inbound TLS streams on %s (cert: %s).", listenAddress, activeCertFile)
	go s.serveRTMPS(lis, srv)
	return nil
}

func (s *Service) rtmpsListenerActive(lis net.Listener) bool {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	return s.rtmpsListener == lis
}

func (s *Service) serveRTMPS(lis net.Listener, srv *joy5rtmp.Server) {
	for {
		nc, err := lis.Accept()
		if err != nil {
			if !s.rtmpsListenerActive(lis) {
				return
			}
			time.Sleep(time.Second)
			continue
		}
		go func(conn net.Conn) {
			if tlsConn, ok := conn.(*tls.Conn); ok {
				_ = tlsConn.SetDeadline(time.Now().Add(rtmpsHandshakeTimeout))
				if err := tlsConn.Handshake(); err != nil {
					log.WithError(err).Debugf("RTMPS TLS handshake failed from %s", conn.RemoteAddr())
					_ = conn.Close()
					return
				}
				_ = tlsConn.SetDeadline(time.Time{})
			}
			srv.HandleNetConn(conn)
		}(nc)
	}
}
