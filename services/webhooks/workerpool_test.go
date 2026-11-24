package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// captureErrorLogs redirects the standard logger into a buffer for the rest of
// the test. Only error lines are captured so unrelated worker chatter cannot
// pollute the assertions.
func captureErrorLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	logger := log.StandardLogger()
	previousOut := logger.Out
	previousFormatter := logger.Formatter
	previousLevel := logger.Level

	var captured bytes.Buffer
	logger.SetOutput(&captured)
	logger.SetFormatter(&log.TextFormatter{DisableColors: true, DisableTimestamp: true})
	logger.SetLevel(log.ErrorLevel)

	t.Cleanup(func() {
		logger.SetOutput(previousOut)
		logger.SetFormatter(previousFormatter)
		logger.SetLevel(previousLevel)
	})

	return &captured
}

// assertNoURLSecrets fails when a log line or an error message leaks the
// destination URL, the path token inside it, or its query credentials.
func assertNoURLSecrets(t *testing.T, where, text, destination, pathToken, queryCred string) {
	t.Helper()

	for _, secret := range []string{destination, pathToken, queryCred} {
		if strings.Contains(text, secret) {
			t.Errorf("%s must not leak %q, got %q", where, secret, text)
		}
	}
}

// The worker is what surfaces a failed delivery to operators, so the log line
// itself is part of the contract: level=error, the webhook ID, the event type
// and the status code - but never the destination URL, which carries a path
// token and query credentials.
func TestWorkerLogsRejectedDelivery(t *testing.T) {
	const (
		status    = http.StatusForbidden
		pathToken = "fake-path-token-abc"
		queryCred = "fake-query-cred-xyz"
	)

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer svr.Close()

	repo := webhookrepository.New(testDatastore)

	destination := fmt.Sprintf("%s/%s?cred=%s", svr.URL, pathToken, queryCred)

	id, err := repo.InsertWebhook(destination, []models.EventType{models.MessageSent}, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := repo.DeleteWebhook(id); err != nil {
			t.Error(err)
		}
	}()

	captured := captureErrorLogs(t)

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
	if !strings.Contains(output, fmt.Sprintf("webhook %d", id)) {
		t.Errorf("expected the webhook ID %d in the log line, got %q", id, output)
	}
	if !strings.Contains(output, string(models.MessageSent)) {
		t.Errorf("expected the event type in the log line, got %q", output)
	}
	assertNoURLSecrets(t, "the failure log line", output, destination, pathToken, queryCred)

	if after := findWebhook(t, repo, id); after.LastUsed != nil {
		t.Errorf("a rejected delivery must not update last_used, got %v", after.LastUsed)
	}
}

