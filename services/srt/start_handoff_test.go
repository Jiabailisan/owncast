package srt

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/services/ingest"
)

// newStartTestService builds the minimum service startSession needs, with a handler
// the test can observe.
func newStartTestService(t *testing.T, handler func(ingest.Session) error) *Service {
	t.Helper()

	svc := &Service{
		deps:   Deps{Sessions: ingest.NewArbiter(), Handler: handler},
		logger: log.New(),
		ctx:    context.Background(),
		wg:     &sync.WaitGroup{},
	}

	return svc
}

// samplerStarted reports whether the statistics sampler is registered.
func samplerStarted(sess *session) bool {
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()

	return sess.sampler != nil
}

// A session that was closed before startSession reaches it must register nothing: no
// sampler, no active snapshot and no handler call.
func TestStartAfterCloseRegistersNothing(t *testing.T) {
	var calls atomic.Int32
	svc := newStartTestService(t, func(ingest.Session) error {
		calls.Add(1)

		return nil
	})

	var sess *session
	sess = newSession(nil, "key", 0, false, func(reason error) { svc.sessionClosed(sess, reason) })
	if !sess.bind(&fakeConn{}) {
		t.Fatal("bind must accept a live session")
	}
	// Close completes before the start path runs.
	sess.Close(ErrSessionClosed)

	svc.startSession(sess)

	if got := calls.Load(); got != 0 {
		t.Fatalf("the handler ran %d times for a closed session, want 0", got)
	}
	if samplerStarted(sess) {
		t.Fatal("a closed session must not start a sampler")
	}
	svc.activeMu.Lock()
	active := svc.active
	svc.activeMu.Unlock()
	if active != nil {
		t.Fatal("a closed session must not be published as active")
	}
}

// A Close that lands while the start path is committing must cancel what the start
// registered, and the session must not be published as the current stream.
func TestCloseDuringStartCancelsWhatWasRegistered(t *testing.T) {
	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32

	svc := newStartTestService(t, func(ingest.Session) error {
		calls.Add(1)
		close(handlerStarted)
		<-release

		return nil
	})

	conn := &fakeConn{}
	var sess *session
	sess = newSession(nil, "key", 0, false, func(reason error) { svc.sessionClosed(sess, reason) })
	if !sess.bind(conn) {
		t.Fatal("bind must accept a live session")
	}

	done := make(chan struct{})
	go func() {
		svc.startSession(sess)
		close(done)
	}()

	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never started")
	}

	// Close while the handler is running: the cleanup must be visible to it.
	sess.Close(ErrRemoteClosed)
	close(release)
	<-done

	if got := calls.Load(); got != 1 {
		t.Fatalf("the handler ran %d times, want 1", got)
	}
	if got := conn.closeCount(); got != 1 {
		t.Fatalf("the connection was closed %d times, want 1", got)
	}
	if samplerStarted(sess) {
		t.Fatal("Close must stop the sampler the start path registered")
	}
	svc.activeMu.Lock()
	active := svc.active
	svc.activeMu.Unlock()
	if active != nil {
		t.Fatal("the cancelled session must not stay active")
	}
}

// A normal start reaches the handler exactly once and publishes the session.
func TestNormalStartInvokesTheHandlerOnce(t *testing.T) {
	var calls atomic.Int32
	svc := newStartTestService(t, func(ingest.Session) error {
		calls.Add(1)

		return nil
	})

	var sess *session
	sess = newSession(nil, "key", 0, false, func(reason error) { svc.sessionClosed(sess, reason) })
	if !sess.bind(&fakeConn{}) {
		t.Fatal("bind must accept a live session")
	}

	svc.startSession(sess)
	t.Cleanup(func() { sess.Close(ErrRemoteClosed) })

	if got := calls.Load(); got != 1 {
		t.Fatalf("the handler ran %d times, want 1", got)
	}
	svc.activeMu.Lock()
	active := svc.active
	svc.activeMu.Unlock()
	if active != sess {
		t.Fatal("a normal start must publish the session as active")
	}
}

// Cleanup from a finished session must not clear the active snapshot or the arbiter
// slot a newer session owns.
func TestStaleCleanupKeepsTheNewSession(t *testing.T) {
	svc := newStartTestService(t, func(ingest.Session) error { return nil })

	old := newSession(nil, "old", 0, false, func(error) {})
	fresh := newSession(nil, "new", 0, false, func(error) {})

	if err := svc.deps.Sessions.TryAcquire(old); err != nil {
		t.Fatalf("acquire old: %v", err)
	}
	old.Close(ErrRemoteClosed)

	if err := svc.deps.Sessions.TryAcquire(fresh); err != nil {
		t.Fatalf("acquire new: %v", err)
	}
	svc.activeMu.Lock()
	svc.active = fresh
	svc.activeMu.Unlock()

	// A late cleanup for the finished session.
	svc.sessionClosed(old, ErrRemoteClosed)

	svc.activeMu.Lock()
	active := svc.active
	svc.activeMu.Unlock()
	if active != fresh {
		t.Fatal("the stale cleanup cleared the new session's active snapshot")
	}
	if current := svc.deps.Sessions.Current(); current == nil || current.ID() != fresh.ID() {
		t.Fatal("the stale cleanup freed the new session's arbiter slot")
	}
}
