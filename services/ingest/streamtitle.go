package ingest

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// MaxStreamTitleLength bounds a title that arrives with a publish request.
const MaxStreamTitleLength = 128

// ParseStreamTitle decodes and validates a title taken from a single query value.
//
// It returns an empty string when the value is absent, cannot be percent-decoded, is empty
// after trimming, is not valid UTF-8, contains a control character, or is longer than
// MaxStreamTitleLength. An unusable title must never replace the saved one, so every
// rejection is a silent empty result and the caller keeps the default title.
func ParseStreamTitle(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	// Decoded exactly once: a title that itself contains a percent sign must survive
	// without being decoded a second time.
	decoded, err := url.QueryUnescape(trimmed)
	if err != nil {
		return ""
	}

	return sanitizeStreamTitle(decoded)
}

// ParseStreamTitleFromQuery extracts the title from a raw, still encoded query string.
//
// The value is decoded once by the query parser and validated afterwards, so it is never
// decoded twice. A query that carries the title more than once is ambiguous and yields no
// title rather than an arbitrary one.
func ParseStreamTitleFromQuery(rawQuery string) string {
	if strings.TrimSpace(rawQuery) == "" {
		return ""
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}

	titles := values["title"]
	if len(titles) != 1 {
		return ""
	}

	return sanitizeStreamTitle(titles[0])
}

// sanitizeStreamTitle validates an already decoded title.
func sanitizeStreamTitle(decoded string) string {
	decoded = strings.TrimSpace(decoded)
	if decoded == "" || !utf8.ValidString(decoded) || utf8.RuneCountInString(decoded) > MaxStreamTitleLength {
		return ""
	}

	for _, r := range decoded {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}

	return decoded
}
