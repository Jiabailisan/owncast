package admin

import (
	log "github.com/sirupsen/logrus"
)

// The generated request bodies type every configuration value as interface{},
// so a malformed body (or a value sent as the wrong JSON type) used to panic the
// handler with an unchecked type assertion. These helpers return the zero value
// for a wrong type and log it; the repository setters validate the value and
// reject an empty or out-of-range result, so the client still gets an error
// response instead of a 500 from a recovered panic.

// Scheme names shared by the admin handlers that build or validate URLs.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

func valueAsString(value interface{}) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	log.Debugln("configuration value is not a string")

	return ""
}

func valueAsBool(value interface{}) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	log.Debugln("configuration value is not a boolean")

	return false
}

func valueAsFloat64(value interface{}) float64 {
	if typed, ok := value.(float64); ok {
		return typed
	}
	log.Debugln("configuration value is not a number")

	return 0
}

// derefStringSlice returns the pointed-to slice, or nil when the field was
// omitted from the request body.
func derefStringSlice(value *[]string) []string {
	if value == nil {
		return nil
	}

	return *value
}

// Checked variants for handlers that report a specific error for a wrong type.
func valueAsStringOK(value interface{}) (string, bool) {
	typed, ok := value.(string)

	return typed, ok
}

func valueAsFloat64OK(value interface{}) (float64, bool) {
	typed, ok := value.(float64)

	return typed, ok
}

func valueAsBoolOK(value interface{}) (bool, bool) {
	typed, ok := value.(bool)

	return typed, ok
}
