package transcoder

import (
	"reflect"
	"testing"
)

func TestThumbnailDecodeFlags(t *testing.T) {
	if got := thumbnailDecodeFlags("h264_nvenc"); !reflect.DeepEqual(got, []string{"-hwaccel", "cuda"}) {
		t.Fatalf("nvenc decode flags = %#v, want -hwaccel cuda", got)
	}
	if got := thumbnailDecodeFlags("libx264"); got != nil {
		t.Fatalf("software codec decode flags = %#v, want nil", got)
	}
	if got := thumbnailDecodeFlags("h264_qsv"); got != nil {
		t.Fatalf("qsv decode flags = %#v, want nil until hwdownload handling exists", got)
	}
}

func TestThumbnailCommandFlagsUseConfiguredHardwareDecoder(t *testing.T) {
	flags := thumbnailCommandFlags("in.ts", "out.jpg", "h264_nvenc")
	hwaccel := indexOf(flags, "-hwaccel")
	input := indexOf(flags, "-i")
	if hwaccel < 0 || input < 0 || hwaccel > input {
		t.Fatalf("expected -hwaccel before -i, got %#v", flags)
	}
	if flags[hwaccel] != "-hwaccel" || flags[hwaccel+1] != "cuda" {
		t.Fatalf("decode flags not placed before input: %#v", flags)
	}
	if indexOf(flags, "-vframes") < 0 || flags[len(flags)-1] != "out.jpg" {
		t.Fatalf("still-frame output missing: %#v", flags)
	}

	gif := animatedGifCommandFlags("in.ts", "out.gif", "h264_nvenc")
	if indexOf(gif, "-hwaccel") < 0 || indexOf(gif, "-hwaccel") > indexOf(gif, "-i") {
		t.Fatalf("expected hardware decode before input for gif: %#v", gif)
	}
	if indexOf(gif, "-filter_complex") < 0 || gif[len(gif)-1] != "out.gif" {
		t.Fatalf("gif filter/output missing: %#v", gif)
	}

	software := thumbnailCommandFlags("in.ts", "out.jpg", "libx264")
	if indexOf(software, "-hwaccel") >= 0 {
		t.Fatalf("software codec should not add hwaccel: %#v", software)
	}
}

func indexOf(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}
