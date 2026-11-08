// Package ingest owns the protocol-neutral inbound media session abstraction
// shared by the RTMP and SRT ingest services, the single-publisher arbiter, and
// the stream-key validator. It deliberately has no dependency on the stream,
// transcoder or HTTP layers so both protocol adapters and their tests can use
// it in isolation.
package ingest

// Protocol identifies the ingest protocol of a session.
type Protocol string

const (
	// ProtocolRTMP is the RTMP ingest protocol (FLV-muxed over TCP).
	ProtocolRTMP Protocol = "rtmp"
	// ProtocolSRT is the SRT ingest protocol (MPEG-TS over UDP).
	ProtocolSRT Protocol = "srt"
)

// InputFormat describes the media container handed to ffmpeg on stdin.
type InputFormat struct {
	// Name is a human-readable container name: "flv" or "mpegts".
	Name string
	// ExtraArgs are the ffmpeg input flags that precede "-i pipe:0". It is
	// empty for RTMP (ffmpeg auto-detects FLV) so the default argv stays
	// byte-identical to the previous release, and contains the explicit
	// "-f mpegts" for SRT.
	ExtraArgs []string
}

// FLVInputFormat is the RTMP input descriptor.
func FLVInputFormat() InputFormat {
	return InputFormat{Name: "flv"}
}

// MPEGTSInputFormat is the SRT input descriptor.
func MPEGTSInputFormat() InputFormat {
	return InputFormat{Name: "mpegts", ExtraArgs: []string{"-f", "mpegts"}}
}

// String returns the container name for logging and status displays.
func (f InputFormat) String() string {
	if f.Name == "" {
		return "unknown"
	}
	return f.Name
}
