package srt

import (
	"errors"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
)

func TestParseStreamIDValid(t *testing.T) {
	tests := []struct {
		name     string
		streamID string
		formats  []string
		format   models.SRTStreamIDFormat
		app      string
		key      string
	}{
		{name: "raw key", streamID: "goodkey", format: models.SRTStreamIDRawKey, app: "live", key: "goodkey"},
		{name: "publish path", streamID: "publish:/live/goodkey", format: models.SRTStreamIDPublishPath, app: "live", key: "goodkey"},
		{name: "publish path without leading slash", streamID: "publish:live/goodkey", format: models.SRTStreamIDPublishPath, app: "live", key: "goodkey"},
		{name: "publish path without app", streamID: "publish:goodkey", format: models.SRTStreamIDPublishPath, app: "live", key: "goodkey"},
		{name: "publish path with key characters", streamID: "publish:/live/Key-123_abc.def", format: models.SRTStreamIDPublishPath, app: "live", key: "Key-123_abc.def"},
		{name: "access control", streamID: "#!::r=live/goodkey,m=publish", format: models.SRTStreamIDAccessControl, app: "live", key: "goodkey"},
		{name: "access control unordered", streamID: "#!::m=publish,r=live/goodkey", format: models.SRTStreamIDAccessControl, app: "live", key: "goodkey"},
		{name: "access control without app", streamID: "#!::r=goodkey,m=publish", format: models.SRTStreamIDAccessControl, app: "live", key: "goodkey"},
		{name: "access control with unknown keys", streamID: "#!::r=live/goodkey,m=publish,u=someone", format: models.SRTStreamIDAccessControl, app: "live", key: "goodkey"},
		{name: "raw key with explicit formats list", streamID: "goodkey", formats: []string{string(models.SRTStreamIDRawKey)}, format: models.SRTStreamIDRawKey, app: "live", key: "goodkey"},
		{name: "access control with explicit formats list", streamID: "#!::r=live/goodkey,m=publish", formats: []string{string(models.SRTStreamIDAccessControl)}, format: models.SRTStreamIDAccessControl, app: "live", key: "goodkey"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseStreamID(test.streamID, test.formats)
			if err != nil {
				t.Fatalf("ParseStreamID(%q) returned error: %v", test.streamID, err)
			}
			if parsed.Format != test.format {
				t.Errorf("Format = %q, want %q", parsed.Format, test.format)
			}
			if parsed.App != test.app {
				t.Errorf("App = %q, want %q", parsed.App, test.app)
			}
			if parsed.Key != test.key {
				t.Errorf("Key = %q, want %q", parsed.Key, test.key)
			}
			if parsed.Mode != StreamIDModePublish {
				t.Errorf("Mode = %q, want %q", parsed.Mode, StreamIDModePublish)
			}
		})
	}
}

func TestParseStreamIDRejects(t *testing.T) {
	tests := []struct {
		name     string
		streamID string
		formats  []string
		wantErr  error
	}{
		{name: "empty", streamID: "", wantErr: ErrStreamIDEmpty},
		{name: "too long", streamID: strings.Repeat("a", maxStreamIDLength+1), wantErr: ErrStreamIDTooLong},
		{name: "raw format disabled", streamID: "goodkey", formats: []string{string(models.SRTStreamIDAccessControl)}, wantErr: ErrStreamIDFormatDisabled},
		{name: "publish format disabled", streamID: "publish:/live/goodkey", formats: []string{string(models.SRTStreamIDRawKey)}, wantErr: ErrStreamIDFormatDisabled},
		{name: "access control format disabled", streamID: "#!::r=live/goodkey,m=publish", formats: []string{string(models.SRTStreamIDRawKey), string(models.SRTStreamIDPublishPath)}, wantErr: ErrStreamIDFormatDisabled},
		{name: "access control without body", streamID: "#!::", wantErr: ErrStreamIDMalformed},
		{name: "access control without resource", streamID: "#!::m=publish", wantErr: ErrStreamIDMalformed},
		{name: "access control empty resource", streamID: "#!::r=,m=publish", wantErr: ErrStreamIDMalformed},
		{name: "access control malformed pair", streamID: "#!::r=live/goodkey,m", wantErr: ErrStreamIDMalformed},
		{name: "access control default mode is request", streamID: "#!::r=live/goodkey", wantErr: ErrStreamIDSubscriber},
		{name: "access control explicit request", streamID: "#!::r=live/goodkey,m=request", wantErr: ErrStreamIDSubscriber},
		{name: "access control bidirectional", streamID: "#!::r=live/goodkey,m=bidirectional", wantErr: ErrStreamIDSubscriber},
		{name: "access control extra path segment", streamID: "#!::r=live/a/b,m=publish", wantErr: ErrStreamIDMalformed},
		{name: "publish path empty", streamID: "publish:", wantErr: ErrStreamIDMalformed},
		{name: "publish path only slashes", streamID: "publish:///", wantErr: ErrStreamIDMalformed},
		{name: "publish path extra segment", streamID: "publish:/live/a/b", wantErr: ErrStreamIDMalformed},
		{name: "raw key with slash", streamID: "good/key", wantErr: ErrStreamIDInvalidKey},
		{name: "raw key with colon", streamID: "good:key", wantErr: ErrStreamIDInvalidKey},
		{name: "raw key with space", streamID: "good key", wantErr: ErrStreamIDInvalidKey},
		{name: "raw key with control character", streamID: "good\x01key", wantErr: ErrStreamIDInvalidKey},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseStreamID(test.streamID, test.formats)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseStreamID(%q) error = %v, want %v", test.streamID, err, test.wantErr)
			}
		})
	}
}

func TestParsedStreamIDStringDoesNotLeakKey(t *testing.T) {
	const key = "supersecretkey"
	parsed, err := ParseStreamID("publish:/live/"+key, nil)
	if err != nil {
		t.Fatalf("ParseStreamID: %v", err)
	}
	if strings.Contains(parsed.String(), key) {
		t.Fatalf("ParsedStreamID.String() leaked the key: %q", parsed.String())
	}
}

func FuzzParseStreamID(fuzz *testing.F) {
	seeds := []string{
		"",
		"goodkey",
		"publish:/live/goodkey",
		"#!::r=live/goodkey,m=publish",
		"#!::r=live/goodkey",
		"#!::",
		strings.Repeat("a", maxStreamIDLength+1),
	}
	for _, seed := range seeds {
		fuzz.Add(seed)
	}

	formats := models.AllSRTStreamIDFormats()
	fuzz.Fuzz(func(t *testing.T, streamID string) {
		parsed, err := ParseStreamID(streamID, formats)
		if err != nil {
			return
		}
		if parsed.Key == "" {
			t.Fatalf("parsed streamid has empty key: %q", streamID)
		}
		if parsed.Mode != StreamIDModePublish {
			t.Fatalf("parsed streamid has non-publish mode: %q", streamID)
		}
		if err := validateStreamKey(parsed.Key); err != nil {
			t.Fatalf("parsed streamid has invalid key: %v", err)
		}
	})
}
