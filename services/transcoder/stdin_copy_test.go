package transcoder

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// newStartTestTranscoder builds a transcoder that writes nothing outside its temp
// directory and runs in RTP-only mode, so Start can be exercised without HLS.
func newStartTestTranscoder(t *testing.T) *Transcoder {
	t.Helper()

	tr := new(Transcoder)
	tr.cfg = &config.Config{LogDirectory: t.TempDir()}
	tr.ffmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
	tr.input = "pipe:0"
	tr.inputFormat = ingest.FLVInputFormat()
	tr.codec = &Libx264Codec{}
	tr.currentLatencyLevel = models.GetLatencyLevel(2)
	tr.SetRTPOnly(true)

	return tr
}

// A child that exits while the ingest side still holds the pipe open must not keep
// exec.Cmd.Wait blocked. Handing the reader to Cmd.Stdin did exactly that: Wait
// also waits for the copy os/exec starts, and that copy sits in Read until more
// bytes arrive, so the completion callback that tears the stream down never ran.
func TestStdinCopyDoesNotBlockWaitWhenTheChildExitsFirst(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })

	cmd := exec.Command("sh", "-c", "exit 0") //nolint:gosec // fixed command
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := startStdinCopy(stdin, reader)

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("cmd.Wait blocked on a parked stdin copy")
	}

	released := make(chan struct{})
	go func() {
		releaseStdinCopy(done, reader)
		close(released)
	}()

	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("releaseStdinCopy did not unblock the copy")
	}
}

// A normal end of input drains the pipe and finishes the copy on its own, which is
// the behaviour the release path must not disturb.
func TestStdinCopyDrainsOnNormalEOF(t *testing.T) {
	reader, writer := io.Pipe()

	cmd := exec.Command("sh", "-c", "cat > /dev/null") //nolint:gosec // fixed command
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := startStdinCopy(stdin, reader)

	go func() {
		_, _ = writer.Write([]byte("ingest bytes"))
		_ = writer.Close()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the copy did not finish after a normal end of input")
	}

	_ = cmd.Wait()
	releaseStdinCopy(done, reader)
}

// A failed start must still report completion, otherwise the stream lifecycle and
// the WebRTC supervisor wait forever for a process that never existed. It must also
// not leave a copy goroutine draining a pipe nobody will read.
func TestStartFailureReportsCompletionAndLeavesNoStdinCopy(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })

	tr := newStartTestTranscoder(t)
	tr.SetStdin(reader)

	completed := make(chan error, 1)
	tr.TranscoderCompleted = func(err error) { completed <- err }

	tr.Start(false)

	select {
	case err := <-completed:
		if err == nil {
			t.Fatal("a failed start must report an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("TranscoderCompleted did not run after a failed start")
	}

	// With the copy started before the process existed this write would be consumed
	// by a goroutine parked in Read on the ingest branch.
	written := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("x"))
		written <- err
	}()

	select {
	case err := <-written:
		t.Fatalf("a stdin copy is still draining the pipe after a failed start: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

// A Stop that arrives between registration and the actual exec must not leave an
// unowned encoder running for the rest of the stream.
func TestStopBeforeStartStopsTheProcessOnceItStarts(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ffmpeg")
	body := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}

	tr := newStartTestTranscoder(t)
	tr.ffmpegPath = script

	completed := make(chan error, 1)
	tr.TranscoderCompleted = func(err error) { completed <- err }

	// The stop lands while no process exists yet.
	tr.Stop()

	go tr.Start(false)

	started := time.Now()

	select {
	case err := <-completed:
		// "signal: killed" can only come from a child that started and was then
		// signalled, so the stop reached a live process rather than failing to start.
		if err == nil || !strings.Contains(err.Error(), "killed") {
			t.Fatalf("the child was not killed after a pre-start Stop: %v", err)
		}
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			t.Fatalf("the child ran for %s before being stopped", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a Stop issued before Start did not stop the process that started afterwards")
	}
}

// Stop racing Start must never read a half-initialised cmd.Process and must still
// terminate the child. Run under -race.
func TestStopDuringStartStopsTheProcess(t *testing.T) {
	for i := 0; i < 20; i++ {
		tr := newStartTestTranscoder(t)
		tr.ffmpegPath = writeSleepingFakeFFmpeg(t)

		completed := make(chan error, 1)
		tr.TranscoderCompleted = func(err error) { completed <- err }

		go tr.Start(false)
		tr.Stop()

		select {
		case err := <-completed:
			// "signal: killed" proves the child started and was signalled; a start
			// failure would report a different error and would not exercise the race.
			if err == nil || !strings.Contains(err.Error(), "killed") {
				t.Fatalf("iteration %d: the child was not stopped after a racing Stop: %v", i, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("iteration %d: a Stop racing Start did not stop the process", i)
		}
	}
}

// A Stop after the child is running must stop it through the published reference.
func TestStopAfterStartStopsTheProcess(t *testing.T) {
	tr := newStartTestTranscoder(t)
	tr.ffmpegPath = writeSleepingFakeFFmpeg(t)

	completed := make(chan error, 1)
	tr.TranscoderCompleted = func(err error) { completed <- err }

	go tr.Start(false)

	// Wait until the process is published and running before stopping it.
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
	case err := <-completed:
		if err == nil || !strings.Contains(err.Error(), "killed") {
			t.Fatalf("the running child was not stopped: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a Stop after Start did not stop the running process")
	}
}

// writeSleepingFakeFFmpeg writes an executable that ignores its arguments and
// sleeps, so a launched child stays alive until it is stopped.
func writeSleepingFakeFFmpeg(t *testing.T) string {
	t.Helper()

	script := filepath.Join(t.TempDir(), "fake-ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}

	return script
}
