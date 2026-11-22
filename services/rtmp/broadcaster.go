package rtmp

import (
	"time"

	"github.com/nareix/joy5/format/flv/flvio"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
)

func (s *Service) setCurrentBroadcasterInfo(session *session, t flvio.Tag, remoteAddr string) {
	data, err := getInboundDetailsFromMetadata(t.DebugFields())
	if err != nil {
		log.Traceln("Unable to parse inbound broadcaster details:", err)
	}

	broadcaster := models.Broadcaster{
		RemoteAddr: remoteAddr,
		Time:       time.Now(),
		StreamDetails: models.InboundStreamDetails{
			Width:          data.Width,
			Height:         data.Height,
			VideoBitrate:   int(data.VideoBitrate),
			VideoCodec:     getVideoCodec(data.VideoCodec),
			VideoFramerate: data.VideoFramerate,
			AudioBitrate:   int(data.AudioBitrate),
			AudioCodec:     getAudioCodec(data.AudioCodec),
			Encoder:        data.Encoder,
			VideoOnly:      data.AudioCodec == nil,
		},
	}

	// The session is the source of truth: it is this connection's own session, so the
	// metadata can never land on a session a later connection installed.
	if session != nil {
		session.SetBroadcaster(&broadcaster)
	}

	s.setBroadcaster(session, broadcaster)
}
