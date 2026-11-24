package stream

import (
	"bytes"
	"testing"
	"time"
)

// pacingReader delivers one fixed chunk per tick, the way a live SRT source
// does, so the read-ahead has to buffer across several reads.
type pacingReader struct {
	delay time.Duration
}

func (r *pacingReader) Read(p []byte) (int, error) {
	time.Sleep(r.delay)

	return copy(p, bytes.Repeat([]byte{0x47, 0x1F, 0xFF, 0x10}, 4096)), nil
}

// TestPrefetchReaderStopsWhenTold proves the verdict-stop: ending the read-ahead
// when the decision landed must close the budget immediately, well before the
// deadline, so no dead bytes are buffered between the verdict and the FFmpeg
// hand-off.
func TestPrefetchReaderStopsWhenTold(t *testing.T) {
	reader := newPrefetchReader(&pacingReader{delay: 10 * time.Millisecond})
	deadline := time.Now().Add(2 * time.Second)
	reader.Prefetch(deadline, 1<<30) // byte ceiling unreachable

	time.Sleep(100 * time.Millisecond)

	stopStart := time.Now()
	reader.Stop("verdict")
	select {
	case <-reader.BudgetSpent():
	case <-time.After(time.Second):
		t.Fatal("Stop did not close the budget")
	}

	_, elapsed, reason := reader.Diagnostics()
	if reason != "verdict" {
		t.Fatalf("end reason = %q, want verdict", reason)
	}
	if elapsed > time.Since(stopStart)+200*time.Millisecond {
		t.Fatalf("read-ahead ran %s, want it to end at the Stop", elapsed)
	}
}

// TestPrefetchReaderDiagnosticsRecordsReasons pins the startup report: the
// recorded reason is the first one that applied.
func TestPrefetchReaderDiagnosticsRecordsReasons(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		reader := newPrefetchReader(&pacingReader{delay: 5 * time.Millisecond})
		reader.Prefetch(time.Now().Add(120*time.Millisecond), 1<<30)
		select {
		case <-reader.BudgetSpent():
		case <-time.After(2 * time.Second):
			t.Fatal("the read-ahead never ended at its deadline")
		}
		_, _, reason := reader.Diagnostics()
		if reason != "deadline" {
			t.Fatalf("reason = %q, want deadline", reason)
		}
	})

	t.Run("memory ceiling", func(t *testing.T) {
		reader := newPrefetchReader(&endlessReader{})
		reader.Prefetch(time.Now().Add(time.Minute), 256*1024)
		select {
		case <-reader.BudgetSpent():
		case <-time.After(2 * time.Second):
			t.Fatal("the read-ahead never ended at its byte ceiling")
		}
		bytes, _, reason := reader.Diagnostics()
		if reason != "memory-ceiling" {
			t.Fatalf("reason = %q, want memory-ceiling", reason)
		}
		if bytes < 256*1024 {
			t.Fatalf("read %d bytes, want at least the ceiling", bytes)
		}
	})
}
