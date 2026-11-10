package rtmp

import (
	"io"
	"net"
	"strconv"
	"time"

	"github.com/nareix/joy5/format/flv"
	"github.com/nareix/joy5/format/flv/flvio"
	"github.com/nareix/joy5/format/rtmp"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// Start binds the RTMP listener and runs the accept loop. Blocks until
// the listener returns. setStreamAsConnected is invoked once per
// authenticated connection; setBroadcaster is invoked when the inbound
// metadata tag arrives.
func (s *Service) Start(setStreamAsConnected func(ingest.Session), setBroadcaster func(models.Broadcaster)) {
	s.setStreamAsConnected = setStreamAsConnected
	s.setBroadcaster = setBroadcaster

	bindAddr := s.configRepository.GetRTMPBindAddress()
	port := s.configRepository.GetRTMPPortNumber()
	listenAddress := net.JoinHostPort(bindAddr, strconv.Itoa(port))
	srv := rtmp.NewServer()
	lis, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Fatal(err)
	}

	srv.LogEvent = func(c *rtmp.Conn, nc net.Conn, e int) {
		es := rtmp.EventString[e]
		log.Traceln("RTMP", nc.LocalAddr(), nc.RemoteAddr(), es)
	}

	srv.HandleConn = s.handleConn

	s.listenerMu.Lock()
	s.listener = lis
	s.listenerMu.Unlock()

	log.Tracef("RTMP server is listening for incoming stream on port: %d", port)

	for {
		nc, err := lis.Accept()
		if err != nil {
			if s.listenerClosed() {
				return
			}
			time.Sleep(time.Second)
			continue
		}
		go srv.HandleNetConn(nc)
	}
}

// Stop closes the RTMP listener, ending the accept loop. The current session is
// force-disconnected separately via Disconnect.
func (s *Service) Stop() {
	s.listenerMu.Lock()
	listener := s.listener
	s.listener = nil
	s.listenerMu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
}

func (s *Service) listenerClosed() bool {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	return s.listener == nil
}

// handleConn is fired when an inbound RTMP connection takes place.
func (s *Service) handleConn(c *rtmp.Conn, nc net.Conn) {
	c.LogTagEvent = func(isRead bool, t flvio.Tag) {
		if t.Type == flvio.TAG_AMF0 {
			log.Tracef("%+v\n", t.DebugFields())
			s.setCurrentBroadcasterInfo(t, nc.RemoteAddr().String())
		}
	}

	// Preserve the historical single-publisher behavior: check the shared
	// arbiter before authenticating so a second publisher cannot overtake an
	// existing stream.
	if s.sessions.Current() != nil {
		log.Errorln("stream already running; can not overtake an existing stream from", nc.RemoteAddr().String())
		_ = nc.Close()
		return
	}

	keyID, accessGranted := s.streamKeys.ValidateRTMPPath(c.URL.Path)
	if !accessGranted {
		log.Errorln("invalid streaming key; rejecting incoming stream from", nc.RemoteAddr().String())
		_ = nc.Close()
		return
	}

	rtmpOut, rtmpIn := io.Pipe()

	var session *session
	session = newSession(nc, rtmpOut, rtmpIn, keyID, func(error) {
		_ = nc.Close()
		_ = rtmpIn.Close()
		s.sessions.Release(session)
	})

	if err := s.sessions.TryAcquire(session); err != nil {
		log.Errorln("stream already running; can not overtake an existing stream from", nc.RemoteAddr().String())
		_ = nc.Close()
		_ = rtmpIn.Close()
		return
	}

	s.setCurrentSession(session)
	session.startStatsSampler()
	log.Infoln("Inbound stream connected from", nc.RemoteAddr().String())
	s.setStreamAsConnected(session)

	w := flv.NewMuxer(rtmpIn)

	for {
		// If we don't get a readable packet in 10 seconds give up and disconnect.
		if err := nc.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			log.Debugln(err)
		}

		pkt, err := c.ReadPacket()

		// Broadcaster disconnected.
		if err == io.EOF {
			s.handleDisconnect(session)
			return
		}

		// Read timeout. Disconnect.
		if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
			log.Debugln("Timeout reading the inbound stream from the broadcaster.  Assuming that they disconnected and ending the stream.")
			s.handleDisconnect(session)
			return
		}

		if err != nil {
			s.handleDisconnect(session)
			return
		}

		if err := w.WritePacket(pkt); err != nil {
			log.Errorln("unable to write rtmp packet", err)
			s.handleDisconnect(session)
			return
		}
	}
}

func (s *Service) handleDisconnect(session *session) {
	if session == nil || session.IsClosed() {
		return
	}

	log.Infoln("Inbound stream disconnected.")
	session.Close(nil)
}

// Disconnect will force disconnect the current inbound RTMP connection.
func (s *Service) Disconnect() {
	session := s.currentSessionOrNil()
	if session == nil {
		return
	}

	log.Traceln("Inbound stream disconnect requested.")
	session.Close(errStreamDisconnectedByAdmin)
}

func (s *Service) setCurrentSession(session *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentSession = session
}

func (s *Service) currentSessionOrNil() *session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentSession
}

// CurrentSession returns the live inbound session, if any.
func (s *Service) CurrentSession() ingest.Session {
	session := s.currentSessionOrNil()
	if session == nil {
		return nil
	}
	return session
}
