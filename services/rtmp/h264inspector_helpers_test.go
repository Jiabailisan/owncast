package rtmp

// Test helpers keep the inspector state machine testable without constructing
// full AVC packets.
func (i *h264SourceInspector) ObserveConfigForTest(profileIDC, levelIDC byte) bool {
	i.changed = false
	i.observeDecoderConfig([]byte{1, profileIDC, 0, levelIDC, 0xff, 0xe0, 0})
	return i.changed
}

func (i *h264SourceInspector) ObserveNASliceForTest(isB bool) {
	i.changed = false
	payload := byte(0xc0)
	if isB {
		payload = 0xa0
	}
	i.observeNALUs([]byte{0, 0, 0, 2, 0x41, payload})
}
