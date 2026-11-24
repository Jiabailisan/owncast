// Package flv inspects the FLV tags of an RTMP ingest so the H.264 copy
// eligibility rules can run on the actual bitstream, exactly as they do for the
// MPEG-TS (SRT) path. RTMP's onMetaData only carries the encoder's claims: it has
// no H.264 profile, level or B-frame information, so without this probe an RTMP
// passthrough leg could only be delivered "unverified". The probe reuses the
// H.264 rules in the mpegts package, so the two protocols cannot disagree about
// a source.
package flv

import (
	"fmt"
	"sync"

	"github.com/owncast/owncast/services/ingest/mpegts"
)

// Video formats and AVC packet types the inspector acts on. They are exported
// so a caller can feed demuxed packets (an RTMP pump hands over av.Packet
// values, not raw FLV tags).
const (
	VideoFormatAVC  = 7
	VideoFormatHEVC = 12

	AVCPacketSequenceHeader = 0
	AVCPacketNALU           = 1
)

// Audio formats and AAC packet types the inspector acts on.
const (
	SoundFormatAAC        = 10
	AACPacketSequenceHead = 0
	AACPacketRaw          = 1
)

// Video bitrate window: the same shape the SRT path uses (a 3 s media-time
// window with a 1 s minimum span), so the two protocols report comparable
// numbers and an unmeasurable window stays "unknown" instead of guessing.
const (
	bitrateWindowMs  = 3000
	minBitrateSpanMs = 1000
	maxBitratePoints = 4096
)

// Info is the inspected source property set. Zero values mean "unknown".
type Info struct {
	VideoCodec string
	AudioCodec string

	Profile  string
	Level    string
	Width    int
	Height   int
	FPS      float64
	FPSValid bool

	HasBFrames    bool
	BFrameChecked bool

	VideoBitrateKbps  float64
	VideoBitrateValid bool
	AudioBitrateKbps  float64
	AudioBitrateValid bool

	AudioProfile    string
	AudioSampleRate int
	AudioChannels   int
}

// bitratePoint is one sampled media time with the payload bytes seen at it.
type bitratePoint struct {
	timeMs uint32
	bytes  int
}

// rateTracker measures a payload bitrate over a media-time window. It mirrors
// the MPEG-TS tracker: headers are excluded, the window is 3 s of media time and
// the value stays invalid below a 1 s span.
type rateTracker struct {
	points []bitratePoint
	total  int
	value  float64
	valid  bool
}

func (t *rateTracker) add(timeMs uint32, payload int) {
	t.points = append(t.points, bitratePoint{timeMs: timeMs, bytes: payload})
	t.total += payload
	// Drop samples outside the window, measured from the newest sample so a
	// stalled timestamp cannot keep the window open forever.
	for len(t.points) > 1 {
		span := timeDeltaMs(timeMs, t.points[0].timeMs)
		if span <= bitrateWindowMs {
			break
		}
		t.total -= t.points[0].bytes
		t.points = t.points[1:]
	}
	if len(t.points) > maxBitratePoints {
		excess := len(t.points) - maxBitratePoints
		for index := 0; index < excess; index++ {
			t.total -= t.points[index].bytes
		}
		t.points = t.points[excess:]
	}
	if len(t.points) < 2 {
		return
	}
	span := timeDeltaMs(timeMs, t.points[0].timeMs)
	if span < minBitrateSpanMs {
		return
	}
	t.value = float64(t.total) * 8 / float64(span)
	t.valid = true
}

// timeDeltaMs returns b-a in milliseconds, tolerating the 32-bit timestamp wrap
// that an RTMP stream reaches after about 49 days.
func timeDeltaMs(a, b uint32) int64 {
	return int64(a - b)
}

// Inspector consumes RTMP FLV tags and publishes the source properties.
type Inspector struct {
	mu    sync.Mutex
	probe mpegts.H264Probe
	info  Info

	videoRate rateTracker
	audioRate rateTracker
}

// NewInspector returns an empty inspector.
func NewInspector() *Inspector { return &Inspector{} }

