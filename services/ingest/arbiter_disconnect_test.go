package ingest

import (
	"errors"
	"testing"
)

// DisconnectIf acts only on the session that owns the slot, so a late offline cannot
// close the publisher that replaced it.
func TestDisconnectIfKeepsTheNewOwner(t *testing.T) {
	arbiter := NewArbiter()

	oldSession := &fakeSession{SessionBase: NewSessionBase("old", ProtocolRTMP, nil, "key", FLVInputFormat(), nil)}
	freshSession := &fakeSession{SessionBase: NewSessionBase("new", ProtocolRTMP, nil, "key", FLVInputFormat(), nil)}

	if err := arbiter.TryAcquire(oldSession); err != nil {
		t.Fatalf("acquire old: %v", err)
	}
	arbiter.Release(oldSession)
	if err := arbiter.TryAcquire(freshSession); err != nil {
		t.Fatalf("acquire new: %v", err)
	}

	if arbiter.DisconnectIf(oldSession, errors.New("stale offline")) {
		t.Fatal("DisconnectIf acted on a session that no longer owns the slot")
	}
	if current := arbiter.Current(); current == nil || current.ID() != freshSession.ID() {
		t.Fatal("the new owner was disconnected")
	}

	if !arbiter.DisconnectIf(freshSession, errors.New("offline")) {
		t.Fatal("DisconnectIf must act on the current owner")
	}
	if current := arbiter.Current(); current != nil {
		t.Fatal("the slot was not freed for the owner")
	}
}
