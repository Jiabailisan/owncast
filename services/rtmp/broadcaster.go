package rtmp

import (
	"time"

	"github.com/nareix/joy5/format/flv/flvio"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
)

func (s *Service) setCurrentBroadcasterInfo(t flvio.Tag, remoteAddr string) {
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

	// Attach the metadata to the session as well as the legacy callback so
	// the protocol-neutral status layer can read it from the session.
	if session := s.currentSessionOrNil(); session != nil {
		session.SetBroadcaster(&broadcaster)
	}

	s.setBroadcaster(broadcaster)
}
