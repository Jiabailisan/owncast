package ingest

import (
	"errors"
	"sync"
)

// ErrPublisherActive is returned by Arbiter.TryAcquire when a publisher is
// already connected, regardless of protocol. This preserves the previous
// single-publisher behavior while making it atomic and cross-protocol.
var ErrPublisherActive = errors.New("a publisher is already connected")

// Arbiter guarantees at most one active publisher across every ingest
// protocol. It replaces the unsynchronized RTMP-local boolean.
type Arbiter struct {
	mu      sync.Mutex
	current Session
}

// NewArbiter returns an empty arbiter.
func NewArbiter() *Arbiter {
	return &Arbiter{}
}

// TryAcquire reserves the single publisher slot for s. It returns
// ErrPublisherActive when another session owns the slot.
func (a *Arbiter) TryAcquire(s Session) error {
	if s == nil {
		return errors.New("nil session")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.current != nil && !sessionClosed(a.current) {
		return ErrPublisherActive
	}
	a.current = s
	return nil
}

// Release frees the slot if s owns it.
func (a *Arbiter) Release(s Session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.current != nil && a.current.ID() == s.ID() {
		a.current = nil
	}
}

// Current returns the active session, or nil.
func (a *Arbiter) Current() Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

// DisconnectCurrent closes the active session, if any. It is used by admins
// to force-disconnect the publisher and by the offline lifecycle.
func (a *Arbiter) DisconnectCurrent(reason error) bool {
	a.mu.Lock()
	s := a.current
	a.current = nil
	a.mu.Unlock()

	if s == nil {
		return false
	}
	s.Close(reason)
	return true
}

func sessionClosed(s Session) bool {
	select {
	case <-s.Done():
		return true
	default:
		return false
	}
}
