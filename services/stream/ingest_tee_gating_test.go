package stream

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// TestTeeCloseBeforeStartFinishesRegisteredBranches covers the cancellation
// path: a session that ends after the branches were registered but before the
// pump started must not leave the branch readers blocked on a pipe nobody
// closes. Run's done-path finishes the branches, so Close returns and the
// readers see EOF.
func TestTeeCloseBeforeStartFinishesRegisteredBranches(t *testing.T) {
	tee := newIngestTee(&pacingReader{delay: 50 * time.Millisecond})
	hls := tee.AddBranch("hls", true, nil)
	webrtc := tee.AddLateBranch("webrtc", false, nil, "mpegts", nil)

	// Close before Start: the pump must exit through its done-path and finish
	// the registered branches.
	closeDone := make(chan struct{})
	go func() {
		tee.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(teeCloseTimeout + 3*time.Second):
		t.Fatal("Close did not return when it landed before Start")
	}

	// Both registered branches must be finished (EOF), not left blocked.
	for name, branch := range map[string]io.Reader{"hls": hls, "webrtc": webrtc} {
		done := make(chan error, 1)
		go func(b io.Reader, name string) {
			buf := make([]byte, 512)
			for {
				_, err := b.Read(buf)
				if err != nil {
					done <- err

					return
				}
			}
		}(branch, name)
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("branch %s returned a nil error instead of EOF", name)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("branch %s is still blocked on a pipe nobody closes", name)
		}
	}
}

// TestTeeStartWithoutHLSBranchKeepsHLSFromTheHead proves the HLS-critical path
// survives a WebRTC registration failure: with only the HLS branch registered,
// Start must deliver the stream from byte 0 to HLS, and the absent WebRTC
// branch must not stall or starve it.
func TestTeeStartWithoutHLSBranchKeepsHLSFromTheHead(t *testing.T) {
	payload := []byte("stream-head-and-body")
	// The source is delivered in small chunks, the way a live SRT source does.
	src := &chunkedReader{data: payload, chunk: 4}
	tee := newIngestTee(src)
	// Registration of the WebRTC branch "fails" (never happens): only HLS exists.
	hls := tee.AddBranch("hls", true, nil)

	tee.Start()

	got := readAllWithin(t, hls, len(payload))
	if !bytes.Equal(got, payload) {
		t.Fatalf("HLS received %q, want the whole stream from its head %q", got, payload)
	}
}

// TestTeeLateBranchAlignerRunsOnRestart covers the other direction of the same
// gate: a branch that joins after Start (the supervised restart) is aligned, so
// it receives a clean stream even though it missed the head.
func TestTeeLateBranchAlignerRunsOnRestart(t *testing.T) {
	// The source replays the read-ahead head instantly and paces the rest, and
	// keeps the stream alive long enough for a restart to actually join: a live
	// publisher is still sending when the supervisor re-registers the branch.
	stream := bytes.Join([][]byte{
		tsPacket(0, 1), tsPacket(0x100, 2), tsPacket(0x100, 3), tsPacket(0x100, 4), tsPacket(0x100, 5),
	}, nil)
	src := &loopingHeadSource{head: stream, skip: 40, firstPass: true}
	tee := newIngestTee(src)
	hls := tee.AddBranch("hls", true, nil)

	// The pump starts (the first WebRTC branch is registered in production
	// before this; here we only model the restart that joins late).
	tee.Start()
	time.Sleep(30 * time.Millisecond)
	late := tee.AddLateBranch("webrtc", false, nil, "mpegts", nil)

	// The late branch must receive an aligned stream: no torn packet at the head.
	buf := make([]byte, 188*8)
	deadline := time.Now().Add(2 * time.Second)
	aligned := false
	for time.Now().Before(deadline) {
		n, err := late.Read(buf)
		if n > 0 {
			aligned = buf[0] == 0x47
			break
		}
		if err != nil {
			break
		}
	}
	if !aligned {
		t.Fatal("the restarted branch received a misaligned stream head")
	}
	_ = hls
}

// loopingHeadSource replays the read-ahead head (data[:skip]) instantly, paces
// the rest of the first pass, and then keeps serving the stream from its sync
// boundary in a loop — a live publisher that is still sending when the
// supervised restart re-registers its branch.
type loopingHeadSource struct {
	head      []byte
	skip      int
	pos       int
	firstPass bool
}

func (s *loopingHeadSource) Read(p []byte) (int, error) {
	if s.firstPass {
		if s.pos >= len(s.head) {
			s.firstPass = false
			s.pos = s.skip // restart at the first whole packet
		} else {
			if s.pos > s.skip {
				time.Sleep(5 * time.Millisecond)
			}
			n := copy(p, s.head[s.pos:])
			s.pos += n

			return n, nil
		}
	}
	time.Sleep(5 * time.Millisecond)
	n := copy(p, s.head[s.skip:])
	s.pos = s.skip + n

	return n, nil
}
