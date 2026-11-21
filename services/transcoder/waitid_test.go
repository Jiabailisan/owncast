package transcoder

import (
	"os/exec"
	"testing"
)

// Observing the exit with WNOWAIT must leave the child waitable, so the single
// cmd.Wait still reaps it and reports the status.
func TestObserveExitLeavesTheChildWaitable(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0") //nolint:gosec // fixed command
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := observeExit(cmd.Process.Pid); err != nil {
		t.Fatalf("observe exit: %v", err)
	}

	// WNOWAIT does not reap: Wait must still see the exit.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait after observation: %v", err)
	}
}

// Observing a child that is already gone reports an error instead of blocking.
func TestObserveExitOnAReapedChildFails(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0") //nolint:gosec // fixed command
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = cmd.Wait()

	if err := observeExit(cmd.Process.Pid); err == nil {
		t.Fatal("observing a reaped child must fail rather than block")
	}
}
