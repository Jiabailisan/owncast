package ingest

import (
	"testing"
)

func TestInputFormats(t *testing.T) {
	flv := FLVInputFormat()
	if flv.Name != "flv" || len(flv.ExtraArgs) != 0 {
		t.Errorf("FLV format must not add argv (golden compatibility), got %+v", flv)
	}

	mpegts := MPEGTSInputFormat()
	if mpegts.Name != "mpegts" {
		t.Errorf("expected mpegts name, got %q", mpegts.Name)
	}
	if len(mpegts.ExtraArgs) != 2 || mpegts.ExtraArgs[0] != "-f" || mpegts.ExtraArgs[1] != "mpegts" {
		t.Errorf("unexpected MPEG-TS args: %v", mpegts.ExtraArgs)
	}
}

func TestSessionBaseCloseIsIdempotentAndClosesDone(t *testing.T) {
	closed := 0
	var s *fakeSession
	s = &fakeSession{SessionBase: NewSessionBase("id", ProtocolSRT, nil, "key", MPEGTSInputFormat(), func(error) { closed++ })}

	if s.Protocol() != ProtocolSRT {
		t.Errorf("unexpected protocol %q", s.Protocol())
	}
	if s.InputFormat().Name != "mpegts" {
		t.Errorf("unexpected input format %q", s.InputFormat().Name)
	}
	if s.IsClosed() {
		t.Error("session must start open")
	}

	s.Close(nil)
	s.Close(nil)
	if closed != 1 {
		t.Errorf("onClose must run exactly once, ran %d times", closed)
	}
	if !s.IsClosed() {
		t.Error("session must report closed")
	}
	select {
	case <-s.Done():
	default:
		t.Error("Done must be closed")
	}
}
