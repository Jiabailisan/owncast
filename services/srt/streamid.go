package srt

import (
	"errors"
	"fmt"
	"strings"

	"github.com/owncast/owncast/models"
)

// maxStreamIDLength is the protocol limit enforced by gosrt (and libsrt).
const maxStreamIDLength = 512

// defaultStreamApp is the application segment used when a streamid grammar
// omits one; it is informational only.
const defaultStreamApp = "live"

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
		App:    defaultStreamApp,
		Key:    streamID,
		Mode:   StreamIDModePublish,
	}, nil
}

func parsePublishStreamID(streamID string, enabledFormats []string) (ParsedStreamID, error) {
	if !models.SRTStreamIDFormatsContains(enabledFormats, models.SRTStreamIDPublishPath) {
		return ParsedStreamID{}, ErrStreamIDFormatDisabled
	}

	rest := strings.TrimPrefix(streamID, "publish:")
	app, key, err := splitStreamAppKey(rest)
	if err != nil {
		return ParsedStreamID{}, err
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

	resource, mode, err := parseAccessControlPairs(body)
	if err != nil {
		return ParsedStreamID{}, err
	}
	if err := validateAccessControlMode(mode); err != nil {
		return ParsedStreamID{}, err
	}

	app, key, err := splitStreamAppKey(resource)
	if err != nil {
		return ParsedStreamID{}, err
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

// parseAccessControlPairs extracts the r (resource) and m (mode) keys from the
// comma-separated SRT access-control content. Unknown keys are permitted by
// the grammar and ignored.
func parseAccessControlPairs(body string) (string, string, error) {
	var resource, mode string
	haveResource := false
	for _, pair := range strings.Split(body, ",") {
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return "", "", ErrStreamIDMalformed
		}
		switch key {
		case "r":
			resource = value
			haveResource = true
		case "m":
			mode = value
		}
	}

	if !haveResource || resource == "" {
		return "", "", ErrStreamIDMalformed
	}
	return resource, mode, nil
}

// validateAccessControlMode enforces the publish intent. The SRT grammar
// defaults an omitted m key to "request" (a subscriber), which an ingest
// listener must reject.
func validateAccessControlMode(mode string) error {
	requestedMode := StreamIDModeRequest
	if mode != "" {
		requestedMode = StreamIDMode(mode)
	}
	if requestedMode != StreamIDModePublish {
		return ErrStreamIDSubscriber
	}
	return nil
}

// splitStreamAppKey parses an optional "<app>/<key>" value. A value without a
// slash is a bare key with the default application.
func splitStreamAppKey(value string) (string, string, error) {
	value = strings.Trim(value, "/")
	if value == "" {
		return "", "", ErrStreamIDMalformed
	}
	if !strings.Contains(value, "/") {
		return defaultStreamApp, value, nil
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrStreamIDMalformed
	}
	return parts[0], parts[1], nil
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
