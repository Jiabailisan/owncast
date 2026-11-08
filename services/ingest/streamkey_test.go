package ingest

import (
	"testing"

	"github.com/owncast/owncast/models"
)

func streamKeysForTest() []models.StreamKey {
	comment := "Primary key"
	key := "abc123"
	key2 := "one/two/three"
	return []models.StreamKey{
		{Key: &key, Comment: &comment},
		{Key: &key2},
	}
}

func TestValidateKey(t *testing.T) {
	v := NewStreamKeyValidator(streamKeysForTest, "")

	if _, ok := v.ValidateKey("abc123"); !ok {
		t.Error("expected the primary key to validate")
	}
	if id, ok := v.ValidateKey("abc123"); ok && id != "Primary key" {
		t.Errorf("expected the configured comment as key id, got %q", id)
	}
	if _, ok := v.ValidateKey("wrong"); ok {
		t.Error("expected an invalid key to be rejected")
	}
	if _, ok := v.ValidateKey(""); ok {
		t.Error("expected an empty key to be rejected")
	}
	if _, ok := v.ValidateKey("abc"); ok {
		t.Error("expected a key prefix to be rejected")
	}
	if _, ok := v.ValidateKey("abc1234"); ok {
		t.Error("expected a key with a suffix to be rejected")
	}
}

func TestValidateRTMPPath(t *testing.T) {
	v := NewStreamKeyValidator(streamKeysForTest, "")

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"positive", "/live/abc123", true},
		{"second key", "/live/one/two/three", true},
		{"negative", "/live/def", false},
		{"bad path", "nonsense", false},
		{"missing secret", "/live/", false},
		{"missing trailing slash", "/live", false},
		{"key before /live/", "/streamkey/live", false},
		{"stuff before and after", "/before/live/abc123", false},
		{"only suffix matches", "/live/three", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := v.ValidateRTMPPath(tt.path); got != tt.want {
				t.Errorf("ValidateRTMPPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestTemporaryKeyOverridesPersistedKeys(t *testing.T) {
	v := NewStreamKeyValidator(streamKeysForTest, "temporary")

	if _, ok := v.ValidateKey("temporary"); !ok {
		t.Error("expected the temporary key to validate")
	}
	if _, ok := v.ValidateKey("abc123"); ok {
		t.Error("expected persisted keys to be ignored while a temporary key is set")
	}
}

func TestFingerprintKeyNeverReturnsTheKey(t *testing.T) {
	fp := FingerprintKey("abc123")
	if fp == "" || fp == "abc123" {
		t.Fatalf("fingerprint must be non-empty and not the key, got %q", fp)
	}
	if len(fp) != 8 {
		t.Errorf("expected a short 8-char fingerprint, got %q", fp)
	}
}
