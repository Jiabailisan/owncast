package transcoder

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
)

func TestFFmpegNvencCommand(t *testing.T) {
	latencyLevel := models.GetLatencyLevel(2)
	codec := NvencCodec{}

	transcoder := new(Transcoder)
	transcoder.cfg = &config.Config{LogDirectory: "data/logs"}
	transcoder.ffmpegPath = filepath.Join("fake", "path", "ffmpeg")
	transcoder.SetInput("fakecontent.flv")
	transcoder.SetOutputPath("fakeOutput")
	transcoder.SetIdentifier("jdoieGg")
	transcoder.SetInternalHTTPPort("8123")
	transcoder.SetCodec(codec.Name())
	transcoder.currentLatencyLevel = latencyLevel

	variant := HLSVariant{}
	variant.videoBitrate = 1200
	variant.isAudioPassthrough = true
	variant.SetVideoFramerate(30)
	variant.SetCPUUsageLevel(2)
	transcoder.AddVariant(variant)

	variant2 := HLSVariant{}
	variant2.videoBitrate = 3500
	variant2.isAudioPassthrough = true
	variant2.SetVideoFramerate(24)
	variant2.SetCPUUsageLevel(4)
	transcoder.AddVariant(variant2)

	variant3 := HLSVariant{}
	variant3.isAudioPassthrough = true
	variant3.isVideoPassthrough = true
	transcoder.AddVariant(variant3)

	cmd := transcoder.GetString()

	expected := transcoder.ffmpegPath + ` -hide_banner -nostats -loglevel level+info -progress pipe:1 -hwaccel cuda -fflags +genpts -flags +cgop -i fakecontent.flv -map v:0 -c:v:0 h264_nvenc -b:v:0 1008k -minrate:v:0 504k -maxrate:v:0 1512k -g:v:0 90 -keyint_min:v:0 90 -r:v:0 30 -tune:v:0 ll -map a:0? -c:a:0 copy -preset p3 -map v:0 -c:v:1 h264_nvenc -b:v:1 3308k -minrate:v:1 1654k -maxrate:v:1 4962k -g:v:1 72 -keyint_min:v:1 72 -r:v:1 24 -tune:v:1 ll -map a:0? -c:a:1 copy -preset p5 -map v:0 -c:v:2 copy -map a:0? -c:a:2 copy -preset p1 -var_stream_map v:0,a:0 v:1,a:1 v:2,a:2 -f hls -hls_time 3 -hls_list_size 10 -hls_flags program_date_time+independent_segments+omit_endlist -segment_format_options mpegts_flags=mpegts_copyts=1 -pix_fmt yuv420p -master_pl_name stream.m3u8 -hls_segment_filename http://127.0.0.1:8123/%v/stream-jdoieGg-%d.ts -max_muxing_queue_size 400 -method PUT http://127.0.0.1:8123/%v/stream.m3u8`

	if cmd != expected {
		t.Errorf("ffmpeg command does not match expected.\nGot %s\n, want: %s", cmd, expected)
	}
}

func TestFFmpegNvencCudaScaleFormatAndSWFallback(t *testing.T) {
	latencyLevel := models.GetLatencyLevel(2)

	tr := new(Transcoder)
	tr.cfg = &config.Config{LogDirectory: "data/logs"}
	tr.ffmpegPath = filepath.Join("fake", "path", "ffmpeg")
	tr.SetInput("fakecontent.flv")
	tr.SetOutputPath("fakeOutput")
	tr.SetIdentifier("cudaTest")
	tr.SetInternalHTTPPort("8123")
	tr.codec = &NvencCodec{cudaScale: true}
	tr.currentLatencyLevel = latencyLevel

	scaled := HLSVariant{}
	scaled.videoBitrate = 2500
	scaled.isAudioPassthrough = true
	scaled.SetVideoFramerate(60)
	scaled.SetVideoScalingHeight(720)
	tr.AddVariant(scaled)

	unscaled := HLSVariant{}
	unscaled.videoBitrate = 4000
	unscaled.isAudioPassthrough = true
	unscaled.SetVideoFramerate(60)
	tr.AddVariant(unscaled)

	cmd := tr.GetString()
	if !strings.Contains(cmd, "scale_cuda=-2:720:format=nv12") {
		t.Fatalf("expected scale_cuda with format=nv12, got: %s", cmd)
	}
	if !strings.Contains(cmd, "scale_cuda=format=nv12") {
		t.Fatalf("expected unscaled CUDA transcode leg to enforce format=nv12, got: %s", cmd)
	}

	// Non-H.264/HEVC source (e.g. AV1) must downgrade cudaScale to software scale.
	tr.SetSourceCodec("av1")
	cmdAV1 := tr.GetString()
	if strings.Contains(cmdAV1, "scale_cuda") {
		t.Fatalf("expected AV1 input to disable scale_cuda, got: %s", cmdAV1)
	}
	if !strings.Contains(cmdAV1, "scale=-2:720") {
		t.Fatalf("expected software scale fallback for AV1 input, got: %s", cmdAV1)
	}
}
