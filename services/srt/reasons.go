package srt

import (
	"errors"

	gosrt "github.com/datarhei/gosrt"
)

// Authentication failure reasons. They are stable strings used for structured
// logs only; the wire rejection is always one of the generic predefined SRT
// codes so a caller cannot probe our configuration.
const (
	reasonCIDRDenied           = "cidr_denied"
	reasonCIDRNotAllowed       = "cidr_not_allowed"
	reasonStreamIDRequired     = "streamid_required"
	reasonStreamIDEmpty        = "streamid_empty"
	reasonSubscriberMode       = "subscriber_mode_not_supported"
	reasonEncryptionRequired   = "encryption_required"
	reasonBadPassphrase        = "bad_passphrase"
	reasonRateLimited          = "auth_rate_limited"
	reasonBadStreamKey         = "bad_stream_key"
	reasonPublisherActive      = "publisher_already_active"
	reasonHandshakeCapExceeded = "handshake_cap_exceeded"
	reasonAcceptFailed         = "accept_failed"
	reasonInternal             = "internal_error"
)

// Wire rejection codes. libsrt only renders predefined (< 2000) rejection
// reasons with an operator-readable message; extended SRT_REJX codes display
// as "Unknown". The detailed reason lives in the Owncast log instead.
var (
	wireRejectGeneric   = gosrt.REJ_PEER
	wireRejectResource  = gosrt.REJ_RESOURCE
	wireRejectBadSecret = gosrt.REJ_BADSECRET
	wireRejectUnsecure  = gosrt.REJ_UNSECURE
	wireRejectSystem    = gosrt.REJ_SYSTEM
)

// Session termination reasons.
var (
	// ErrRemoteClosed means the publisher closed the SRT connection.
	ErrRemoteClosed = errors.New("publisher disconnected")
	// ErrIdleTimeout means no media arrived within the configured idle window.
	ErrIdleTimeout = errors.New("srt connection idle timeout")
	// ErrAdminDisconnect means an admin force-disconnected the publisher.
	ErrAdminDisconnect = errors.New("disconnected by admin")
	// ErrServiceStop means the SRT service stopped and dropped the session.
	ErrServiceStop = errors.New("srt service stopped")
	// ErrStreamEnded means the stream lifecycle ended the session.
	ErrStreamEnded = errors.New("stream ended")
	// ErrHandlerFailed means the ingest handler rejected the accepted session.
	ErrHandlerFailed = errors.New("ingest handler failed")
	// ErrSessionNotBound is returned when the media reader is used before the
	// SRT connection is bound to the session.
	ErrSessionNotBound = errors.New("srt session is not bound")
	// ErrNoHandler is returned by Start when no ingest handler was injected.
	ErrNoHandler = errors.New("srt handler is not configured")
	// ErrServiceNotRunning is returned by operations that require a listener.
	ErrServiceNotRunning = errors.New("srt service is not running")
)
