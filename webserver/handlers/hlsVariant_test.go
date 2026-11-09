package handlers

import "testing"

func TestHLSVariantIndexFromPath(t *testing.T) {
	tests := []struct {
		relativePath string
		index        int
		ok           bool
	}{
		{relativePath: "stream.m3u8", ok: false},
		{relativePath: "0/stream.m3u8", index: 0, ok: true},
		{relativePath: "12/stream-abc-42.ts", index: 12, ok: true},
		{relativePath: "0", ok: false},
		{relativePath: "", ok: false},
		{relativePath: "quality/stream.m3u8", ok: false},
		{relativePath: "-1/stream.m3u8", ok: false},
		{relativePath: "0/../1/stream.m3u8", index: 0, ok: true},
	}

	for _, test := range tests {
		index, ok := hlsVariantIndexFromPath(test.relativePath)
		if ok != test.ok {
			t.Fatalf("hlsVariantIndexFromPath(%q) ok = %v, want %v", test.relativePath, ok, test.ok)
		}
		if ok && index != test.index {
			t.Fatalf("hlsVariantIndexFromPath(%q) index = %d, want %d", test.relativePath, index, test.index)
		}
	}
}
