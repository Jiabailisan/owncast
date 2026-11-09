package mpegts

import (
	"fmt"
	"strings"
)

// Media-time bitrate estimation and AAC/ADTS details.
//
// Bitrates are measured from PES payload bytes and presentation timestamps, not
// wall-clock arrival time, so a file/next-day replay and a live publish produce
// the same numbers. Counted bytes are the PES payload only: TS packet headers,
// adaptation fields and PES headers are excluded. Codec framing overhead inside
// the ES payload (Annex-B start codes, ADTS headers, NAL headers) remains
// included; for an AAC stream that is about 7 bytes per 1024-sample frame
// (roughly 0.3% at 128 kbps), and it is negligible for the bitrate display.
const (
	// bitrateWindowTicks is the rolling media-time window (3 s at 90 kHz).
	bitrateWindowTicks = int64(3 * 90000)
	// minBitrateSpanTicks is the minimum observed media-time span before a
	// bitrate is reported; below it the value stays invalid (N/A).
	minBitrateSpanTicks = int64(90000) // 1 s
	// ptsModulus is the 33-bit PTS wrap space.
	ptsModulus = int64(1) << 33
	// pesHeaderMinLength is the minimum PES header size before optional fields.
	pesHeaderMinLength = 9
	// adtsHeaderLength is the fixed AAC ADTS header length without CRC.
	adtsHeaderLength = 7
	// maxAudioESBuffer caps the retained audio elementary-stream tail.
	maxAudioESBuffer = 1 << 20
)

// sourceBitrateTracker accumulates PES payload sample sizes with their
// presentation timestamps and reports a rolling bitrate over media time.
// The zero value is ready to use.
type sourceBitrateTracker struct {
	samples []bitrateSample
	maxPTS  int64
	primed  bool
}

type bitrateSample struct {
	pts   int64
	bytes int
}

// add records one PES payload sample. Samples without a valid PTS are ignored
// by the caller, so the tracker only sees timed payload.
func (b *sourceBitrateTracker) add(pts int64, payloadBytes int) {
	if payloadBytes <= 0 || pts < 0 {
		return
	}
	b.samples = append(b.samples, bitrateSample{pts: pts, bytes: payloadBytes})
	if !b.primed || forwardPTS(b.maxPTS, pts) < ptsModulus/2 {
		b.maxPTS = pts
		b.primed = true
	}

	// Bound memory: retain at most two windows of samples.
	for len(b.samples) > 1 && b.ageTicks(b.samples[0].pts) > 2*bitrateWindowTicks {
		b.samples = b.samples[1:]
	}
}

// rateKbps returns the rolling bitrate in kilobits per second and whether the
// window holds enough media time for the value to be meaningful.
func (b *sourceBitrateTracker) rateKbps() (float64, bool) {
	if !b.primed || len(b.samples) < 2 {
		return 0, false
	}

	var totalBytes int64
	var span int64
	for _, sample := range b.samples {
		age := b.ageTicks(sample.pts)
		if age > bitrateWindowTicks {
			continue
		}
		totalBytes += int64(sample.bytes)
		if age > span {
			span = age
		}
	}
	if span < minBitrateSpanTicks || totalBytes == 0 {
		return 0, false
	}

	// bytes * 8 bits / (span ticks / 90000 ticks per second) / 1000 bits per kbit
	kbps := float64(totalBytes) * 8 * 90000 / float64(span) / 1000
	return kbps, true
}

// ageTicks reports how far pts is behind the newest observed timestamp.
func (b *sourceBitrateTracker) ageTicks(pts int64) int64 {
	return forwardPTS(pts, b.maxPTS)
}

// forwardPTS returns the wrapped forward distance from one 33-bit PTS to
// another.
func forwardPTS(from, to int64) int64 {
	distance := to - from
	if distance < 0 {
		distance += ptsModulus
	}
	return distance
}

// parsePESPayload returns the elementary-stream bytes carried by one TS packet
// payload and, when the packet starts a PES, its PTS.
func parsePESPayload(payload []byte, payloadStart bool) ([]byte, int64, bool) {
	if !payloadStart {
		return payload, 0, false
	}
	if len(payload) < pesHeaderMinLength || payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
		return nil, 0, false
	}

	headerDataLength := int(payload[8])
	esStart := pesHeaderMinLength + headerDataLength
	if esStart > len(payload) {
		return nil, 0, false
	}

	flags := payload[7]
	var pts int64
	hasPTS := false
	if flags&0x80 != 0 && len(payload) >= 14 {
		bytes := payload[9:14]
		pts = int64(bytes[0]>>1&0x07)<<30 |
			int64(bytes[1])<<22 |
			int64(bytes[2]>>1)<<15 |
			int64(bytes[3])<<7 |
			int64(bytes[4]>>1)
		hasPTS = true
	}

	return payload[esStart:], pts, hasPTS
}

// aacHeader is the subset of an AAC ADTS header we display.
type aacHeader struct {
	profileID   int
	sampleRate  int
	channels    int
	frameLength int
}

var aacSampleRates = [...]int{
	96000, 88200, 64000, 48000, 44100, 32000,
	24000, 22050, 16000, 12000, 11025, 8000, 7350,
}

