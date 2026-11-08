package webrtc

// Error codes returned to WHEP clients. They are stable strings because the
// viewer frontend switches on them to decide between retrying and falling
// back to HLS (spec 7.1).
const (
	CodeDisabled            = "webrtc_disabled"
	CodeCapacity            = "webrtc_capacity"
	CodePerIPCapacity       = "webrtc_per_ip_capacity"
	CodeRateLimited         = "webrtc_rate_limited"
	CodeAuthRequired        = "webrtc_auth_required"
	CodeDomainMismatch      = "webrtc_domain_mismatch"
	CodeBadOffer            = "webrtc_bad_offer"
	CodeUnsupportedMedia    = "webrtc_unsupported_media"
	CodeCodecMismatch       = "webrtc_codec_mismatch"
	CodeVariantNotFound     = "webrtc_variant_not_found"
	CodeSessionNotFound     = "webrtc_session_not_found"
	CodeICEGatherTimeout    = "webrtc_ice_gather_timeout"
	CodeApplicationDisabled = "webrtc_application_disabled"
	CodeUnsupported         = "webrtc_unsupported"
	CodeInternal            = "webrtc_internal"
)
