package admin

import (
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

func TestFromEntryIncludesStructuredFields(t *testing.T) {
	now := time.Now()

	plain := fromEntry(&log.Entry{
		Time:    now,
		Level:   log.InfoLevel,
		Message: "Inbound stream connected from 124.131.217.2:35155",
	})
	if plain.Message != "Inbound stream connected from 124.131.217.2:35155" {
		t.Fatalf("plain message = %q", plain.Message)
	}

	structured := fromEntry(&log.Entry{
		Time:    now,
		Level:   log.InfoLevel,
		Message: "SRT session accepted",
		Data: log.Fields{
			"remote": "124.131.217.2:34890",
			"key_id": "LanBing",
			"reason": "publisher disconnected",
		},
	})
	want := `SRT session accepted key_id=LanBing reason="publisher disconnected" remote=124.131.217.2:34890`
	if structured.Message != want {
		t.Fatalf("structured message = %q, want %q", structured.Message, want)
	}
}