// FeedVideo inspects one FLV video tag. format and packetType are the values the
// FLV tag header carried; payload is the tag body after that header.
func (i *Inspector) FeedVideo(format, packetType uint8, payload []byte, timeMs uint32) {
	if len(payload) == 0 {
		return
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	switch format {
	case VideoFormatAVC:
		i.info.VideoCodec = "h264"
	case VideoFormatHEVC:
		i.info.VideoCodec = "hevc"
	}

	if format == VideoFormatAVC {
		switch packetType {
		case AVCPacketSequenceHeader:
			i.parseAVCConfigurationRecord(payload)
		case AVCPacketNALU:
			i.feedAVCCNALUs(payload)
		}
	}

	// Only real media payload counts toward the bitrate; the sequence header is
	// configuration, not video.
	if packetType == AVCPacketNALU || (format != VideoFormatAVC && format != VideoFormatHEVC) {
		i.videoRate.add(timeMs, len(payload))
		i.info.VideoBitrateKbps = i.videoRate.value
		i.info.VideoBitrateValid = i.videoRate.valid
	}
}

// FeedAudio inspects one FLV audio tag.
func (i *Inspector) FeedAudio(format, packetType uint8, payload []byte, timeMs uint32) {
	if len(payload) == 0 {
		return
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	if format == SoundFormatAAC {
		i.info.AudioCodec = "aac"
		if packetType == AACPacketSequenceHead {
			i.parseAudioSpecificConfig(payload)
		}
	} else {
		i.info.AudioCodec = audioCodecName(format)
	}

	i.audioRate.add(timeMs, len(payload))
	i.info.AudioBitrateKbps = i.audioRate.value
	i.info.AudioBitrateValid = i.audioRate.valid
}

// Snapshot returns the current property set, including the H.264 probe results.
func (i *Inspector) Snapshot() Info {
	i.mu.Lock()
	defer i.mu.Unlock()

	info := i.info
	probe := i.probe.Snapshot()
	if probe.VideoCodec != "" {
		info.VideoCodec = probe.VideoCodec
	}
	if probe.Profile != "" {
		info.Profile = probe.Profile
	}
	if probe.Level != "" {
		info.Level = probe.Level
	}
	if probe.Width > 0 && probe.Height > 0 {
		info.Width = probe.Width
		info.Height = probe.Height
	}
	if probe.FPSValid {
		info.FPS = probe.FPS
		info.FPSValid = true
	}
	if probe.BFrameChecked {
		info.HasBFrames = probe.HasBFrames
		info.BFrameChecked = true
	}

	return info
}

// Decided reports whether the B-frame verdict has been reached.
func (i *Inspector) Decided() bool { return i.probe.Decided() }

// parseAVCConfigurationRecord reads the SPS/PPS out of an AVCDecoderConfigurationRecord
// and feeds the SPS to the H.264 probe. The record's own profile/level bytes are
// kept as a fallback for a stream whose SPS cannot be parsed.
func (i *Inspector) parseAVCConfigurationRecord(record []byte) {
	if len(record) < 7 {
		return
	}
	if i.info.Profile == "" {
		i.info.Profile = avcProfileName(record[1])
	}
	if i.info.Level == "" {
		i.info.Level = formatLevel(record[3])
	}

	offset := 5
	numSPS := int(record[offset] & 0x1f)
	offset++
	for index := 0; index < numSPS && offset+2 <= len(record); index++ {
		length := int(record[offset])<<8 | int(record[offset+1])
		offset += 2
		if length <= 0 || offset+length > len(record) {
			return
		}
		i.probe.FeedNAL(record[offset : offset+length])
		offset += length
	}
}

// feedAVCCNALUs feeds every length-prefixed NAL unit of an AVC access unit. A
// payload that does not parse as length-prefixed is retried as Annex-B, so a
// publisher that sends start codes inside FLV is still inspected.
func (i *Inspector) feedAVCCNALUs(payload []byte) {
	if hasAnnexBStartCode(payload) {
		i.feedAnnexB(payload)

		return
	}

	offset := 0
	fed := 0
	for offset+4 <= len(payload) {
		length := int(payload[offset])<<24 | int(payload[offset+1])<<16 |
			int(payload[offset+2])<<8 | int(payload[offset+3])
		offset += 4
		if length <= 0 || offset+length > len(payload) {
			break
		}
		i.probe.FeedNAL(payload[offset : offset+length])
		offset += length
		fed++
	}
	if fed > 0 {
		return
	}
	i.feedAnnexB(payload)
}

// hasAnnexBStartCode reports whether the payload begins with a start code, which
// distinguishes an Annex-B access unit from a length-prefixed one.
func hasAnnexBStartCode(payload []byte) bool {
	if len(payload) >= 4 && payload[0] == 0 && payload[1] == 0 && payload[2] == 0 && payload[3] == 1 {
		return true
	}

	return len(payload) >= 3 && payload[0] == 0 && payload[1] == 0 && payload[2] == 1
}

// feedAnnexB feeds the NAL units of an Annex-B payload.
func (i *Inspector) feedAnnexB(payload []byte) {
	start := -1
	for index := 0; index+3 <= len(payload); index++ {
		if payload[index] != 0 || payload[index+1] != 0 {
			continue
		}
		headerLen := 0
		if payload[index+2] == 1 {
			headerLen = 3
		} else if index+4 <= len(payload) && payload[index+2] == 0 && payload[index+3] == 1 {
			headerLen = 4
		}
		if headerLen == 0 {
			continue
		}
		if start >= 0 {
			i.probe.FeedNAL(trimTrailingZeros(payload[start:index]))
		}
		start = index + headerLen
		index += headerLen - 1
	}
	if start >= 0 && start < len(payload) {
		i.probe.FeedNAL(trimTrailingZeros(payload[start:]))
	}
}

// trimTrailingZeros drops the zero padding an Annex-B writer may leave before
// the next start code.
func trimTrailingZeros(nal []byte) []byte {
	end := len(nal)
	for end > 0 && nal[end-1] == 0 {
		end--
	}

	return nal[:end]
}

// parseAudioSpecificConfig reads the AAC sample rate and channel count from an
// AudioSpecificConfig, and names the profile.
func (i *Inspector) parseAudioSpecificConfig(config []byte) {
	reader := bitReader{data: config}
	objectType := int(reader.read(5))
	if objectType == 31 {
		objectType = 32 + int(reader.read(6))
	}
	index := int(reader.read(4))
	sampleRate := 0
	if index == 15 {
		sampleRate = int(reader.read(24))
	} else if index < len(aacSampleRates) {
		sampleRate = aacSampleRates[index]
	}
	channels := int(reader.read(4))
	if reader.failed {
		return
	}
	if sampleRate > 0 {
		i.info.AudioSampleRate = sampleRate
	}
	if channels > 0 {
		i.info.AudioChannels = channels
	}
	i.info.AudioProfile = aacProfileName(objectType)
}

// aacSampleRates is the samplingFrequencyIndex table from ISO/IEC 14496-3.
var aacSampleRates = []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

// bitReader reads big-endian bit fields; past the end it reports failure and
// returns zeroes.
type bitReader struct {
	data   []byte
	bit    int
	failed bool
}

func (r *bitReader) read(count int) uint32 {
	var value uint32
	for index := 0; index < count; index++ {
		byteIndex := r.bit / 8
		if byteIndex >= len(r.data) {
			r.failed = true

			return value << uint(count-index)
		}
		bit := (r.data[byteIndex] >> uint(7-r.bit%8)) & 1
		value = value<<1 | uint32(bit)
		r.bit++
	}

	return value
}

// avcProfileName names the profile_idc byte of an AVCDecoderConfigurationRecord.
func avcProfileName(profileIDC byte) string {
	switch profileIDC {
	case 66:
		return "baseline"
	case 77:
		return "main"
	case 88:
		return "extended"
	case 100:
		return "high"
	case 110:
		return "high-10"
	case 122:
		return "high-4:2:2"
	case 244:
		return "high-4:4:4"
	default:
		return "unknown"
	}
}

// formatLevel renders a level_idc byte as "major.minor".
func formatLevel(levelIDC byte) string {
	if levelIDC == 0 {
		return ""
	}

	return fmt.Sprintf("%d.%d", levelIDC/10, levelIDC%10)
}

// aacProfileName names the AAC audio object type.
func aacProfileName(objectType int) string {
	switch objectType {
	case 2:
		return "LC"
	case 5:
		return "HE-AAC"
	case 29:
		return "HE-AACv2"
	case 1:
		return "Main"
	default:
		return ""
	}
}

// audioCodecName maps an FLV sound format to the display codec name.
func audioCodecName(format uint8) string {
	switch format {
	case 2, 14:
		return "mp3"
	case 0, 3:
		return "pcm"
	case 11:
		return "speex"
	default:
		return ""
	}
}
