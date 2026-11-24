package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/persistence/configrepository"
	"github.com/owncast/owncast/persistence/webhookrepository"
	"github.com/owncast/owncast/services/activitypub/persistence/followersrepository"
	"github.com/owncast/owncast/services/dispatcher"
)

// Job bundles a single webhook destination + payload for a worker.
type Job struct {
	wg      *sync.WaitGroup
	payload WebhookEvent
	webhook models.Webhook
}

// Service owns the webhook dispatcher: a bounded worker pool that
// fans events out to configured destinations.
//
// Most events also need the current stream status and follower details
// to build their payloads, so those collaborators are dependencies.
type Service struct {
	workerPoolSize int
	queue          chan Job

	// getStatus returns the current stream status for inclusion in
	// outbound event payloads. Supplied by main.go (the stream service).
	getStatus func() models.Status

	// followers is consulted by the fediverse-engagement builder to
	// resolve actor IRIs to display data.
	followers followersrepository.FollowersRepository

	// configRepository provides server metadata included in stream-status
	// event payloads.
	configRepository configrepository.ConfigRepository

	// webhookRepository stores configured webhook destinations and is
	// consulted when dispatching events as well as when marking a
	// destination as used after a successful send.
	webhookRepository webhookrepository.WebhookRepository

	// events is the shared dispatcher every event is published to, so
	// in-process consumers (the plugin host today, other subsystems later)
	// receive them without this service knowing who they are.
	events *dispatcher.Dispatcher
}

// Deps lists every collaborator a *Service needs at construction.
type Deps struct {
	GetStatus         func() models.Status
	Followers         followersrepository.FollowersRepository
	ConfigRepository  configrepository.ConfigRepository
	WebhookRepository webhookrepository.WebhookRepository
	Events            *dispatcher.Dispatcher
}

// New constructs an idle webhook Service. Call Start to launch the
// worker pool.
func New(deps Deps) *Service {
	return &Service{
		workerPoolSize:    runtime.GOMAXPROCS(0),
		getStatus:         deps.GetStatus,
		followers:         deps.Followers,
		configRepository:  deps.ConfigRepository,
		webhookRepository: deps.WebhookRepository,
		events:            deps.Events,
	}
}

// SetGetStatus wires the stream-status callback after construction.
// Exists because the composition root has a small cycle: webhooks
// needs stream.GetStatus, but stream needs *webhooks.Service. main.go
// constructs webhooks first with a nil callback, then fills it in once
// streamSvc exists. Must be called before Start.
func (s *Service) SetGetStatus(fn func() models.Status) {
	s.getStatus = fn
}

const (
	// webhookRequestTimeout bounds one delivery attempt.
	webhookRequestTimeout = 10 * time.Second
	// webhookQueueDepth keeps enqueueing non-blocking for the chat event loop.
	webhookQueueDepth = 256
)

// Start launches the worker goroutines that drain the queue.
func (s *Service) Start() {
	// Buffered so enqueueing cannot block the caller (the chat event loop).
	s.queue = make(chan Job, webhookQueueDepth)
	for i := 1; i <= s.workerPoolSize; i++ {
		go s.worker(i)
	}
}

func (s *Service) addToQueue(webhook models.Webhook, payload WebhookEvent, wg *sync.WaitGroup) {
	// Destinations are named by their stored ID, never by URL: a webhook URL
	// routinely embeds a path token and query credentials, and log files are
	// far easier to hand out than the admin UI.
	log.Tracef("Queued Event %s for Webhook %d", payload.Type, webhook.ID)
	select {
	case s.queue <- Job{wg, payload, webhook}:
	default:
		// Queue is full: drop the delivery rather than blocking the producer.
		log.Warnln("webhook queue full; dropping event", payload.Type, "for webhook", webhook.ID)
		if wg != nil {
			wg.Done()
		}
	}
}

func (s *Service) worker(workerID int) {
	log.Debugf("Started Webhook worker %d", workerID)

	for job := range s.queue {
		log.Debugf("Event %s sent to Webhook %d using worker %d", job.payload.Type, job.webhook.ID, workerID)

		if err := s.sendWebhook(job); err != nil {
			// The failure line must stay diagnosable without the URL or the
			// underlying transport text: the stored webhook ID identifies the
			// destination, the event type says what was being delivered, and the
			// HTTP status (when the receiver answered) or the failure class says
			// why it failed. See deliveryFailureSummary for why the cause never
			// reaches the log.
			log.Errorf("Event: %s failed to send to webhook %d: %s", job.payload.Type, job.webhook.ID, deliveryFailureSummary(err))
		}
		log.Tracef("Done with Event %s to Webhook %d using worker %d", job.payload.Type, job.webhook.ID, workerID)
		if job.wg != nil {
			job.wg.Done()
		}
	}
}

// deliveryFailureKind is the closed vocabulary of reasons a delivery produced no
// HTTP response. It is the only failure detail allowed into an error string or a
// log line: net/http error text embeds the request URL, and redirect failures
// can echo the raw Location header back.
type deliveryFailureKind string

const (
	// deliveryFailureInvalidTarget means the stored destination could not be
	// turned into a request at all.
	deliveryFailureInvalidTarget deliveryFailureKind = "invalid target"
	// deliveryFailureTimeout means the attempt was abandoned before the receiver
	// answered (client deadline or a dial/handshake timeout).
	deliveryFailureTimeout deliveryFailureKind = "timeout"
	// deliveryFailureTransport covers every other client.Do failure: refused or
	// reset connections, DNS failures, TLS errors, unusable redirects.
	deliveryFailureTransport deliveryFailureKind = "transport failure"
)

