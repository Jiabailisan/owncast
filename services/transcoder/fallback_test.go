package transcoder

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// When the exit cannot be observed the reference is kept and the raw PGID signal is
// disabled, so Stop still terminates the child through the process handle. This is
// the fallback path that also covers platforms without waitid.
func TestStopStillWorksWhenObservationFails(t *testing.T) {
	original := observeExitHook
	observeExitHook = func(int) error { return errors.New("observation unavailable") }
	t.Cleanup(func() { observeExitHook = original })

	tr := newStartTestTranscoder(t)
	tr.ffmpegPath = writeSleepingFakeFFmpeg(t)

	var calls atomic.Int32
	completed := make(chan error, 1)
	tr.TranscoderCompleted = func(err error) {
		calls.Add(1)
		completed <- err
	}

	go tr.Start(false)

	// The fallback is entered once the reference is published and marked unsafe.
	deadline := time.Now().Add(10 * time.Second)
	for {
		tr.commandExecMu.Lock()
		ready := tr.commandExec != nil && tr.pgidUnsafe
		tr.commandExecMu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fallback path was never entered")
		}
		time.Sleep(5 * time.Millisecond)
	}

	tr.Stop()

	select {
	case <-completed:
	case <-time.After(15 * time.Second):
		t.Fatal("Stop did not terminate the child on the fallback path")
	}

	// Wait retires the reference, so a repeated Stop sends nothing.
	tr.commandExecMu.Lock()
	ref, reaped := tr.commandExec, tr.processReaped
	tr.commandExecMu.Unlock()
	if ref != nil || !reaped {
		t.Fatalf("the reference was not retired after the reap (ref=%v reaped=%v)", ref, reaped)
	}

	tr.Stop()

	if got := calls.Load(); got != 1 {
		t.Fatalf("the completion callback ran %d times, want 1", got)
	}
}
