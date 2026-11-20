package transcoder

import (
	"io"
	"os/exec"
	"testing"
	"time"
)

// A child that exits while the ingest side still holds the pipe open must not keep
// exec.Cmd.Wait blocked. Handing the reader to Cmd.Stdin did exactly that: Wait
// also waits for the copy os/exec starts, and that copy sits in Read until more
// bytes arrive, so the completion callback that tears the stream down never ran.
func TestStdinCopyDoesNotBlockWaitWhenTheChildExitsFirst(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })

	cmd := exec.Command("sh", "-c", "exit 0") //nolint:gosec // fixed command
	done, err := startStdinCopy(cmd, reader)
	if err != nil {
		t.Fatalf("startStdinCopy: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

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
	done, err := startStdinCopy(cmd, reader)
	if err != nil {
		t.Fatalf("startStdinCopy: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

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
