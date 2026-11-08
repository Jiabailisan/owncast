package ingest

import (
	"errors"
	"io"
	"net"
	"testing"
)

type fakeSession struct {
	*SessionBase
}

func newFakeSession(id string) *fakeSession {
	return &fakeSession{SessionBase: NewSessionBase(id, ProtocolRTMP, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}, "key", FLVInputFormat(), nil)}
}

func (f *fakeSession) Reader() io.Reader { return nil }

func TestArbiterSinglePublisher(t *testing.T) {
	a := NewArbiter()
	first := newFakeSession("first")

	if err := a.TryAcquire(first); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	second := newFakeSession("second")
	if err := a.TryAcquire(second); !errors.Is(err, ErrPublisherActive) {
		t.Fatalf("expected ErrPublisherActive, got %v", err)
	}
	if a.Current() == nil || a.Current().ID() != "first" {
		t.Fatalf("unexpected current session: %v", a.Current())
	}

	a.Release(second) // non-owner release is a no-op
	if a.Current() == nil {
		t.Fatal("non-owner release cleared the slot")
	}

	a.Release(first)
	if a.Current() != nil {
		t.Fatal("owner release did not clear the slot")
	}

	if err := a.TryAcquire(second); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestArbiterDisconnectCurrent(t *testing.T) {
	a := NewArbiter()
	s := newFakeSession("only")
	if err := a.TryAcquire(s); err != nil {
		t.Fatal(err)
	}

	if !a.DisconnectCurrent(errors.New("admin")) {
		t.Fatal("expected DisconnectCurrent to act on the active session")
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("session was not closed")
	}
	if a.Current() != nil {
		t.Fatal("arbiter slot not cleared")
	}
	if a.DisconnectCurrent(nil) {
		t.Fatal("disconnect with no session must report false")
	}
}

func TestArbiterAcquiresAfterClosedSession(t *testing.T) {
	a := NewArbiter()
	stale := newFakeSession("stale")
	if err := a.TryAcquire(stale); err != nil {
		t.Fatal(err)
	}
	stale.Close(nil)

	fresh := newFakeSession("fresh")
	if err := a.TryAcquire(fresh); err != nil {
		t.Fatalf("a closed session must not block a new publisher: %v", err)
	}
}