// deliveryError reports a failed webhook delivery using only data that is safe
// to log or return to a caller. The destination URL is deliberately absent: an
// operator's webhook URL routinely embeds a path token and query credentials,
// so it is treated as a secret. The stored webhook ID locates the destination
// instead, and the HTTP status (when the receiver answered) or the failure class
// explains the failure.
type deliveryError struct {
	// status is the HTTP status code the receiver answered with, or 0 when no
	// response was produced (connection failure, timeout, malformed URL).
	status int
	// kind classifies a delivery that produced no response. It stays empty when
	// status is set.
	kind deliveryFailureKind
	// err is the underlying cause with any *url.Error unwrapped. It exists for
	// errors.Is/errors.As only: url.Error text embeds the full request URL, and
	// a redirect failure wraps text that echoes the raw Location header, so the
	// cause never appears in Error() or in a log line.
	err error
}

func (e *deliveryError) Error() string {
	if status := e.statusCode(); status != 0 {
		return fmt.Sprintf("webhook delivery failed with HTTP status %d", status)
	}
	if e.kind != "" {
		return fmt.Sprintf("webhook delivery failed: %s", e.kind)
	}
	return "webhook delivery failed"
}

// summary renders the failure for a log line: the HTTP status when the receiver
// answered, otherwise the failure class. The cause is never included.
func (e *deliveryError) summary() string {
	if status := e.statusCode(); status != 0 {
		return fmt.Sprintf("HTTP status %d", status)
	}
	if e.kind != "" {
		return string(e.kind)
	}
	return "delivery failed"
}

// Unwrap exposes the cause so errors.Is/errors.As still work for callers.
func (e *deliveryError) Unwrap() error {
	return e.err
}

// statusCode reports the receiver's HTTP status, or 0 when the request never
// produced a response.
func (e *deliveryError) statusCode() int {
	return e.status
}

// deliveryFailureSummary reduces a failed delivery to what a log line may carry:
// the HTTP status or the failure class. A cause is never rendered, and failures
// that happen before a request exists (payload marshalling, signature lookup)
// collapse to a fixed label, because their text is not worth the leak risk.
func deliveryFailureSummary(err error) string {
	var delivery *deliveryError
	if errors.As(err, &delivery) {
		return delivery.summary()
	}
	return "internal error"
}

// classifyDeliveryFailure maps a client.Do failure to the closed failure
// vocabulary. Timeouts are recognised through the cause, so a wrapped
// context.DeadlineExceeded and a net.Error still report as a timeout.
func classifyDeliveryFailure(err error) deliveryFailureKind {
	if errors.Is(err, context.DeadlineExceeded) {
		return deliveryFailureTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return deliveryFailureTimeout
	}
	return deliveryFailureTransport
}

// unwrapURLError strips the *url.Error wrapper net/http adds around transport
// and parse failures, keeping only the underlying cause. Both url.Error.Error()
// and %q formatting of it include the full request URL, which carries the path
// token and query credentials. Unwrapping removes that URL but not every secret:
// a redirect that cannot be parsed leaves net/http's own
// 'failed to parse Location header "<location>"' text in the cause, which is why
// the cause is kept for errors.Is/errors.As and never rendered.
func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

func (s *Service) sendWebhook(job Job) error {
	jsonText, err := json.Marshal(job.payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", job.webhook.URL, bytes.NewReader(jsonText))
	if err != nil {
		// Without this check a malformed stored URL leaves req nil and the
		// Header.Set below panics inside a worker goroutine. The URL itself is
		// never echoed back: http.NewRequest parses through url.Parse, whose
		// errors are *url.Error values carrying the raw URL, so only the cause
		// survives, and deliveryError renders the failure class rather than the
		// cause.
		return &deliveryError{kind: deliveryFailureInvalidTarget, err: unwrapURLError(err)}
	}
	signature, err := s.generateSignature(jsonText, job)
	if err != nil {
		return err
	}

	req.Header.Set("owncast-signature", signature)
	req.Header.Set("Content-Type", "application/json")

	// A webhook destination that never answers must not pin a worker forever.
	client := &http.Client{Timeout: webhookRequestTimeout}

	resp, err := client.Do(req)
	if err != nil {
		// client.Do failures arrive as *url.Error, whose message embeds the full
		// URL (path token and query included). Only the cause is kept, and only
		// its class reaches an error string or a log line.
		cause := unwrapURLError(err)
		return &deliveryError{kind: classifyDeliveryFailure(cause), err: cause}
	}

	defer resp.Body.Close()
	// Drain a bounded amount so the connection can go back to the pool instead of
	// paying a fresh TCP+TLS handshake for every delivery.
	_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)

	// Only a 2xx answer means the receiver accepted the event. A 4xx/5xx means the
	// delivery failed, so report it and leave last_used untouched: a rejected
	// destination must not look healthy in the admin UI. The status code is the
	// only detail carried in the error; the response body is never read into it,
	// and the signature never appears in it.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &deliveryError{status: resp.StatusCode}
	}

	if err := s.webhookRepository.SetWebhookAsUsed(job.webhook); err != nil {
		// The statement binds only the webhook ID, so this driver error cannot
		// carry the destination URL; the ID is added to locate the destination.
		log.Warnf("failed to record last_used for webhook %d: %s", job.webhook.ID, err)
	}

	return nil
}
