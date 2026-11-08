// Package srt implements the SRT ingest service: a gosrt listener, the
// accept-time authentication pipeline (streamid parsing, CIDR filters,
// per-IP failure rate limiting, passphrase enforcement and the shared
// stream-key validator), the single-publisher arbiter integration, and the
// protocol-neutral ingest.Session adapter that feeds MPEG-TS bytes to the
// transcoder.
//
// The service is constructed once in main.go (the composition root) and has
// no package-level state. Configuration is supplied through a provider
// function so that persisted settings changes can be applied without
// reconstructing the service.
package srt
