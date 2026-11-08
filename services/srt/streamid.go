package srt

import (
	"errors"
	"fmt"
	"strings"

	"github.com/owncast/owncast/models"
)

// maxStreamIDLength is the protocol limit enforced by gosrt (and libsrt).
const maxStreamIDLength = 512

// Streamid parsing errors. Their messages are stable, non-secret log reasons;
// they are safe to log and are mapped to a generic wire rejection by the auth
// pipeline (a caller never learns which key or format detail failed).
var (
	// ErrStreamIDEmpty means the caller did not send a streamid.
	ErrStreamIDEmpty = errors.New("streamid_required")
	// ErrStreamIDTooLong means the streamid exceeds the SRT protocol limit.
	ErrStreamIDTooLong = errors.New("streamid_too_long")
	// ErrStreamIDFormatDisabled means the streamid matches a format the
	// operator disabled.
	ErrStreamIDFormatDisabled = errors.New("streamid_format_disabled")
	// ErrStreamIDMalformed means the streamid does not match its grammar.
	ErrStreamIDMalformed = errors.New("streamid_malformed")
	// ErrStreamIDSubscriber means the caller is asking to receive media.
	ErrStreamIDSubscriber = errors.New("subscriber_mode_not_supported")
	// ErrStreamIDInvalidKey means the extracted key contains characters that
	// Owncast stream keys never contain.
	ErrStreamIDInvalidKey = errors.New("streamid_invalid_key")
)

// StreamIDMode is the connection intent requested by the caller. SRT's
// access-control grammar defaults to "request" (a subscriber), which an
// ingest listener must reject.
type StreamIDMode string

const (
	// StreamIDModePublish means the caller wants to send media.
	StreamIDModePublish StreamIDMode = "publish"
	// StreamIDModeRequest means the caller wants to receive media.
	StreamIDModeRequest StreamIDMode = "request"
)

// ParsedStreamID is the result of parsing an SRT streamid.
type ParsedStreamID struct {
	// Format identifies which enabled grammar matched.
	Format models.SRTStreamIDFormat
	// App is the optional application path segment (informational).
	App string
	// Key is the candidate stream key. It is secret material: never log it.
	Key string
	// Mode is the requested connection mode.
	Mode StreamIDMode
}

// ParseStreamID parses an SRT streamid against the enabled format list. An
// empty formats list enables every format (models.SRTStreamIDFormatsContains
// semantics). Parsing is pure and never touches the network or storage.
//
// The three supported formats are:
//
//   - raw-key: the whole streamid is the key, e.g. "abc123".
//   - publish:/live/<key>: Owncast's RTMP-like path, e.g. "publish:/live/abc123".
//   - SRT access control: "#!::r=<app>/<key>,m=publish"; the m key defaults to
//     "request" per the SRT access-control specification, so omitting it is a
//     subscriber request and is rejected.
func ParseStreamID(streamID string, enabledFormats []string) (ParsedStreamID, error) {
	if streamID == "" {
		return ParsedStreamID{}, ErrStreamIDEmpty
	}
	if len(streamID) > maxStreamIDLength {
		return ParsedStreamID{}, ErrStreamIDTooLong
	}

	switch {
	case strings.HasPrefix(streamID, "#!::"):
		return parseAccessControlStreamID(streamID, enabledFormats)
	case strings.HasPrefix(streamID, "publish:"):
		return parsePublishStreamID(streamID, enabledFormats)
	default:
		return parseRawStreamID(streamID, enabledFormats)
	}
}

func parseRawStreamID(streamID string, enabledFormats []string) (ParsedStreamID, error) {
	if !models.SRTStreamIDFormatsContains(enabledFormats, models.SRTStreamIDRawKey) {
		return ParsedStreamID{}, ErrStreamIDFormatDisabled
	}
	if err := validateStreamKey(streamID); err != nil {
		return ParsedStreamID{}, err
	}
	return ParsedStreamID{
		Format: models.SRTStreamIDRawKey,
		App:    "live",
		Key:    streamID,
		Mode:   StreamIDModePublish,
	}, nil
}

