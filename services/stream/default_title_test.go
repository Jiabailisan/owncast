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

// A stream that is live but carries no publisher title shows the default, so changing that
// default is a real change viewers see and must be announced. The same session has to span the
// whole save: the check is about a stable stream, not about whether one exists.
func TestSaveDefaultTitleWithoutLiveTitleAnnouncesChange(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	if err := repo.SetStreamTitle("B"); err != nil {
		t.Fatalf("seed default: %v", err)
	}

	svc := &Service{configRepository: repo}
	session := newTestIngestSession("live")
	if !svc.setCurrentSession(session) {
		t.Fatal("the test session must become the current session")
	}

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

// A publish that starts while the default is being written must not turn into an announcement.
// The save compares titles on both sides, and across two sessions that comparison is meaningless,
// so the save stays silent. The save callback is the deterministic trigger: no sleeps involved.
func TestSaveDefaultTitleStaysSilentWhenAPublishStartsDuringTheSave(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	if err := repo.SetStreamTitle("B"); err != nil {
		t.Fatalf("seed default: %v", err)
	}

	svc := &Service{configRepository: repo}

	changed, err := svc.SaveDefaultTitle(func(value string) error {
		if err := repo.SetStreamTitle(value); err != nil {
			return err
		}

		// A publisher takes the stream between the two reads.
		session := newTestIngestSession("late")
		session.SetStreamTitle("A")
		if !svc.setCurrentSession(session) {
			t.Fatal("the late session must become the current session")
		}

		return nil
	}, "C")
	if err != nil {
		t.Fatalf("save default: %v", err)
	}
	if changed {
		t.Fatal("a publish that started during the save must not be announced as a title change")
	}

	if got := repo.GetStreamTitle(); got != "C" {
		t.Fatalf("saved default is %q, want C: the save must succeed regardless", got)
	}
	if got := svc.currentStreamTitle(); got != "A" {
		t.Fatalf("viewers see %q, want the new session's title A", got)
	}
}

// The same applies when the current stream ends during the save: the two reads describe different
// streams, so nothing is announced even though the default really did change.
func TestSaveDefaultTitleStaysSilentWhenTheStreamEndsDuringTheSave(t *testing.T) {
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

	changed, err := svc.SaveDefaultTitle(func(value string) error {
		if err := repo.SetStreamTitle(value); err != nil {
			return err
		}

		// The publisher disconnects between the two reads.
		svc.setCurrentSession(nil)

		return nil
	}, "C")
	if err != nil {
		t.Fatalf("save default: %v", err)
	}
	if changed {
		t.Fatal("a stream that ended during the save must not be announced as a title change")
	}

	if got := repo.GetStreamTitle(); got != "C" {
		t.Fatalf("saved default is %q, want C", got)
	}
	if got := svc.currentStreamTitle(); got != "C" {
		t.Fatalf("viewers see %q, want the new default C", got)
	}
}
