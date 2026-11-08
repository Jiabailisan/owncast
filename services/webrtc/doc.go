// Package webrtc implements Owncast's WebRTC delivery: a Pion engine with the
// ICE/DTLS transport policy derived from the operator's domain and network
// configuration, a WHEP signaling endpoint, per-variant shared RTP tracks fed
// by FFmpeg loopback legs, and the viewer capacity/stats registry consumed by
// the admin API.
//
// The design is documented in docs/design/phase0/03-webrtc-dependency-and-design.md.
// The service is constructed once in main.go (the composition root) with
// New(Deps) and is injected into the HTTP router and the transcoder plan
// builder. It keeps no package-level state.
package webrtc