// A delivery that never reaches the receiver fails inside net/http with a
// *url.Error whose message embeds the full URL. Both the returned error and the
// log line must carry the unwrapped cause only.
func TestWorkerLogsTransportFailureWithoutURL(t *testing.T) {
	const (
		pathToken = "fake-path-token-abc"
		queryCred = "fake-query-cred-xyz"
	)

	// Bind a port and close it again so nothing is listening when the worker
	// dials: client.Do then fails before any request is written.
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	destination := fmt.Sprintf("%s/%s?cred=%s", svr.URL, pathToken, queryCred)
	svr.Close()

	repo := webhookrepository.New(testDatastore)

	id, err := repo.InsertWebhook(destination, []models.EventType{models.MessageSent}, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := repo.DeleteWebhook(id); err != nil {
			t.Error(err)
		}
	}()

	stored := findWebhook(t, repo, id)

	// The error handed back to the caller is redacted too, not just the log line.
	directErr := testSvc.sendWebhook(Job{
		payload: WebhookEvent{Type: models.MessageSent, EventData: struct{}{}},
		webhook: stored,
	})
	if directErr == nil {
		t.Fatal("a connection failure should be reported as a failed delivery")
	}
	if directErr.Error() == "" {
		t.Error("a failed delivery should still explain itself")
	}
	assertNoURLSecrets(t, "the returned error", directErr.Error(), destination, pathToken, queryCred)

	captured := captureErrorLogs(t)

	var wg sync.WaitGroup
	testSvc.sendEventToWebhooks(WebhookEvent{Type: models.MessageSent, EventData: struct{}{}}, &wg)
	wg.Wait()

	output := captured.String()

	if !strings.Contains(output, "level=error") {
		t.Errorf("expected a level=error log line, got %q", output)
	}
	if !strings.Contains(output, fmt.Sprintf("webhook %d", id)) {
		t.Errorf("expected the webhook ID %d in the log line, got %q", id, output)
	}
	assertNoURLSecrets(t, "the failure log line", output, destination, pathToken, queryCred)

	if after := findWebhook(t, repo, id); after.LastUsed != nil {
		t.Errorf("a failed delivery must not update last_used, got %v", after.LastUsed)
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

// newRedirectServer answers every request with a 302 pointing at location, so a
// delivery against it fails while net/http handles the redirect.
func newRedirectServer(t *testing.T, location string) *httptest.Server {
	t.Helper()

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(svr.Close)

	return svr
}

// A 302 whose Location carries a path token and query credentials fails the
// delivery during redirect handling, and net/http reports that failure through a
// *url.Error. What leaks depends on the Location shape:
//
//   - a syntactically valid relative Location is followed until the redirect
//     limit, and url.Error.URL becomes the Location itself, so the raw text is
//     Post "/fake-path-token-abc?cred=...": stopped after 10 redirects;
//   - an unparseable Location is echoed verbatim by net/http in
//     failed to parse Location header "http://[::1]:namedport/...".
//
// Neither may reach the returned error or the worker's log line, and a failed
// delivery must leave last_used untouched.
func TestRedirectFailureKeepsLocationSecretsOutOfErrorAndLog(t *testing.T) {
	const (
		pathToken = "fake-path-token-abc"
		queryCred = "fake-query-cred-xyz"
	)

	tests := []struct {
		name     string
		location string
	}{
		{
			name:     "relative Location followed until the redirect limit",
			location: "/" + pathToken + "?cred=" + queryCred,
		},
		{
			name:     "unparseable Location rejected during redirect handling",
			location: "http://[::1]:namedport/" + pathToken + "?cred=" + queryCred,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			svr := newRedirectServer(t, testCase.location)

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

			stored := findWebhook(t, repo, id)

			directErr := testSvc.sendWebhook(Job{
				payload: WebhookEvent{Type: models.MessageSent, EventData: struct{}{}},
				webhook: stored,
			})
			if directErr == nil {
				t.Fatal("a redirect failure should be reported as a failed delivery")
			}
			if !strings.Contains(directErr.Error(), string(deliveryFailureTransport)) {
				t.Errorf("error %q should carry the %q class", directErr.Error(), deliveryFailureTransport)
			}
			assertNoURLSecrets(t, "the returned error", directErr.Error(), svr.URL, pathToken, queryCred)

			// The cause stays reachable for callers even though its text - which
			// can echo the Location - never reaches the error string.
			var delivery *deliveryError
			if !errors.As(directErr, &delivery) {
				t.Fatalf("expected a *deliveryError, got %T", directErr)
			}
			if delivery.statusCode() != 0 {
				t.Errorf("a redirect failure produced no response, got status %d", delivery.statusCode())
			}
			if delivery.Unwrap() == nil {
				t.Error("the cause should stay reachable through Unwrap")
			}

			captured := captureErrorLogs(t)

			// Go through the queue so the assertion covers the worker's own error
			// path. The log write happens before wg.Done(), so wg.Wait() orders it
			// before the read.
			var wg sync.WaitGroup
			testSvc.sendEventToWebhooks(WebhookEvent{Type: models.MessageSent, EventData: struct{}{}}, &wg)
			wg.Wait()

			output := captured.String()

			if !strings.Contains(output, "level=error") {
				t.Errorf("expected a level=error log line, got %q", output)
			}
			if !strings.Contains(output, fmt.Sprintf("webhook %d", id)) {
				t.Errorf("expected the webhook ID %d in the log line, got %q", id, output)
			}
			if !strings.Contains(output, string(deliveryFailureTransport)) {
				t.Errorf("expected the failure class %q in the log line, got %q", deliveryFailureTransport, output)
			}
			assertNoURLSecrets(t, "the failure log line", output, svr.URL, pathToken, queryCred)

			if after := findWebhook(t, repo, id); after.LastUsed != nil {
				t.Errorf("a failed delivery must not update last_used, got %v", after.LastUsed)
			}
		})
	}
}

// timeoutCause stands in for net/http's own timeout error, which reports through
// net.Error.Timeout rather than context.DeadlineExceeded.
type timeoutCause struct{}

func (timeoutCause) Error() string { return "i/o timeout" }

func (timeoutCause) Timeout() bool { return true }

// Temporary keeps timeoutCause assignable to net.Error, which still requires it.
func (timeoutCause) Temporary() bool { return true }

// A client.Do failure is reduced to the closed failure vocabulary so no cause
// text can reach an error string or a log line.
func TestClassifyDeliveryFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want deliveryFailureKind
	}{
		{name: "context deadline", err: context.DeadlineExceeded, want: deliveryFailureTimeout},
		{name: "wrapped context deadline", err: fmt.Errorf("dial: %w", context.DeadlineExceeded), want: deliveryFailureTimeout},
		{name: "net timeout", err: timeoutCause{}, want: deliveryFailureTimeout},
		{
			name: "url.Error wrapping a net timeout",
			err:  &url.Error{Op: "Post", URL: "https://example.com/fake-path-token-abc", Err: timeoutCause{}},
			want: deliveryFailureTimeout,
		},
		{name: "connection refused", err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), want: deliveryFailureTransport},
		{name: "redirect limit", err: errors.New("stopped after 10 redirects"), want: deliveryFailureTransport},
		{name: "nil cause", err: nil, want: deliveryFailureTransport},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := classifyDeliveryFailure(testCase.err); got != testCase.want {
				t.Errorf("classifyDeliveryFailure(%v) = %q, want %q", testCase.err, got, testCase.want)
			}
		})
	}
}

