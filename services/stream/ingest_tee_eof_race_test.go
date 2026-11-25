package stream

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// eofSource ends immediately, so the pump reaches its EOF path (flush →
// finishAll → deferred close(stopped)) on the first read.
type eofSource struct{}

func (eofSource) Read([]byte) (int, error) { return 0, io.EOF }

// TestTeeBranchRegisteredDuringEOFShutdownGetsEOF targets the window between
// finishAll and close(stopped): a branch registered in that gap used to receive
// a live pipe nobody writes to or closes, blocking its reader forever. The
// check and the registration now share t.mu with finishAll, so the branch
// either is finished by the pump or gets an already-closed pipe.
func TestTeeBranchRegisteredDuringEOFShutdownGetsEOF(t *testing.T) {
	for attempt := 0; attempt < 200; attempt++ {
		tee := newIngestTee(eofSource{})
		hls := tee.AddBranch("hls", true, nil)
		tee.Start()
		go tee.Run()

		// Drain the HLS branch until EOF: the pump finishes the branches right
		// after, and the test registers a new branch inside that window.
		drained := make(chan struct{})
		go func() {
			buf := make([]byte, 512)
			for {
				if _, err := hls.Read(buf); err != nil {
					close(drained)

					return
				}
			}
		}()

		<-drained

		// Hammer registrations while the pump is between finishAll and
		// close(stopped). Every branch must see EOF promptly.
		var wg sync.WaitGroup
		results := make([]error, 16)
		for index := 0; index < 16; index++ {
			wg.Add(1)
			go func(slot int) {
				defer wg.Done()
				branch := tee.AddLateBranch("webrtc", false, nil, "mpegts", nil)
				if branch == nil {
					results[slot] = io.ErrNoProgress

					return
				}
				buf := make([]byte, 512)
				done := make(chan error, 1)
				go func() {
					for {
						_, err := branch.Read(buf)
						if err != nil {
							done <- err

							return
						}
					}
				}()
				select {
				case err := <-done:
					results[slot] = err
				case <-time.After(2 * time.Second):
					results[slot] = io.ErrNoProgress // still blocked: the race
				}
			}(index)
		}
		wg.Wait()

		for slot, err := range results {
			if err == nil || err == io.ErrNoProgress {
				t.Fatalf("attempt %d slot %d: a branch registered during shutdown did not see EOF (err=%v)", attempt, slot, err)
			}
		}

		// Clean up this attempt's pump before the next one.
		tee.Close()
	}
}

// TestTeeBranchRegisteredAfterCloseGetsEOF does the same against the Close
// path, where the timeout fallback calls finishAll directly.
func TestTeeBranchRegisteredAfterCloseGetsEOF(t *testing.T) {
	tee := newIngestTee(bytes.NewReader(nil))
	tee.AddBranch("hls", true, nil)
	tee.Start()
	go tee.Run()
	tee.Close()

	branch := tee.AddLateBranch("webrtc", false, nil, "mpegts", nil)
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 512)
		for {
			_, err := branch.Read(buf)
			if err != nil {
				done <- err

				return
			}
		}
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a branch registered after Close must see an error (EOF)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a branch registered after Close is still blocked on a live pipe")
	}
}
