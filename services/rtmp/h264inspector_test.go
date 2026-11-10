package rtmp

import "testing"

func TestH264SliceTypeDetection(t *testing.T) {
	if !sliceTypeIsB([]byte{0x41, 0xA0}) {
		t.Fatal("expected B slice detection")
	}
	if sliceTypeIsB([]byte{0x41, 0xC0}) {
		t.Fatal("did not expect B slice detection")
	}
}

func TestH264InspectorReadsDecoderConfig(t *testing.T) {
	inspector := newH264SourceInspector()
	changed := inspector.ObserveConfigForTest(77, 51)
	if !changed {
		t.Fatal("expected decoder config to change source details")
	}
	profile, level, _, _, bframes := inspector.SourceDetails()
	if profile != "main" || level != "5.1" {
		t.Fatalf("unexpected profile/level: %q %q", profile, level)
	}
	if bframes != nil {
		t.Fatal("B-frame verdict must stay unknown before slices are inspected")
	}
	for index := 0; index < slicesForRTMPBFrameDecision; index++ {
		inspector.ObserveNASliceForTest(false)
	}
	_, _, _, _, bframes = inspector.SourceDetails()
	if bframes == nil || *bframes {
		t.Fatal("expected known no-B-frame verdict")
	}
	bframeInspector := newH264SourceInspector()
	bframeInspector.ObserveNASliceForTest(true)
	_, _, _, _, bframes = bframeInspector.SourceDetails()
	if bframes == nil || !*bframes {
		t.Fatal("expected B-frame verdict")
	}
}