// parseADTSHeader decodes one AAC ADTS header.
func parseADTSHeader(data []byte) (aacHeader, bool) {
	if len(data) < adtsHeaderLength {
		return aacHeader{}, false
	}
	if data[0] != 0xFF || data[1]&0xF6 != 0xF0 {
		return aacHeader{}, false
	}

	profileID := int(data[2]>>6) & 0x03
	sampleRateIndex := int(data[2]>>2) & 0x0F
	if sampleRateIndex >= len(aacSampleRates) {
		return aacHeader{}, false
	}
	channels := int(data[2]&0x01)<<2 | int(data[3]>>6)&0x03
	frameLength := int(data[3]&0x03)<<11 | int(data[4])<<3 | int(data[5]>>5)&0x07
	if frameLength < adtsHeaderLength {
		return aacHeader{}, false
	}

	return aacHeader{
		profileID:   profileID,
		sampleRate:  aacSampleRates[sampleRateIndex],
		channels:    channels,
		frameLength: frameLength,
	}, true
}

// profileName returns the AAC object-type name used by the audio profile field
// (matches the ffprobe style, e.g. "LC").
func (h aacHeader) profileName() string {
	switch h.profileID {
	case 0:
		return "Main"
	case 1:
		return "LC"
	case 2:
		return "SSR"
	case 3:
		return "LTP"
	default:
		return valueUnknown
	}
}

// detail returns a compact human-readable description, e.g.
// "AAC-LC 48kHz stereo".
func (h aacHeader) detail() string {
	parts := []string{"AAC-" + h.profileName()}
	if h.sampleRate > 0 {
		parts = append(parts, fmt.Sprintf("%dkHz", h.sampleRate/1000))
	}
	if label := aacChannelLabel(h.channels); label != "" {
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

func aacChannelLabel(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 3:
		return "3 channels"
	case 4:
		return "4 channels"
	case 5:
		return "5 channels"
	case 6:
		return "5.1"
	case 7:
		return "7.1"
	default:
		return ""
	}
}

// pesAccumulator tracks the total elementary-stream bytes of the current PES
// packet until the next packet start, when the sample is handed to the bitrate
// tracker together with the PES PTS.
type pesAccumulator struct {
	bytes  int
	pts    int64
	hasPTS bool
}

// addPacket feeds one TS payload into the accumulator. It returns the completed
// previous PES sample (pts, bytes) when this packet starts a new PES.
func (a *pesAccumulator) addPacket(esBytes int, pts int64, hasPTS, payloadStart bool) (int64, int, bool) {
	if payloadStart {
		flushPTS, flushBytes, flush := a.pts, a.bytes, a.hasPTS && a.bytes > 0
		a.bytes = 0
		a.pts = 0
		a.hasPTS = false
		if esBytes > 0 {
			a.bytes = esBytes
			a.pts = pts
			a.hasPTS = hasPTS
		}
		return flushPTS, flushBytes, flush
	}
	a.bytes += esBytes
	return 0, 0, false
}

// consumeVideoPayload appends one TS packet of video payload to the ES buffer,
// feeds the H.264 inspector and updates the rolling video bitrate.
func (i *Inspector) consumeVideoPayload(payload []byte, payloadStart bool) {
	es, pts, hasPTS := parsePESPayload(payload, payloadStart)
	if len(es) == 0 {
		return
	}

	i.es = append(i.es, es...)
	if len(i.es) > maxESBuffer {
		i.es = append([]byte(nil), i.es[len(i.es)-maxESBuffer/2:]...)
	}
	i.scanNALs()

	if flushPTS, flushBytes, flush := i.videoPES.addPacket(len(es), pts, hasPTS, payloadStart); flush {
		i.recordVideoBitrate(flushPTS, flushBytes)
	}
}

// recordVideoBitrate adds one completed PES sample to the video bitrate
// tracker and republishes the rolling value.
func (i *Inspector) recordVideoBitrate(pts int64, payloadBytes int) {
	i.videoRate.add(pts, payloadBytes)
	if kbps, ok := i.videoRate.rateKbps(); ok {
		i.info.VideoBitrateKbps = kbps
		i.info.VideoBitrateValid = true
	}
}

// consumeAudioPayload appends one TS packet of audio payload, parses AAC ADTS
// headers for the codec details and updates the rolling audio bitrate.
func (i *Inspector) consumeAudioPayload(payload []byte, payloadStart bool) {
	es, pts, hasPTS := parsePESPayload(payload, payloadStart)
	if len(es) == 0 {
		return
	}

	i.audioES = append(i.audioES, es...)
	if len(i.audioES) > maxAudioESBuffer {
		i.audioES = append([]byte(nil), i.audioES[len(i.audioES)-maxAudioESBuffer/2:]...)
	}
	i.parseADTSFrames()

	if flushPTS, flushBytes, flush := i.audioPES.addPacket(len(es), pts, hasPTS, payloadStart); flush {
		i.audioRate.add(flushPTS, flushBytes)
		if kbps, ok := i.audioRate.rateKbps(); ok {
			i.info.AudioBitrateKbps = kbps
			i.info.AudioBitrateValid = true
		}
	}
}

// parseADTSFrames walks the retained audio bytes, records the AAC details from
// the first complete ADTS frame and drops consumed frames from the buffer.
func (i *Inspector) parseADTSFrames() {
	buffer := i.audioES
	consumed := 0
	for len(buffer)-consumed >= adtsHeaderLength {
		if buffer[consumed] != 0xFF || buffer[consumed+1]&0xF6 != 0xF0 {
			consumed++
			continue
		}
		header, ok := parseADTSHeader(buffer[consumed:])
		if !ok {
			consumed++
			continue
		}
		if len(buffer)-consumed < header.frameLength {
			break
		}
		if !i.audioDetailsSet {
			i.info.AudioProfile = header.profileName()
			i.info.AudioSampleRate = header.sampleRate
			i.info.AudioChannels = header.channels
			i.info.AudioCodecDetail = header.detail()
			i.audioDetailsSet = true
		}
		consumed += header.frameLength
	}

	if consumed > 0 {
		i.audioES = append([]byte(nil), buffer[consumed:]...)
	}
}
