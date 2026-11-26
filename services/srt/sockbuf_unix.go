//go:build !windows

package srt

import (
	"syscall"

	log "github.com/sirupsen/logrus"
)

const (
	// srtUDPRecvBufferBytes requests an 8 MiB kernel UDP receive buffer on the
	// SRT listener socket so keyframe microbursts or brief Go GC / disk I/O
	// pauses at high ingest bitrates do not overflow Linux's small default
	// net.core.rmem_default (often 212 KiB). The kernel silently clamps the
	// request to net.core.rmem_max.
	srtUDPRecvBufferBytes = 8 << 20
	// srtUDPSendBufferBytes requests a 4 MiB kernel UDP send buffer for SRT
	// ACK/NAK control traffic.
	srtUDPSendBufferBytes = 4 << 20
)

// srtSocketBufferControl configures enlarged UDP socket buffers via gosrt's
// ListenerControl hook. Failures are logged and ignored so restricted
// containers never fail to bind the SRT listener.
func srtSocketBufferControl(_, _ string, c syscall.RawConn) error {
	_ = c.Control(func(fd uintptr) {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, srtUDPRecvBufferBytes); err != nil {
			log.WithError(err).Debug("srt: could not enlarge UDP SO_RCVBUF")
		} else if actual, err := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF); err == nil {
			log.WithField("so_rcvbuf_bytes", actual).Info("srt: configured UDP receive buffer")
		}
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, srtUDPSendBufferBytes); err != nil {
			log.WithError(err).Debug("srt: could not enlarge UDP SO_SNDBUF")
		}
	})
	return nil
}
