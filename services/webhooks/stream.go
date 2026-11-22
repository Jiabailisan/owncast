package webhooks

import (
	"time"

	"github.com/teris-io/shortid"

	"github.com/owncast/owncast/models"
)

// SendStreamStatusEvent dispatches a stream-status event (started,
// stopped, title-updated, …) to all configured webhook destinations.
func (s *Service) SendStreamStatusEvent(eventType models.EventType, streamTitle string) {
	s.sendStreamStatusEvent(eventType, shortid.MustGenerate(), time.Now(), streamTitle)
}

func (s *Service) sendStreamStatusEvent(eventType models.EventType, id string, timestamp time.Time, streamTitle string) {
	// A publisher may set this stream's title in its key. When it did, the notification
	// carries that title; otherwise the saved default applies.
	if streamTitle == "" {
		streamTitle = s.configRepository.GetStreamTitle()
	}

	s.SendEventToWebhooks(WebhookEvent{
		Type: eventType,
		EventData: map[string]interface{}{
			"id":          id,
			"name":        s.configRepository.GetServerName(),
			"summary":     s.configRepository.GetServerSummary(),
			"streamTitle": streamTitle,
			"status":      s.getStatus(),
			"serverURL":   s.configRepository.GetServerURL(),
			"timestamp":   timestamp,
		},
	})
}
