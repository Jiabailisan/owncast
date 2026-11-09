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

	// Additive source-properties fields. They are populated for SRT from the
	// MPEG-TS/H.264 inspector (RTMP carries profile only in onMetaData when
	// the encoder provides it) and drive the live status source panel and the
	// WebRTC copy-eligibility check (spec C4).
	VideoProfile string `json:"videoProfile,omitempty"`
	VideoLevel   string `json:"videoLevel,omitempty"`
	HasBFrames   *bool  `json:"hasBFrames,omitempty"`

	// Audio codec details. They are populated for SRT from the MPEG-TS/AAC
	// inspector (RTMP onMetaData does not carry them, so RTMP leaves them
	// unset and the UI shows N/A). AudioSampleRate is in Hz.
	AudioProfile    string `json:"audioProfile,omitempty"`
	AudioSampleRate int    `json:"audioSampleRate,omitempty"`
	AudioChannels   int    `json:"audioChannels,omitempty"`
}

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
