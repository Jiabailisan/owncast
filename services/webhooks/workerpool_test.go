package webhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/persistence/webhookrepository"
	"github.com/owncast/owncast/services/chat/events"
	"github.com/owncast/owncast/services/ingest"
)

// findWebhook returns the stored webhook with the given id, so a test can assert
// on persisted state (last_used in particular) instead of on in-memory copies.
func findWebhook(t *testing.T, repo webhookrepository.WebhookRepository, id int) models.Webhook {
	t.Helper()

	hooks, err := repo.GetWebhooks()
	if err != nil {
		t.Fatalf("failed to read webhooks: %v", err)
	}

	for _, hook := range hooks {
		if hook.ID == id {
			return hook
		}
	}

	t.Fatalf("webhook %d not found in the repository", id)

	return models.Webhook{}
}

// only 2xx counts as a delivery. 4xx/5xx must return an error that carries the
// status code, must not leak the response body, and must not update last_used.
func TestSendWebhookStatusClassification(t *testing.T) {
	const (
		secret    = "abc123"
		leakyBody = "receiver-body-must-not-reach-the-error"
	)

	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "200 OK", status: http.StatusOK},
		{name: "204 No Content", status: http.StatusNoContent},
		{name: "400 Bad Request", status: http.StatusBadRequest, wantErr: true},
		{name: "403 Forbidden", status: http.StatusForbidden, wantErr: true},
		{name: "500 Internal Server Error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(leakyBody))
			}))
			defer svr.Close()

			repo := webhookrepository.New(testDatastore)

			id, err := repo.InsertWebhook(svr.URL, []models.EventType{models.StreamStarted}, secret)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := repo.DeleteWebhook(id); err != nil {
					t.Error(err)
				}
			}()

			stored := findWebhook(t, repo, id)
			if stored.LastUsed != nil {
				t.Fatalf("a fresh webhook should have no last_used, got %v", stored.LastUsed)
			}

			err = testSvc.sendWebhook(Job{
				payload: WebhookEvent{Type: models.StreamStarted, EventData: struct{}{}},
				webhook: stored,
			})

			if testCase.wantErr {
				if err == nil {
					t.Fatalf("HTTP %d should be reported as a failed delivery", testCase.status)
				}

				message := err.Error()
				if !strings.Contains(message, fmt.Sprintf("%d", testCase.status)) {
					t.Errorf("error %q should carry the HTTP status code %d", message, testCase.status)
				}
				if strings.Contains(message, leakyBody) {
					t.Errorf("error %q must not include the response body", message)
				}
				if strings.Contains(message, secret) || strings.Contains(message, "owncast-signature") {
					t.Errorf("error %q must not include credentials or the signature", message)
				}
				if strings.Contains(message, svr.URL) {
					t.Errorf("error %q should be built from the status code only, got the URL too", message)
				}
			} else {
				if err != nil {
					t.Fatalf("HTTP %d should be a successful delivery, got %v", testCase.status, err)
				}
			}

			after := findWebhook(t, repo, id)

			if testCase.wantErr && after.LastUsed != nil {
				t.Errorf("HTTP %d must not update last_used, got %v", testCase.status, after.LastUsed)
			}
			if !testCase.wantErr && after.LastUsed == nil {
				t.Errorf("HTTP %d should update last_used", testCase.status)
			}
		})
	}
}

// The worker is what surfaces a failed delivery to operators, so the log line
// itself is part of the contract: level=error plus the status code.
func TestWorkerLogsRejectedDelivery(t *testing.T) {
	const status = http.StatusForbidden

	logger := log.StandardLogger()
	previousOut := logger.Out
	previousFormatter := logger.Formatter
	previousLevel := logger.Level

	var captured bytes.Buffer
	logger.SetOutput(&captured)
	logger.SetFormatter(&log.TextFormatter{DisableColors: true, DisableTimestamp: true})
	logger.SetLevel(log.ErrorLevel)

	defer func() {
		logger.SetOutput(previousOut)
		logger.SetFormatter(previousFormatter)
		logger.SetLevel(previousLevel)
	}()

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer svr.Close()

	repo := webhookrepository.New(testDatastore)

	id, err := repo.InsertWebhook(svr.URL, []models.EventType{models.MessageSent}, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := repo.DeleteWebhook(id); err != nil {
			t.Error(err)
		}
	}()

	// Go through the queue so the assertion covers the worker's own error path.
	// The log write happens before wg.Done(), so wg.Wait() orders it before the read.
	var wg sync.WaitGroup
	testSvc.sendEventToWebhooks(WebhookEvent{Type: models.MessageSent, EventData: struct{}{}}, &wg)
	wg.Wait()

	output := captured.String()

	if !strings.Contains(output, "level=error") {
		t.Errorf("expected a level=error log line, got %q", output)
	}
	if !strings.Contains(output, fmt.Sprintf("%d", status)) {
		t.Errorf("expected the HTTP status %d in the log line, got %q", status, output)
	}
	if !strings.Contains(output, svr.URL) {
		t.Errorf("expected the destination URL in the log line, got %q", output)
	}
	if !strings.Contains(output, string(models.MessageSent)) {
		t.Errorf("expected the event type in the log line, got %q", output)
	}

	if after := findWebhook(t, repo, id); after.LastUsed != nil {
		t.Errorf("a rejected delivery must not update last_used, got %v", after.LastUsed)
	}
}

// A title taken from the ingest key (the raw ?title=%23Kobayashi's LiveTime
// query, decoded to "#Kobayashi's LiveTime") still reaches the receiver inside
// the event payload, and the delivery is still treated as a success.
func TestStreamStatusEventCarriesParsedTitle(t *testing.T) {
	const rawQuery = "title=%23Kobayashi's LiveTime"

	want := ingest.ParseStreamTitleFromQuery(rawQuery)
	if want != "#Kobayashi's LiveTime" {
		t.Fatalf("parsed title = %q, want %q", want, "#Kobayashi's LiveTime")
	}

	type capturedEvent struct {
		Type      models.EventType       `json:"type"`
		EventData map[string]interface{} `json:"eventData"`
	}

	received := make(chan capturedEvent, 1)
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event capturedEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("failed to decode webhook body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- event
		w.WriteHeader(http.StatusOK)
	}))
	defer svr.Close()

	repo := webhookrepository.New(testDatastore)

	id, err := repo.InsertWebhook(svr.URL, []models.EventType{models.StreamStarted}, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := repo.DeleteWebhook(id); err != nil {
			t.Error(err)
		}
	}()

	testSvc.sendStreamStatusEvent(events.StreamStarted, "id", time.Unix(72, 6).UTC(), want)

	select {
	case event := <-received:
		if event.Type != models.StreamStarted {
			t.Errorf("event type = %q, want %q", event.Type, models.StreamStarted)
		}

		gotTitle, _ := event.EventData["streamTitle"].(string)
		if gotTitle != want {
			t.Errorf("streamTitle in the POST body = %q, want %q", gotTitle, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the webhook delivery")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		after := findWebhook(t, repo, id)
		if after.LastUsed != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a successful delivery should have updated last_used")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
