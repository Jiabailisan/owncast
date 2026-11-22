package srt

import (
	"testing"

	"github.com/owncast/owncast/models"
)

// A raw key may carry a title suffix; the key itself still authenticates.
func TestRawStreamIDWithTitle(t *testing.T) {
	formats := []string{string(models.SRTStreamIDRawKey)}

	parsed, err := ParseStreamID("TEST_KEY?title=%23GameTime", formats)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Key != "TEST_KEY" {
		t.Fatalf("key = %q, want the bare key", parsed.Key)
	}
	if parsed.Title != "#GameTime" {
		t.Fatalf("title = %q, want #GameTime", parsed.Title)
	}

	// A bare key keeps working and carries no title.
	bare, err := ParseStreamID("TEST_KEY", formats)
	if err != nil {
		t.Fatalf("bare key: %v", err)
	}
	if bare.Key != "TEST_KEY" || bare.Title != "" {
		t.Fatalf("bare = %+v, want the key without a title", bare)
	}

	// An unusable title must not invalidate a correct key.
	broken, err := ParseStreamID("TEST_KEY?title=%zz", formats)
	if err != nil {
		t.Fatalf("invalid title must not fail the key: %v", err)
	}
	if broken.Key != "TEST_KEY" || broken.Title != "" {
		t.Fatalf("broken title = %+v, want the key without a title", broken)
	}

	// An empty title after the separator is the same as no title.
	empty, err := ParseStreamID("TEST_KEY?title=", formats)
	if err != nil {
		t.Fatalf("empty title: %v", err)
	}
	if empty.Key != "TEST_KEY" || empty.Title != "" {
		t.Fatalf("empty title = %+v", empty)
	}
}
