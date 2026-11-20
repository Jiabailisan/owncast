package stream

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/transcoder"
)

// testSession is a SessionBase with the media reader the protocol adapters supply
// in production; SessionBase alone does not implement ingest.Session.
type testSession struct {
	*ingest.SessionBase
}

func (s testSession) Reader() io.Reader { return nil }

func newTestIngestSession(id string) testSession {
	return testSession{SessionBase: ingest.NewSessionBase(id, ingest.ProtocolRTMP, nil, "key", ingest.FLVInputFormat(), nil)}
}

// A supervisor belonging to a session that already ended must not clear the
// process reference a newer session installed, otherwise the replacement process
// of the next stream is left unreferenced and cannot be stopped.
func TestClearWebRTCTranscoderRespectsOwnership(t *testing.T) {
	svc := &Service{}
	owner := newTestIngestSession("owner")
	stale := &transcoder.Transcoder{}
	current := &transcoder.Transcoder{}

	svc.claimWebRTCProcessSession(owner)
	if !svc.registerWebRTCTranscoder(owner, current) {
		t.Fatal("the owning session must be able to register its process")
	}

	svc.clearWebRTCTranscoder(stale)

	if got := svc.getWebRTCTranscoder(); got != current {
		t.Fatal("a stale supervisor cleared the current process reference")
	}

	svc.clearWebRTCTranscoder(current)

	if got := svc.getWebRTCTranscoder(); got != nil {
		t.Fatal("the owning supervisor did not clear its own reference")
	}
}

// Ownership is checked and the reference is written under one lock, so a
// supervisor whose session was replaced cannot install its process afterwards.
func TestRegisterWebRTCTranscoderRejectsAStaleSession(t *testing.T) {
	svc := &Service{}
	oldSession := newTestIngestSession("old")
	newSession := newTestIngestSession("new")

	svc.claimWebRTCProcessSession(oldSession)
	oldProcess := &transcoder.Transcoder{}
	if !svc.registerWebRTCTranscoder(oldSession, oldProcess) {
		t.Fatal("the owning session must be able to register its process")
	}

	// A new stream takes over while the old supervisor is still in its backoff.
	svc.claimWebRTCProcessSession(newSession)

	if svc.registerWebRTCTranscoder(oldSession, oldProcess) {
		t.Fatal("a supervisor from the previous session registered its process")
	}
	if got := svc.getWebRTCTranscoder(); got != nil {
		t.Fatalf("a stale supervisor installed a process reference: %p", got)
	}

	newProcess := &transcoder.Transcoder{}
	if !svc.registerWebRTCTranscoder(newSession, newProcess) {
		t.Fatal("the current session must be able to register its process")
	}
	if got := svc.getWebRTCTranscoder(); got != newProcess {
		t.Fatal("the current session's process reference was not recorded")
	}
}

// A stop request that lands between registration and the actual exec must keep the
// supervisor from installing a process for a session that is already gone.
func TestRegisterWebRTCTranscoderRejectsACancelledSession(t *testing.T) {
	svc := &Service{}
	session := newTestIngestSession("cancelled")

	svc.claimWebRTCProcessSession(session)
	session.Close(errors.New("publisher disconnected"))

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not report completion")
	}

	if svc.registerWebRTCTranscoder(session, &transcoder.Transcoder{}) {
		t.Fatal("a cancelled session registered a process")
	}
	if got := svc.getWebRTCTranscoder(); got != nil {
		t.Fatal("a cancelled session installed a process reference")
	}
}
