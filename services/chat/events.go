package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/chat/events"
	"github.com/owncast/owncast/services/dispatcher"
	"github.com/owncast/owncast/utils"
)

func (s *Service) userNameChanged(eventData chatClientEvent) {
	var receivedEvent events.NameChangeEvent
	if err := json.Unmarshal(eventData.data, &receivedEvent); err != nil {
		log.Errorln("error unmarshalling to NameChangeEvent", err)
		return
	}

	proposedUsername := receivedEvent.NewName

	// Check if name is on the blocklist
	blocklist := s.configRepository.GetForbiddenUsernameList()

	// Names have a max length
	proposedUsername = utils.MakeSafeStringOfLength(proposedUsername, config.MaxChatDisplayNameLength)

	// Check if the sanitized name is empty or just whitespace
	if strings.TrimSpace(proposedUsername) == "" {
		log.Debugln(logSanitize(eventData.client.User.DisplayName), "attempted to change name to empty or whitespace-only name")
		message := "Display name cannot be empty or contain only whitespace."
		s.sendActionToClient(eventData.client, message)

		// Resend the client's user so their username is in sync.
		eventData.client.sendConnectedClientInfo()

		return
	}

	if blockedName, forbidden := ForbiddenUsername(proposedUsername, blocklist); forbidden {
		// Denied.
		log.Debugln(logSanitize(eventData.client.User.DisplayName), "blocked from changing name to", logSanitize(proposedUsername), "due to blocked name", blockedName)
		message := fmt.Sprintf("You cannot change your name to **%s**.", proposedUsername)
		s.sendActionToClient(eventData.client, message)

		// Resend the client's user so their username is in sync.
		eventData.client.sendConnectedClientInfo()

		return
	}

	// Check if the name is not already assigned to a registered user.
	if available, err := s.userRepository.IsDisplayNameAvailable(proposedUsername); err != nil {
		log.Errorln("error checking if name is available", err)
		return
	} else if !available {
		message := fmt.Sprintf("The name **%s** has already been registered. If this is your name, please authenticate.", proposedUsername)
		s.sendActionToClient(eventData.client, message)

		// Resend the client's user so their username is in sync.
		eventData.client.sendConnectedClientInfo()

		return
	}

	savedUser := s.userRepository.GetUserByToken(eventData.client.accessToken)
	if savedUser == nil {
		// The token was revoked or the row is gone; do not crash the chat
		// goroutine, just refresh the client with what we still know.
		eventData.client.sendConnectedClientInfo()

		return
	}
	oldName := savedUser.DisplayName

	// Check that the new name is different from old.
	if proposedUsername == oldName {
		eventData.client.sendConnectedClientInfo()
		return
	}

	// Save the new name
	u := eventData.client.GetUser()
	if u == nil {
		return
	}
	if err := s.userRepository.ChangeUsername(u.ID, proposedUsername); err != nil {
		log.Errorln("error changing username", err)
	}

	// Prepare updated user copy before publishing to the client pointer so
	// concurrent readers never observe a half-mutated User struct.
	now := time.Now()
	updatedUser := *savedUser
	updatedUser.DisplayName = proposedUsername
	updatedUser.NameChangedAt = &now
	eventData.client.SetUser(&updatedUser)

	broadcastEvent := events.NameChangeBroadcast{
		Oldname: oldName,
	}
	broadcastEvent.User = &updatedUser
	broadcastEvent.SetDefaults()
	payload := broadcastEvent.GetBroadcastPayload()
	if err := s.Broadcast(payload); err != nil {
		log.Errorln("error broadcasting NameChangeEvent", err)
		return
	}

	// Send chat user name changed webhook
	receivedEvent.User = &updatedUser
	receivedEvent.ClientID = eventData.client.Id
	s.webhooks.SendChatEventUsernameChanged(receivedEvent)

	// Resend the client's user so their username is in sync.
	eventData.client.sendConnectedClientInfo()
}

func (s *Service) userColorChanged(eventData chatClientEvent) {
	var receivedEvent events.ColorChangeEvent
	if err := json.Unmarshal(eventData.data, &receivedEvent); err != nil {
		log.Errorln("error unmarshalling to ColorChangeEvent", err)
		return
	}

	// Verify this color is valid
	if receivedEvent.NewColor > config.MaxUserColor {
		log.Errorln("invalid color requested when changing user display color")
		return
	}

	u := eventData.client.GetUser()
	if u == nil {
		return
	}
	// Save the new color
	if err := s.userRepository.ChangeUserColor(u.ID, receivedEvent.NewColor); err != nil {
		log.Errorln("error changing user display color", err)
	}

	// Resend client's user info with new color, publishing an immutable copy.
	updatedUser := *u
	updatedUser.DisplayColor = receivedEvent.NewColor
	eventData.client.SetUser(&updatedUser)
	eventData.client.sendConnectedClientInfo()
}

func (s *Service) userMessageSent(eventData chatClientEvent) {
	var event events.UserMessageEvent
	if err := json.Unmarshal(eventData.data, &event); err != nil {
		log.Errorln("error unmarshalling to UserMessageEvent", err)
		return
	}

	event.SetDefaults()
	event.ClientID = eventData.client.Id

	// Ignore empty messages
	if event.Empty() {
		return
	}

	// Ignore if the stream has been offline
	if !s.getStatus().Online && s.getStatus().LastDisconnectTime != nil {
		disconnectedTime := s.getStatus().LastDisconnectTime.Time
		if time.Since(disconnectedTime) > 5*time.Minute {
			return
		}
	}

	event.User = s.userRepository.GetUserByToken(eventData.client.accessToken)

	// Guard against nil users
	if event.User == nil {
		return
	}

	// Run inbound-message filters (plugin filterChatMessage handlers) before
	// broadcast. A filter may rewrite event.Body in place or drop the message.
	if s.events != nil && !s.events.ApplyFilters(context.Background(), dispatcher.Event{Type: models.MessageSent, Payload: &event}) {
		return
	}

	payload := event.GetBroadcastPayload()
	if err := s.Broadcast(payload); err != nil {
		log.Errorln("error broadcasting UserMessageEvent payload", err)
		return
	}

	// Send chat message sent webhook
	s.webhooks.SendChatEvent(&event)
	s.chatMessagesSentCounter.Inc()
	s.chatMessageRepository.SaveUserMessage(event)
	eventData.client.MessageCount++
}

func logSanitize(userValue string) string {
	// strip carriage return and newline from user-submitted values to prevent log injection
	sanitizedValue := strings.ReplaceAll(userValue, "\n", "")
	sanitizedValue = strings.ReplaceAll(sanitizedValue, "\r", "")

	return fmt.Sprintf("userSuppliedValue(%s)", sanitizedValue)
}
