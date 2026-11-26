//go:build windows

package srt

import (
	"syscall"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

const (
	srtUDPRecvBufferBytes = 8 << 20
	srtUDPSendBufferBytes = 4 << 20
)

func srtSocketBufferControl(_, _ string, c syscall.RawConn) error {
	_ = c.Control(func(fd uintptr) {
		if err := windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_RCVBUF, srtUDPRecvBufferBytes); err != nil {
			log.WithError(err).Debug("srt: could not enlarge UDP SO_RCVBUF")
		}
		if err := windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_SNDBUF, srtUDPSendBufferBytes); err != nil {
			log.WithError(err).Debug("srt: could not enlarge UDP SO_SNDBUF")
		}
	})
	return nil
}
