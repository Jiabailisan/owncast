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

// A failed process must be replaceable by the same stream: clearing the process
// reference keeps the session ownership so the supervisor can register its
// replacement after the backoff.
func TestWebRTCProcessRetriesWithinTheSameSession(t *testing.T) {
	svc := &Service{}
	session := newTestIngestSession("stream")

	svc.claimWebRTCProcessSession(session)
	first := &transcoder.Transcoder{}
	if !svc.registerWebRTCTranscoder(session, first) {
		t.Fatal("the owning session must be able to register its first process")
	}

	// The first process failed; the supervisor clears it and backs off.
	svc.clearWebRTCTranscoder(first)

	if got := svc.getWebRTCTranscoder(); got != nil {
		t.Fatalf("the failed process reference was not cleared: %p", got)
	}

	replacement := &transcoder.Transcoder{}
	if !svc.registerWebRTCTranscoder(session, replacement) {
		t.Fatal("the same session must be able to register its replacement process")
	}
	if got := svc.getWebRTCTranscoder(); got != replacement {
		t.Fatal("the replacement process was not recorded")
	}
}

// A supervisor from the previous stream must not release the ownership a newer
// stream already claimed, even when it exits after the new stream took over but
// before that stream registered its own process.
func TestStaleSupervisorReleaseKeepsTheNewSessionOwnership(t *testing.T) {
	svc := &Service{}
	oldSession := newTestIngestSession("old")
	newSession := newTestIngestSession("new")

	svc.claimWebRTCProcessSession(oldSession)
	if !svc.registerWebRTCTranscoder(oldSession, &transcoder.Transcoder{}) {
		t.Fatal("the first stream must register its process")
	}

	// The next stream claims ownership; it has not registered a process yet.
	svc.claimWebRTCProcessSession(newSession)

	// The old supervisor now exits for good.
	svc.releaseWebRTCProcessSession(oldSession)

	if !svc.registerWebRTCTranscoder(newSession, &transcoder.Transcoder{}) {
		t.Fatal("a stale supervisor released the new stream's ownership")
	}
}
