package srt

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"

	gosrt "github.com/datarhei/gosrt"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// fakeConnRequest implements gosrt.ConnRequest for accept-pipeline unit tests.
type fakeConnRequest struct {
	remote           net.Addr
	version          uint32
	streamID         string
	encrypted        bool
	setPassphraseErr error
	acceptErr        error
	conn             gosrt.Conn

	passphrase string
	accepted   bool
	rejected   bool
	rejection  gosrt.RejectionReason
}

func (r *fakeConnRequest) RemoteAddr() net.Addr { return r.remote }
func (r *fakeConnRequest) Version() uint32      { return r.version }
func (r *fakeConnRequest) StreamId() string     { return r.streamID }
func (r *fakeConnRequest) SocketId() uint32     { return 1 }
func (r *fakeConnRequest) PeerSocketId() uint32 { return 2 }
func (r *fakeConnRequest) IsEncrypted() bool    { return r.encrypted }

func (r *fakeConnRequest) SetPassphrase(passphrase string) error {
	if r.setPassphraseErr != nil {
		return r.setPassphraseErr
	}
	r.passphrase = passphrase
	return nil
}

func (r *fakeConnRequest) SetRejectionReason(reason gosrt.RejectionReason) {
	r.rejection = reason
}

func (r *fakeConnRequest) Accept() (gosrt.Conn, error) {
	if r.acceptErr != nil {
		return nil, r.acceptErr
	}
	r.accepted = true
	if r.conn == nil {
		r.conn = &fakeConn{readErr: io.EOF}
	}
	return r.conn, nil
}

func (r *fakeConnRequest) Reject(reason gosrt.RejectionReason) {
	r.rejected = true
	r.rejection = reason
}

func newAuthTestService(t *testing.T, cfg *models.SRTConfig) *Service {
	t.Helper()
	svc := New(Deps{
		ConfigProvider: func() models.SRTConfig { return *cfg },
		StreamKeys:     testStreamKeyValidator(),
		Sessions:       ingest.NewArbiter(),
		Handler:        func(ingest.Session) error { return nil },
		Logger:         testLogger(),
	})
	if err := svc.ApplyConfig(*cfg); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	return svc
}

func TestAuthenticateAcceptsValidKey(t *testing.T) {
	cfg := testAuthConfig()
	svc := newAuthTestService(t, &cfg)

	request := &fakeConnRequest{
		remote:   testRemoteAddr(t, "127.0.0.1:5000"),
		version:  5,
		streamID: "testkey",
		conn:     &fakeConn{readErr: io.EOF},
	}

	sess, ok := svc.authenticate(request)
	if !ok {
		t.Fatal("expected the request to be accepted")
	}
	defer sess.Close(ErrRemoteClosed)

	if !request.accepted {
		t.Error("ConnRequest.Accept was not called")
	}
	if request.rejected {
		t.Error("ConnRequest.Reject must not be called for an accepted request")
	}
	if got := sess.StreamKeyID(); got != "test comment" {
		t.Errorf("StreamKeyID = %q, want the key comment", got)
	}
	if got := sess.InputFormat().Name; got != "mpegts" {
		t.Errorf("InputFormat = %q, want mpegts", got)
	}
	if current := svc.deps.Sessions.Current(); current == nil || current.ID() != sess.ID() {
		t.Error("accepted session should own the arbiter slot")
	}
}