func parsePublishStreamID(streamID string, enabledFormats []string) (ParsedStreamID, error) {
	if !models.SRTStreamIDFormatsContains(enabledFormats, models.SRTStreamIDPublishPath) {
		return ParsedStreamID{}, ErrStreamIDFormatDisabled
	}

	rest := strings.TrimPrefix(streamID, "publish:")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return ParsedStreamID{}, ErrStreamIDMalformed
	}

	app := "live"
	key := rest
	if strings.Contains(rest, "/") {
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return ParsedStreamID{}, ErrStreamIDMalformed
		}
		app, key = parts[0], parts[1]
	}

	if err := validateStreamKey(key); err != nil {
		return ParsedStreamID{}, err
	}

	return ParsedStreamID{
		Format: models.SRTStreamIDPublishPath,
		App:    app,
		Key:    key,
		Mode:   StreamIDModePublish,
	}, nil
}

func parseAccessControlStreamID(streamID string, enabledFormats []string) (ParsedStreamID, error) {
	if !models.SRTStreamIDFormatsContains(enabledFormats, models.SRTStreamIDAccessControl) {
		return ParsedStreamID{}, ErrStreamIDFormatDisabled
	}

	body := strings.TrimPrefix(streamID, "#!::")
	if body == "" {
		return ParsedStreamID{}, ErrStreamIDMalformed
	}

	var resource, mode string
	haveResource := false
	for _, pair := range strings.Split(body, ",") {
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return ParsedStreamID{}, ErrStreamIDMalformed
		}
		switch key {
		case "r":
			resource = value
			haveResource = true
		case "m":
			mode = value
		default:
			// Unknown keys are permitted by the SRT access-control grammar and
			// ignored here.
		}
	}

	if !haveResource || resource == "" {
		return ParsedStreamID{}, ErrStreamIDMalformed
	}

	requestedMode := StreamIDModeRequest
	if mode != "" {
		requestedMode = StreamIDMode(mode)
	}
	if requestedMode != StreamIDModePublish {
		return ParsedStreamID{}, ErrStreamIDSubscriber
	}

	resource = strings.Trim(resource, "/")
	if resource == "" {
		return ParsedStreamID{}, ErrStreamIDMalformed
	}

	app := "live"
	key := resource
	if strings.Contains(resource, "/") {
		parts := strings.Split(resource, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return ParsedStreamID{}, ErrStreamIDMalformed
		}
		app, key = parts[0], parts[1]
	}

	if err := validateStreamKey(key); err != nil {
		return ParsedStreamID{}, err
	}

	return ParsedStreamID{
		Format: models.SRTStreamIDAccessControl,
		App:    app,
		Key:    key,
		Mode:   StreamIDModePublish,
	}, nil
}

// validateStreamKey rejects keys that Owncast never generates and that could
// confuse the streamid grammars, logs or URL displays.
func validateStreamKey(key string) error {
	if key == "" {
		return ErrStreamIDMalformed
	}
	if len(key) > maxStreamIDLength {
		return ErrStreamIDTooLong
	}
	for i := 0; i < len(key); i++ {
		if !isPrintableKeyByte(key[i]) {
			return ErrStreamIDInvalidKey
		}
	}
	return nil
}

func isPrintableKeyByte(b byte) bool {
	if b < 0x21 || b > 0x7e {
		return false
	}
	switch b {
	case '/', ':', ',', '=', '#', '!', '?', '&', '%', '+':
		return false
	default:
		return true
	}
}

// String returns a log-safe description of a parsed streamid (format and app
// only; the key is intentionally omitted).
func (p ParsedStreamID) String() string {
	return fmt.Sprintf("format=%s app=%s", p.Format, p.App)
}
