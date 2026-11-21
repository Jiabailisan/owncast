package transcoder

import (
	"testing"
	"time"
)

// The signalable reference must be withdrawn before the completion callback runs, so
// a Stop triggered from that callback cannot signal a pid the kernel is about to
// reuse. This goes through the real Start path.
func TestStartWithdrawsTheReferenceBeforeReaping(t *testing.T) {
	tr := newStartTestTranscoder(t)
	tr.ffmpegPath = writeExitFakeFFmpeg(t)

	var withdrawn bool
	tr.TranscoderCompleted = func(error) {
		tr.commandExecMu.Lock()
		withdrawn = tr.commandExec == nil && tr.processReaped
		tr.commandExecMu.Unlock()
	}

	tr.Start(false)

	if !withdrawn {
		t.Fatal("the signalable reference must be withdrawn before the completion callback")
	}
}

// A Stop that arrives while the exit is being observed still stops a running child.
func TestStopDuringExitObservation(t *testing.T) {
	tr := newStartTestTranscoder(t)
	tr.ffmpegPath = writeSleepingFakeFFmpeg(t)

	completed := make(chan error, 1)
	tr.TranscoderCompleted = func(err error) { completed <- err }

	go tr.Start(false)

	deadline := time.Now().Add(10 * time.Second)
	for {
		tr.commandExecMu.Lock()
		published := tr.commandExec != nil
		tr.commandExecMu.Unlock()
		if published {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the process was never published")
		}
		time.Sleep(5 * time.Millisecond)
	}

	tr.Stop()

	select {
	case <-completed:
	case <-time.After(15 * time.Second):
		t.Fatal("a Stop during exit observation did not stop the child")
	}
}
