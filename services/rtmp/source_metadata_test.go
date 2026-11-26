package rtmp

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/nareix/joy5/av"
	joy5flv "github.com/nareix/joy5/format/flv"
	"github.com/nareix/joy5/format/flv/flvio"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// newTestSession builds a session with a loopback connection and a discarded
// media pipe, so the metadata path can be exercised without a publisher.
func newTestSession(t *testing.T) *session {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	return newSession(server, reader, writer, "key:test", func(error) {})
}

// avcConfigRecord is an AVCDecoderConfigurationRecord for the x264 720p
// High@3.1 stream the FLV inspector tests use.
func avcConfigRecord() []byte {
	sps := []byte{
		0x67, 0x64, 0x00, 0x1f, 0xac, 0xd9, 0x40, 0x50, 0x05, 0xbb, 0x01, 0x10,
		0x00, 0x00, 0x03, 0x00, 0x10, 0x00, 0x00, 0x03, 0x03, 0xc0, 0xf1, 0x83, 0x19, 0x60,
	}
	pps := []byte{0x68, 0xef, 0xbc, 0xb0}

	record := []byte{1, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	record = append(record, byte(len(sps)>>8), byte(len(sps)))
	record = append(record, sps...)
	record = append(record, 1, byte(len(pps)>>8), byte(len(pps)))
	record = append(record, pps...)

	return record
}

// avccSlice wraps a coded-slice NAL in the AVCC length prefix.
func avccSlice(nal []byte) []byte {
	payload := []byte{byte(len(nal) >> 24), byte(len(nal) >> 16), byte(len(nal) >> 8), byte(len(nal))}

	return append(payload, nal...)
}

// TestRTMPMetadataMergesBitstreamOverDeclaration proves the bitstream wins over
// onMetaData where it has a value, and that the encoder name (which only the
// metadata carries) survives.
func TestRTMPMetadataMergesBitstreamOverDeclaration(t *testing.T) {
	session := newTestSession(t)

	session.setDeclaredDetails(models.InboundStreamDetails{
		Encoder:      "obs-output module (libobs version 30.0)",
		Width:        1920,
		Height:       1080,
		VideoCodec:   "H.264",
		AudioCodec:   "AAC",
		VideoBitrate: 9000,
		AudioBitrate: 160,
	})

	session.inspectPacket(av.Packet{Type: av.H264DecoderConfig, Data: avcConfigRecord()})
	// A B slice settles the verdict immediately.
	session.inspectPacket(av.Packet{Type: av.H264, Data: avccSlice([]byte{0x41, 0xA0})})

	broadcaster := session.Broadcaster()
	if broadcaster == nil {
		t.Fatal("the session published no broadcaster")
	}
	details := broadcaster.StreamDetails

	if details.Width != 1280 || details.Height != 720 {
		t.Fatalf("size = %dx%d, want the bitstream's 1280x720", details.Width, details.Height)
	}
	if details.VideoProfile != "high" || details.VideoLevel != "3.1" {
		t.Fatalf("profile/level = %q/%q, want high/3.1", details.VideoProfile, details.VideoLevel)
	}
	if details.HasBFrames == nil || !*details.HasBFrames {
		t.Fatalf("hasBFrames = %v, want true from the bitstream", details.HasBFrames)
	}
	if details.Encoder != "obs-output module (libobs version 30.0)" {
		t.Fatalf("encoder = %q, want the declared encoder name", details.Encoder)
	}
	if details.VideoBitrate != 9000 || details.VideoBitrateSource != models.VideoBitrateDeclared {
		t.Fatalf("bitrate = %d (%q), want the declared 9000 kbps before a measurement exists",
			details.VideoBitrate, details.VideoBitrateSource)
	}

	// A measured bitrate replaces the declaration and says so.
	for index := 0; index < 21; index++ {
		session.inspectPacket(av.Packet{
			Type: av.H264,
			Data: avccSlice([]byte{0x41, 0xC0}),
			Time: timeMillis(index * 100),
		})
	}
	details = session.Broadcaster().StreamDetails
	if details.VideoBitrateSource != models.VideoBitrateMeasured {
		t.Fatalf("bitrate source = %q, want measured", details.VideoBitrateSource)
	}
	if details.VideoBitrate >= 9000 {
		t.Fatalf("bitrate = %d, want the measured value to replace the declaration", details.VideoBitrate)
	}
}

// TestRTMPMetadataPublishesVerdictOnce proves SourceInfoReady closes exactly
// when the verdict is published, which is what the copy decision waits on.
func TestRTMPMetadataPublishesVerdictOnce(t *testing.T) {
	session := newTestSession(t)
	session.inspectPacket(av.Packet{Type: av.H264DecoderConfig, Data: avcConfigRecord()})

	select {
	case <-session.SourceInfoReady():
		t.Fatal("the verdict was announced before the inspector decided")
	default:
	}

	for index := 0; index < 60; index++ {
		session.inspectPacket(av.Packet{
			Type: av.H264,
			Data: avccSlice([]byte{0x41, 0xC0}),
			Time: timeMillis(index * 33),
		})
		session.publishSourceInfoIfDecided()
	}

	select {
	case <-session.SourceInfoReady():
	default:
		t.Fatal("the verdict was not announced after 60 non-B slices")
	}
	details := session.Broadcaster().StreamDetails
	if details.HasBFrames == nil || *details.HasBFrames {
		t.Fatalf("hasBFrames = %v, want false", details.HasBFrames)
	}
}

// TestRTMPMetadataKeepsVideoOnlyFlag proves a source without audio stays marked
// video-only, which the transcoder needs to avoid emitting audio outputs.
func TestRTMPMetadataKeepsVideoOnlyFlag(t *testing.T) {
	session := newTestSession(t)
	session.setDeclaredDetails(models.InboundStreamDetails{
		VideoCodec: "H.264",
		VideoOnly:  true,
	})
	session.inspectPacket(av.Packet{Type: av.H264DecoderConfig, Data: avcConfigRecord()})

	if details := session.Broadcaster().StreamDetails; !details.VideoOnly {
		t.Fatal("the video-only flag was lost")
	}
}

// timeMillis converts milliseconds to the duration av.Packet carries.
func timeMillis(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }

// TestRTMPContainerPrefixRebuildsTheDecoderConfiguration proves a process that
// joins the stream late is handed a valid FLV prefix: the file header plus the
// video and audio sequence-header tags. FLV sends that configuration exactly
// once, so without this the isolated WebRTC process has no extradata and cannot
// decode anything.
func TestRTMPContainerPrefixRebuildsTheDecoderConfiguration(t *testing.T) {
	session := newTestSession(t)
	videoConfig := avcConfigRecord()
	audioConfig := []byte{0x11, 0x90}

	if prefix := session.ContainerPrefix(); prefix != nil {
		t.Fatal("a session without a decoder configuration must return no prefix")
	}

	session.inspectPacket(av.Packet{Type: av.H264DecoderConfig, Data: videoConfig})
	session.inspectPacket(av.Packet{Type: av.AACDecoderConfig, Data: audioConfig})

	prefix := session.ContainerPrefix()
	if len(prefix) < 13 || string(prefix[0:3]) != "FLV" {
		t.Fatalf("prefix does not start with an FLV header: %x", prefix[:13])
	}

	// Video sequence header tag.
	offset := 13
	if prefix[offset] != 9 {
		t.Fatalf("first tag type is %d, want 9 (video)", prefix[offset])
	}
	videoSize := int(prefix[offset+1])<<16 | int(prefix[offset+2])<<8 | int(prefix[offset+3])
	if videoSize != 5+len(videoConfig) {
		t.Fatalf("video tag size is %d, want %d", videoSize, 5+len(videoConfig))
	}
	videoBody := prefix[offset+11 : offset+11+videoSize]
	if videoBody[0] != 0x17 || videoBody[1] != 0x00 {
		t.Fatalf("video tag is not an AVC sequence header: %x", videoBody[:2])
	}
	if !bytes.Equal(videoBody[5:], videoConfig) {
		t.Fatal("the video tag does not carry the cached decoder configuration")
	}
	offset += 11 + videoSize

	// The PreviousTagSize after the video tag must be 11 + its data size.
	previous := int(prefix[offset])<<24 | int(prefix[offset+1])<<16 | int(prefix[offset+2])<<8 | int(prefix[offset+3])
	if previous != 11+videoSize {
		t.Fatalf("PreviousTagSize is %d, want %d", previous, 11+videoSize)
	}
	offset += 4

	// Audio sequence header tag.
	if prefix[offset] != 8 {
		t.Fatalf("second tag type is %d, want 8 (audio)", prefix[offset])
	}
	audioSize := int(prefix[offset+1])<<16 | int(prefix[offset+2])<<8 | int(prefix[offset+3])
	if audioSize != 2+len(audioConfig) {
		t.Fatalf("audio tag size is %d, want %d", audioSize, 2+len(audioConfig))
	}
	audioBody := prefix[offset+11 : offset+11+audioSize]
	if audioBody[0] != 0xAF || audioBody[1] != 0x00 {
		t.Fatalf("audio tag is not an AAC sequence header: %x", audioBody[:2])
	}
}

// TestRTMPOnMetaDataAfterInspectionPreservesProbedFields guards against a late
// onMetaData tag overwriting probed HasBFrames/Profile/Level or being dropped
// because Encoder was omitted from the dedup key.
func TestRTMPOnMetaDataAfterInspectionPreservesProbedFields(t *testing.T) {
	sess := newTestSession(t)
	sess.inspectPacket(av.Packet{Type: av.H264DecoderConfig, Data: avcConfigRecord()})
	for index := 0; index < 60; index++ {
		sess.inspectPacket(av.Packet{
			Type: av.H264,
			Data: avccSlice([]byte{0x41, 0xC0}),
			Time: timeMillis(index * 33),
		})
	}

	before := sess.Broadcaster()
	if before == nil || before.StreamDetails.HasBFrames == nil || *before.StreamDetails.HasBFrames {
		t.Fatalf("expected probed HasBFrames=false before onMetaData, got %+v", before)
	}

	var published models.Broadcaster
	svc := &Service{
		setBroadcaster: func(_ ingest.Session, b models.Broadcaster) {
			published = b
		},
	}

	// Build a minimal AMF0 onMetaData tag matching the probed dimensions/codec
	// while adding an Encoder name.
	metaBuf := make([]byte, 512)
	n := flvio.FillAMF0Vals(metaBuf, []interface{}{
		"onMetaData",
		flvio.AMFMap{
			{K: "width", V: float64(1280)},
			{K: "height", V: float64(720)},
			{K: "videocodecid", V: float64(7)},
			{K: "encoder", V: "OBS Studio 31.0"},
		},
	})
	tag := flvio.Tag{Type: flvio.TAG_AMF0, Data: metaBuf[:n]}
	svc.setCurrentBroadcasterInfo(sess, tag, "127.0.0.1")

	after := sess.Broadcaster()
	if after == nil {
		t.Fatal("session broadcaster became nil")
	}
	details := after.StreamDetails
	if details.HasBFrames == nil || *details.HasBFrames {
		t.Fatalf("HasBFrames = %v, want probed false preserved", details.HasBFrames)
	}
	if details.VideoProfile != "high" || details.VideoLevel != "3.1" {
		t.Fatalf("profile/level = %q/%q, want high/3.1 preserved", details.VideoProfile, details.VideoLevel)
	}
	if details.Encoder != "OBS Studio 31.0" {
		t.Fatalf("encoder = %q, want OBS Studio 31.0 merged", details.Encoder)
	}
	if published.StreamDetails.HasBFrames == nil || *published.StreamDetails.HasBFrames {
		t.Fatalf("published broadcaster lost probed HasBFrames: %+v", published.StreamDetails)
	}
}

func TestRTMPContainerPrefixVideoOnlySetsVideoFlagOnly(t *testing.T) {
	sess := newTestSession(t)
	sess.inspectPacket(av.Packet{Type: av.H264DecoderConfig, Data: avcConfigRecord()})

	videoOnly := sess.ContainerPrefix()
	if len(videoOnly) < 13 {
		t.Fatalf("expected FLV prefix, got %d bytes", len(videoOnly))
	}
	if videoOnly[4] != 0x01 {
		t.Fatalf("video-only FLV type flags = 0x%02x, want 0x01", videoOnly[4])
	}

	sess.inspectPacket(av.Packet{Type: av.AACDecoderConfig, Data: []byte{0x11, 0x90}})
	videoAndAudio := sess.ContainerPrefix()
	if videoAndAudio[4] != 0x05 {
		t.Fatalf("video+audio FLV type flags = 0x%02x, want 0x05", videoAndAudio[4])
	}
}

// TestWriteFLVPacketUsesStereoHeaderForAAC guards against joy5 emitting 0xAE
// (mono) when pkt.AAC is nil, which mismatches ContainerPrefix()'s 0xAF and
// triggers FFmpeg PARAM_CHANGE errors on the native AAC decoder.
func TestWriteFLVPacketUsesStereoHeaderForAAC(t *testing.T) {
	var buf bytes.Buffer
	muxer := joy5flv.NewMuxer(&buf)

	if err := writeFLVPacket(muxer, av.Packet{
		Type: av.AACDecoderConfig,
		Data: []byte{0x11, 0x90},
	}); err != nil {
		t.Fatalf("writeFLVPacket(AACDecoderConfig) failed: %v", err)
	}
	if err := writeFLVPacket(muxer, av.Packet{
		Type: av.AAC,
		Time: 21 * time.Millisecond,
		Data: []byte{0x21, 0x10, 0x04, 0x60},
	}); err != nil {
		t.Fatalf("writeFLVPacket(AAC) failed: %v", err)
	}

	raw := buf.Bytes()
	if len(raw) < 13 || string(raw[:3]) != "FLV" {
		t.Fatalf("output missing FLV header: %x", raw)
	}

	offset := 13
	for i, wantPacketType := range []byte{0x00, 0x01} {
		if offset+11 > len(raw) {
			t.Fatalf("tag %d truncated at offset %d", i, offset)
		}
		if raw[offset] != 8 {
			t.Fatalf("tag %d type = %d, want 8 (audio)", i, raw[offset])
		}
		dataSize := int(raw[offset+1])<<16 | int(raw[offset+2])<<8 | int(raw[offset+3])
		body := raw[offset+11 : offset+11+dataSize]
		if len(body) < 2 {
			t.Fatalf("tag %d audio body too short: %d", i, len(body))
		}
		if body[0] != 0xAF {
			t.Fatalf("tag %d FLV audio header byte = 0x%02x, want 0xAF (AAC stereo per FLV spec)", i, body[0])
		}
		if body[1] != wantPacketType {
			t.Fatalf("tag %d AAC packet type = 0x%02x, want 0x%02x", i, body[1], wantPacketType)
		}
		offset += 11 + dataSize + 4
	}
}

// TestRTMPBroadcasterTimeStaysAnchoredToSessionStart verifies that periodic
// bitrate/metadata refreshes never overwrite broadcaster.Time with time.Now(),
// which previously kept the admin stream duration stuck at 00:00.
func TestRTMPBroadcasterTimeStaysAnchoredToSessionStart(t *testing.T) {
	sess := newTestSession(t)
	startedAt := sess.StartedAt()

	sess.inspectPacket(av.Packet{Type: av.H264DecoderConfig, Data: avcConfigRecord()})
	sess.inspectPacket(av.Packet{Type: av.H264, Data: avccSlice([]byte{0x41, 0xA0})})
	first := sess.Broadcaster()
	if first == nil {
		t.Fatal("expected broadcaster after initial probe")
	}
	if !first.Time.Equal(startedAt) {
		t.Fatalf("initial broadcaster.Time = %v, want session StartedAt %v", first.Time, startedAt)
	}

	time.Sleep(15 * time.Millisecond)
	for index := 0; index < 21; index++ {
		sess.inspectPacket(av.Packet{
			Type: av.H264,
			Data: avccSlice([]byte{0x41, 0xC0}),
			Time: timeMillis(index * 100),
		})
	}
	updated := sess.Broadcaster()
	if updated == nil {
		t.Fatal("expected broadcaster after bitrate update")
	}
	if !updated.Time.Equal(startedAt) {
		t.Fatalf("refreshed broadcaster.Time = %v, want immutable session StartedAt %v", updated.Time, startedAt)
	}
}
