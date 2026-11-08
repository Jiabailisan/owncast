package ingest

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/owncast/owncast/models"
)

const rtmpPathPrefix = "/live/"

// StreamKeyValidator is the single stream-key check shared by RTMP and SRT.
// The key material is never logged or returned: KeyID returns the operator's
// comment when configured, otherwise a short SHA-256 fingerprint.
type StreamKeyValidator struct {
	// getKeys is resolved per validation so admin edits apply without a
	// restart.
	getKeys func() []models.StreamKey
	// temporaryKey is the session-only override (--streamkey). When set it
	// replaces the persisted key list, matching the historical RTMP behavior.
	temporaryKey string
}

// NewStreamKeyValidator builds a validator. getKeys may be nil.
func NewStreamKeyValidator(getKeys func() []models.StreamKey, temporaryKey string) *StreamKeyValidator {
	return &StreamKeyValidator{getKeys: getKeys, temporaryKey: temporaryKey}
}

// ValidateKey checks a raw stream key. It returns a stable, non-secret key ID
// and whether the key is valid. Comparison is constant time.
func (v *StreamKeyValidator) ValidateKey(key string) (string, bool) {
	if key == "" && v.temporaryKey == "" {
		return "", false
	}

	var keys []models.StreamKey
	if v.temporaryKey != "" {
		keys = []models.StreamKey{{Key: &v.temporaryKey}}
	} else if v.getKeys != nil {
		keys = v.getKeys()
	}

	var matched *models.StreamKey
	for i := range keys {
		if keys[i].Key == nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(*keys[i].Key)) == 1 {
			matched = &keys[i]
			break
		}
	}
	if matched == nil {
		return "", false
	}
	return describeStreamKey(*matched), true
}

// ValidateRTMPPath validates the RTMP connection path ("/live/<key>") exactly
// like the previous inline implementation.
func (v *StreamKeyValidator) ValidateRTMPPath(path string) (string, bool) {
	if !strings.HasPrefix(path, rtmpPathPrefix) {
		return "", false
	}
	return v.ValidateKey(path[len(rtmpPathPrefix):])
}

// describeStreamKey returns the non-secret identifier for a key: the operator
// comment when present, else a short fingerprint.
func describeStreamKey(key models.StreamKey) string {
	if key.Comment != nil && *key.Comment != "" {
		return *key.Comment
	}
	if key.Key == nil {
		return "unknown"
	}
	sum := sha256.Sum256([]byte(*key.Key))
	return "key:" + hex.EncodeToString(sum[:4])
}

// FingerprintKey is exported for tests and logging helpers that need a stable
// non-reversible identifier without validating anything.
func FingerprintKey(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}
