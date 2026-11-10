package stream

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readAllWithTimeout(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	result := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		result <- data
	}()
	select {
	case data := <-result:
		return data
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading tee branch")
		return nil
	}
}

// TestIngestTeeFansOutIdenticalBytes proves both FFmpeg branches receive the
// same byte stream.
func TestIngestTeeFansOutIdenticalBytes(t *testing.T) {
	source := bytes.NewReader([]byte("mpeg-ts-bytes-for-hls-and-webrtc"))
	tee := newIngestTee(source)
	hls := tee.AddBranch("hls", true, nil)
	webrtc := tee.AddBranch("webrtc", false, nil)

	var wg sync.WaitGroup
	var hlsData, webrtcData []byte
	wg.Add(2)
	go func() { defer wg.Done(); hlsData = readAllWithTimeout(t, hls) }()
	go func() { defer wg.Done(); webrtcData = readAllWithTimeout(t, webrtc) }()

	go tee.Run()
	wg.Wait()

	if !bytes.Equal(hlsData, webrtcData) {
		t.Fatalf("branches received different bytes: hls=%q webrtc=%q", hlsData, webrtcData)
	}
	if len(hlsData) == 0 {
		t.Fatal("no data reached the branches")
	}
}

// TestIngestTeeOptionalBranchDoesNotStallCritical proves a stalled optional
// branch (simulated by not reading it) cannot block the critical branch.
func TestIngestTeeOptionalBranchDoesNotStallCritical(t *testing.T) {
	// Larger than the optional branch queue (256 * 32KiB) so it must overflow.
	payload := bytes.Repeat([]byte{0x47, 0x00, 0x10, 0x20}, 3*1024*1024) // 12MB
	source := bytes.NewReader(payload)
	tee := newIngestTee(source)
	hls := tee.AddBranch("hls", true, nil)

	overflowed := make(chan struct{}, 1)
	// Deliberately never read the optional branch.
	_ = tee.AddBranch("webrtc", false, func() { overflowed <- struct{}{} })

	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(hls)
		done <- data
	}()
	go tee.Run()

	select {
	case data := <-done:
		if len(data) != len(payload) {
			t.Fatalf("critical branch got %d bytes, want %d", len(data), len(payload))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("critical branch stalled because of the optional branch")
	}
	select {
	case <-overflowed:
		// Expected: the optional branch overflowed and was failed.
	case <-time.After(15 * time.Second):
		t.Fatal("optional branch was never failed on overflow")
	}
}

// TestIngestTeeCloseReleasesBranches proves Close closes every branch pipe.
func TestIngestTeeCloseReleasesBranches(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		// Source that blocks until closed.
		_, _ = io.Copy(io.Discard, pr)
	}()
	tee := newIngestTee(pr)
	branch := tee.AddBranch("hls", true, nil)
	go tee.Run()
	time.Sleep(50 * time.Millisecond)
	tee.Close()
	_ = pw.Close()

	if _, err := io.ReadAll(branch); err != nil {
		t.Fatalf("closing the tee should EOF the branch, got %v", err)
	}
}

// TestIngestTeeRemoveBranchStopsQueueing proves a replaced branch is unlinked:
// after RemoveBranch the fan-out must not queue into it any more, which is what
// keeps a supervised WebRTC-process restart from leaking memory and warnings.
func TestIngestTeeRemoveBranchStopsQueueing(t *testing.T) {
	source := bytes.NewReader([]byte("0123456789"))
	tee := newIngestTee(source)

	var overflowed atomic.Int32
	dead := tee.AddBranch("webrtc", false, func() { overflowed.Add(1) })
	live := tee.AddBranch("hls", true, nil)

	tee.mu.Lock()
	before := len(tee.branches)
	tee.mu.Unlock()
	if before != 2 {
		t.Fatalf("expected two branches, got %d", before)
	}

	tee.RemoveBranch("webrtc")

	tee.mu.Lock()
	after := len(tee.branches)
	tee.mu.Unlock()
	if after != 1 {
		t.Fatalf("expected the removed branch to be unlinked, got %d", after)
	}

	// The removed branch ends with an error rather than staying open.
	if _, err := io.ReadAll(dead); err == nil {
		t.Fatal("expected the removed branch to end with an error")
	}
	if overflowed.Load() != 0 {
		t.Fatal("removal must not be reported as an overflow")
	}

	go tee.Run()
	data := readAllWithTimeout(t, live)
	if string(data) != "0123456789" {
		t.Fatalf("remaining branch did not receive the stream: %q", data)
	}

	// Removing an unknown name is a no-op.
	tee.RemoveBranch("does-not-exist")
}
