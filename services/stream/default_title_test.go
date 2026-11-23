package stream

import "testing"

// A publisher that set a title in its stream key keeps it. Saving a new default still succeeds
// and is what the admin field shows, but viewers see no change, so nothing is announced, and
// the newly saved default is what viewers get once the stream ends.
func TestSaveDefaultTitleWithLiveTitleKeepsTheEffectiveTitle(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	if err := repo.SetStreamTitle("B"); err != nil {
		t.Fatalf("seed default: %v", err)
	}

	svc := &Service{configRepository: repo}
	session := newTestIngestSession("live")
	session.SetStreamTitle("A")
	if !svc.setCurrentSession(session) {
		t.Fatal("the test session must become the current session")
	}

	changed, err := svc.SaveDefaultTitle(repo.SetStreamTitle, "C")
	if err != nil {
		t.Fatalf("save default: %v", err)
	}
	if changed {
		t.Fatal("saving the default must not announce a change while a stream title overrides it")
	}

	if got := svc.currentStreamTitle(); got != "A" {
		t.Fatalf("viewers see %q, want the stream title A", got)
	}
	if got := repo.GetStreamTitle(); got != "C" {
		t.Fatalf("saved default is %q, want C", got)
	}

	// The stream ends: the default that was just saved takes over.
	svc.setCurrentSession(nil)
	if got := svc.currentStreamTitle(); got != "C" {
		t.Fatalf("after the stream ended viewers see %q, want the new default C", got)
	}
}

// Without a stream title the default is exactly what viewers see, so a real change is
// announced.
func TestSaveDefaultTitleWithoutLiveTitleAnnouncesChange(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	if err := repo.SetStreamTitle("B"); err != nil {
		t.Fatalf("seed default: %v", err)
	}

	svc := &Service{configRepository: repo}

	changed, err := svc.SaveDefaultTitle(repo.SetStreamTitle, "C")
	if err != nil {
		t.Fatalf("save default: %v", err)
	}
	if !changed {
		t.Fatal("changing the default with no stream title must announce the change")
	}

	if got := svc.currentStreamTitle(); got != "C" {
		t.Fatalf("viewers see %q, want C", got)
	}
	if got := repo.GetStreamTitle(); got != "C" {
		t.Fatalf("saved default is %q, want C", got)
	}
}

// Saving the same value again changes nothing for viewers and must stay silent.
func TestSaveDefaultTitleWithUnchangedValueIsSilent(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	if err := repo.SetStreamTitle("B"); err != nil {
		t.Fatalf("seed default: %v", err)
	}

	svc := &Service{configRepository: repo}

	changed, err := svc.SaveDefaultTitle(repo.SetStreamTitle, "B")
	if err != nil {
		t.Fatalf("save default: %v", err)
	}
	if changed {
		t.Fatal("re-saving the same default must not announce anything")
	}
}