func TestAuthenticateRejections(t *testing.T) {
	tests := []struct {
		name      string
		mutateCfg func(*models.SRTConfig)
		request   func(t *testing.T) *fakeConnRequest
		wireCode  gosrt.RejectionReason
	}{
		{
			name: "empty streamid",
			request: func(t *testing.T) *fakeConnRequest {
				return &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:5001"), version: 5}
			},
			wireCode: gosrt.REJ_PEER,
		},
		{
			name: "format disabled",
			mutateCfg: func(cfg *models.SRTConfig) {
				cfg.StreamIDFormats = []string{string(models.SRTStreamIDAccessControl)}
			},
			request: func(t *testing.T) *fakeConnRequest {
				return &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:5002"), version: 5, streamID: "testkey"}
			},
			wireCode: gosrt.REJ_PEER,
		},
		{
			name: "subscriber mode",
			mutateCfg: func(cfg *models.SRTConfig) {
				cfg.StreamIDFormats = models.AllSRTStreamIDFormats()
			},
			request: func(t *testing.T) *fakeConnRequest {
				return &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:5003"), version: 5, streamID: "#!::r=live/testkey"}
			},
			wireCode: gosrt.REJ_PEER,
		},
		{
			name: "encryption required",
			mutateCfg: func(cfg *models.SRTConfig) {
				cfg.Passphrase = "testpassphrase123"
			},
			request: func(t *testing.T) *fakeConnRequest {
				return &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:5004"), version: 5, streamID: "testkey"}
			},
			wireCode: gosrt.REJ_UNSECURE,
		},
		{
			name: "bad passphrase",
			mutateCfg: func(cfg *models.SRTConfig) {
				cfg.Passphrase = "testpassphrase123"
			},
			request: func(t *testing.T) *fakeConnRequest {
				return &fakeConnRequest{
					remote:           testRemoteAddr(t, "127.0.0.1:5005"),
					version:          5,
					streamID:         "testkey",
					encrypted:        true,
					setPassphraseErr: errors.New("bad secret"),
				}
			},
			wireCode: gosrt.REJ_BADSECRET,
		},
		{
			name: "cidr denied",
			mutateCfg: func(cfg *models.SRTConfig) {
				cfg.DeniedCIDRs = []string{"127.0.0.0/8"}
			},
			request: func(t *testing.T) *fakeConnRequest {
				return &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:5006"), version: 5, streamID: "testkey"}
			},
			wireCode: gosrt.REJ_PEER,
		},
		{
			name: "unknown stream key",
			request: func(t *testing.T) *fakeConnRequest {
				return &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:5007"), version: 5, streamID: "wrongkey"}
			},
			wireCode: gosrt.REJ_PEER,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testAuthConfig()
			if test.mutateCfg != nil {
				test.mutateCfg(&cfg)
			}
			svc := newAuthTestService(t, &cfg)
			request := test.request(t)

			sess, ok := svc.authenticate(request)
			if ok {
				sess.Close(ErrRemoteClosed)
				t.Fatal("expected the request to be rejected")
			}
			if !request.rejected {
				t.Fatal("ConnRequest.Reject was not called")
			}
			if request.rejection != test.wireCode {
				t.Errorf("rejection code = %d, want %d", request.rejection, test.wireCode)
			}
			if request.accepted {
				t.Error("ConnRequest.Accept must not be called after a rejection")
			}
		})
	}
}

func TestAuthenticatePublisherConflict(t *testing.T) {
	cfg := testAuthConfig()
	svc := newAuthTestService(t, &cfg)

	dummy := newSession(testRemoteAddr(t, "127.0.0.1:6000"), "key:other", 5, false, nil)
	if err := svc.deps.Sessions.TryAcquire(dummy); err != nil {
		t.Fatalf("pre-acquire arbiter: %v", err)
	}
	defer func() {
		svc.deps.Sessions.Release(dummy)
		dummy.Close(nil)
	}()

	request := &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:6001"), version: 5, streamID: "testkey"}
	sess, ok := svc.authenticate(request)
	if ok {
		sess.Close(ErrRemoteClosed)
		t.Fatal("expected the second publisher to be rejected")
	}
	if request.rejection != gosrt.REJ_RESOURCE {
		t.Errorf("rejection code = %d, want %d", request.rejection, gosrt.REJ_RESOURCE)
	}
}

func TestAuthenticateAcceptErrorReleasesArbiter(t *testing.T) {
	cfg := testAuthConfig()
	svc := newAuthTestService(t, &cfg)

	request := &fakeConnRequest{
		remote:    testRemoteAddr(t, "127.0.0.1:7000"),
		version:   5,
		streamID:  "testkey",
		acceptErr: errors.New("handshake failed"),
	}
	if _, ok := svc.authenticate(request); ok {
		t.Fatal("expected the accept failure to fail the request")
	}
	if current := svc.deps.Sessions.Current(); current != nil {
		t.Error("arbiter slot must be released when Accept fails")
	}
}

func TestAuthenticateRateLimited(t *testing.T) {
	cfg := testAuthConfig()
	cfg.AuthFailureRateLimit = 2
	svc := newAuthTestService(t, &cfg)

	ip := netip.MustParseAddr("127.0.0.1")
	for i := 0; i < 2; i++ {
		request := &fakeConnRequest{
			remote:   testRemoteAddr(t, "127.0.0.1:7100"),
			version:  5,
			streamID: "wrongkey",
		}
		if _, ok := svc.authenticate(request); ok {
			t.Fatal("expected bad key rejection")
		}
	}

	if svc.limits.Allow(ip) {
		t.Fatal("expected the IP to be rate limited after the configured failures")
	}

	request := &fakeConnRequest{remote: testRemoteAddr(t, "127.0.0.1:7101"), version: 5, streamID: "testkey"}
	sess, ok := svc.authenticate(request)
	if ok {
		sess.Close(ErrRemoteClosed)
		t.Fatal("expected the rate-limited request to be rejected even with a valid key")
	}
}

// compile-time checks for the test fakes.
var (
	_ gosrt.ConnRequest = (*fakeConnRequest)(nil)
	_ gosrt.Conn        = (*fakeConn)(nil)
	_                   = ingest.ProtocolSRT
)
