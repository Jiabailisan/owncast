package models

import "time"

// Broadcaster represents the details around the inbound broadcasting connection.
type Broadcaster struct {
	Time          time.Time            `json:"time"`
	RemoteAddr    string               `json:"remoteAddr"`
	StreamDetails InboundStreamDetails `json:"streamDetails"`
}

// InboundStreamDetails represents an inbound broadcast stream.
type InboundStreamDetails struct {
	VideoCodec     string  `json:"videoCodec"`
	AudioCodec     string  `json:"audioCodec"`
	Encoder        string  `json:"encoder"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	VideoBitrate   int     `json:"videoBitrate"`
	AudioBitrate   int     `json:"audioBitrate"`
	VideoFramerate float32 `json:"framerate"`
	VideoOnly      bool    `json:"-"`

	// Additive source-properties fields, extracted from the bitstream by the
	// protocol's own inspector: the MPEG-TS/H.264 inspector for SRT and the
	// FLV/AVC inspector for RTMP. They drive the live status source panel and the
	// WebRTC copy-eligibility check (spec C4), so both protocols answer the same
	// questions about a source.
	VideoProfile string `json:"videoProfile,omitempty"`
	VideoLevel   string `json:"videoLevel,omitempty"`
	HasBFrames   *bool  `json:"hasBFrames,omitempty"`

	// Audio codec details, extracted by the same inspectors.
	// AudioSampleRate is in Hz.
	AudioProfile    string `json:"audioProfile,omitempty"`
	AudioSampleRate int    `json:"audioSampleRate,omitempty"`
	AudioChannels   int    `json:"audioChannels,omitempty"`

	// VideoBitrateSource names where VideoBitrate came from: "measured" when it
	// was measured from the bitstream (SRT PES statistics, RTMP video tag
	// payload), "declared" when it is the encoder's own onMetaData claim. Empty
	// when the bitrate is unknown. A WebRTC transcode leg prefers a measured rate
	// over a declared one when it chooses its automatic target.
	VideoBitrateSource string `json:"videoBitrateSource,omitempty"`
}

// Video bitrate provenance values for InboundStreamDetails.VideoBitrateSource.
const (
	// VideoBitrateMeasured is a bitrate measured from the bitstream.
	VideoBitrateMeasured = "measured"
	// VideoBitrateDeclared is a bitrate the encoder declared in container metadata.
	VideoBitrateDeclared = "declared"
)

// RTMPStreamMetadata is the raw metadata that comes in with a RTMP connection.
type RTMPStreamMetadata struct {
	VideoCodec     interface{} `json:"videocodecid"`
	AudioCodec     interface{} `json:"audiocodecid"`
	Encoder        string      `json:"encoder"`
	Width          int         `json:"width"`
	Height         int         `json:"height"`
	VideoBitrate   float32     `json:"videodatarate"`
	VideoFramerate float32     `json:"framerate"`
	AudioBitrate   float32     `json:"audiodatarate"`
}