// The error string carries the HTTP status or the failure class only. The cause
// stays reachable through Unwrap so errors.Is/errors.As keep working, but its
// text - which can embed the destination URL or a Location header - never
// appears in the string.
func TestDeliveryErrorRendersClassificationOnly(t *testing.T) {
	const (
		pathToken = "fake-path-token-abc"
		queryCred = "fake-query-cred-xyz"
	)

	destination := "https://example.com/" + pathToken + "?cred=" + queryCred
	cause := fmt.Errorf("Post %q: %w", destination, context.DeadlineExceeded)

	tests := []struct {
		name string
		err  *deliveryError
		want string
	}{
		{
			name: "HTTP status",
			err:  &deliveryError{status: http.StatusForbidden},
			want: "webhook delivery failed with HTTP status 403",
		},
		{
			name: "invalid target",
			err:  &deliveryError{kind: deliveryFailureInvalidTarget, err: cause},
			want: "webhook delivery failed: invalid target",
		},
		{
			name: "timeout",
			err:  &deliveryError{kind: deliveryFailureTimeout, err: cause},
			want: "webhook delivery failed: timeout",
		},
		{
			name: "transport failure",
			err:  &deliveryError{kind: deliveryFailureTransport, err: cause},
			want: "webhook delivery failed: transport failure",
		},
		{
			name: "no detail",
			err:  &deliveryError{},
			want: "webhook delivery failed",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.err.Error(); got != testCase.want {
				t.Errorf("Error() = %q, want %q", got, testCase.want)
			}
			assertNoURLSecrets(t, "the error string", testCase.err.Error(), destination, pathToken, queryCred)

			if testCase.err.err != nil {
				if !errors.Is(testCase.err, context.DeadlineExceeded) {
					t.Error("errors.Is should still reach context.DeadlineExceeded through the cause")
				}
			}
		})
	}
}

// errors.As reaches the original cause, so a caller can still inspect it even
// though deliveryError renders only a classification.
func TestDeliveryErrorKeepsCauseReachable(t *testing.T) {
	cause := &net.DNSError{Name: "example.com", Err: "no such host", IsNotFound: true}
	err := &deliveryError{kind: deliveryFailureTransport, err: cause}

	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || dnsErr != cause {
		t.Errorf("errors.As should reach the original cause, got %v", dnsErr)
	}

	if strings.Contains(err.Error(), "example.com") || strings.Contains(err.Error(), "no such host") {
		t.Errorf("the error string must not render the cause, got %q", err.Error())
	}
}
