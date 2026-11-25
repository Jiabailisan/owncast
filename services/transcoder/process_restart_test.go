package transcoder

import "testing"

func TestTranscoderSetRestartCountReflectedInSnapshot(t *testing.T) {
	tr := &Transcoder{segmentIdentifier: "webrtc-test"}
	tr.SetPurpose([]string{"webrtc"})
	tr.SetRestartCount(2)

	snap := tr.Snapshot()
	if snap.RestartCount != 2 {
		t.Fatalf("RestartCount = %d, want 2", snap.RestartCount)
	}
}
