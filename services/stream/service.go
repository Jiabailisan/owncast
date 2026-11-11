// Package stream is the live-streaming lifecycle service: stream connect /
// disconnect handling, viewer stats, status reporting, storage provider
// setup, and the offline content state. It owns the state that used to
// live as package-level globals in the legacy `core` package; that package
// now exists only as a thin backward-compatibility shim that delegates
// here.
package stream

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/persistence/configrepository"
	"github.com/owncast/owncast/services/activitypub"
	"github.com/owncast/owncast/services/chat"
	"github.com/owncast/owncast/services/datastore"
	"github.com/owncast/owncast/services/geoip"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/transcoder"
	"github.com/owncast/owncast/services/webhooks"
	"github.com/owncast/owncast/yp"
)

// activeViewerPurgeTimeout is the silence-window after which a viewer is
// dropped from the active set.
const activeViewerPurgeTimeout = 15 * time.Second

// Service is the stream subsystem. Construct one in main.go with New(),
// register it with the legacy core compat shim via core.Init(svc), then
// call Start(ctx) to bring up the storage, transcoder, RTMP listener and
// associated infrastructure.
type Service struct {
	// stats owns viewer-count, connect timing, etc. statsMu guards the
	// stats fields that are touched from goroutines (viewer-set updates,
	// chat-client removal).
	stats   *models.Stats
	statsMu sync.RWMutex

	// tearingDown is set for the whole duration of the go-offline sequence.
	// IsStreamConnected flips to false at the start of that sequence, while the
	// teardown still has to close the session, the WebRTC legs and the ingest tee; a
	// publisher that reconnects in that window must wait instead of being accepted
	// and then closed by the teardown that is still running.
	tearingDown atomic.Bool

	// storage is the active HLS storage provider (local filesystem or S3).
	storage models.StorageProvider

	// transcoderMu guards transcoder, which the connect goroutine replaces
	// while status handlers read it for the FFmpeg panel.
	transcoderMu sync.RWMutex
	// transcoder holds the active ffmpeg child while a stream is online.
	// Replaced when a stream comes up, nil'd when it goes down.
	transcoder *transcoder.Transcoder
	// offlineTranscoder holds the always-on offline-clip process. Tracked so
	// shutdown cannot orphan it.
	offlineTranscoder *transcoder.Transcoder

	// yp talks to the Owncast directory.
	yp *yp.YP

	// broadcaster carries the metadata of the currently-connected RTMP
	// source. Nil between streams.
	broadcaster *models.Broadcaster

	// currentBroadcast holds the in-flight broadcast settings (latency
	// level, output variants) captured at stream-connect time.
	currentBroadcast *models.CurrentBroadcast

	// offlineCleanupTimer fires once N minutes after a stream goes
	// offline to wipe HLS content. onlineCleanupTicker fires every N
	// minutes while a stream is up to prune old segments.
	offlineCleanupTimer *time.Timer
	// offlineCleanupDone ends the offline cleanup goroutine; the Timer channel
	// itself never closes.
	offlineCleanupDone  chan struct{}
	offlineCleanupMu    sync.Mutex
	onlineCleanupTicker *time.Ticker
	// onlineCleanupDone ends the cleanup goroutine; a Ticker channel never
	// closes, so the goroutine needs an explicit stop signal.
	onlineCleanupDone chan struct{}
	onlineCleanupMu   sync.Mutex

	// onlineTimerCancelFunc cancels the delayed live-notification
	// dispatcher when a stream ends before the notification fires.
	onlineTimerCancelFunc context.CancelFunc

	// lastNotified is the last time we sent the live-going-online
	// notification; used to debounce re-notifications when a stream
	// flaps.
	lastNotified *time.Time

	// HLS transcoder plumbing. handler routes finished playlists/segments
	// to the storage provider; fileWriter feeds the transcoder's output
	// into handler.
	handler    transcoder.HLSHandler
	fileWriter *transcoder.FileWriterReceiverService

	// thumbnailGen owns the periodic thumbnail+preview snapshotter that
	// runs while a stream is online. Lazily started on stream-connect,
	// stopped on stream-disconnect.
	thumbnailGen *transcoder.ThumbnailGenerator

	// geoIPClient performs the asynchronous viewer-geolocation lookup.
	geoIPClient *geoip.Client

	// sessions is the cross-protocol single-publisher arbiter. The stream
	// service uses it to force-disconnect the active ingest session and to
	// report which protocol/key is live.
	sessions *ingest.Arbiter

	// webRTC is the optional WebRTC delivery service. Nil when delivery is not
	// wired; the stream then produces HLS only.
	webRTC WebRTCProvider
	// webRTCLegs tracks the variant indices whose media legs are open for the
	// current stream so they are closed exactly once on disconnect.
	webRTCLegs []int
	// legsMu guards legsByVariant and webRTCLegs.
	legsMu sync.RWMutex
	// legsByVariant maps variant index to its open media leg (isolated-model
	// restart rebasing).
	legsByVariant map[int]WebRTCMediaLeg
	// ingestTeeMu guards ingestTee (isolated process model only).
	ingestTeeMu sync.Mutex
	// ingestTee fans the ingest bytes out to both FFmpeg processes.
	ingestTee *ingestTee
	// webRTCProcessMu guards webRTCProcess (isolated process model only).
	webRTCProcessMu sync.RWMutex
	// webRTCProcess is the WebRTC-only FFmpeg process.
	webRTCProcess *transcoder.Transcoder
	// webRTCMu guards webRTCWarnings.
	webRTCMu sync.RWMutex
	// webRTCWarnings are the leg-planning warnings for the current stream.
	webRTCWarnings []models.StatusWarning

	// sessionMu guards currentSession, which is replaced on connect and
	// cleared on disconnect while status handlers read it.
	sessionMu sync.RWMutex
	// currentSession is the authenticated inbound session that started the
	// current live stream (RTMP or SRT). Nil between streams.
	currentSession ingest.Session

	// activitypub is the federation subsystem; the stream service uses
	// it to broadcast go-live notifications to followers when a stream
	// starts.
	activitypub *activitypub.Service

	// webhooks dispatches stream-status events (started/stopped) to
	// user-configured HTTP destinations.
	webhooks *webhooks.Service

	// chat is the websocket chat server; the stream service tells it
	// when a stream starts/ends so it can send the appropriate
	// system-message announcements.
	chat *chat.Service

	// datastore is the database handle the stream service hands to the
	// notifications subsystem when dispatching delayed go-live pushes.
	datastore *datastore.Datastore

	// configRepository provides all server settings used during stream
	// lifecycle (latency level, output variants, stream keys, …).
	configRepository configrepository.ConfigRepository

	// cfg holds runtime configuration values (HLS storage scratch dirs,
	// debug flag) shared with transcoder/thumbnail children spawned by
	// the stream lifecycle.
	cfg *config.Config

	// engine drives broadcaster ingest + transcoding. The local engine runs
	// it in-process; lifecycle is reported back via the StreamEvents methods.
	engine StreamEngine
}

// Deps is the explicit-dependency contract the service requires at
// construction time. Each protocol's ingest service is supplied as an
// IngestListener so the engine starts all of them identically.
type Deps struct {
	Ingest           []IngestListener
	Sessions         *ingest.Arbiter
	WebRTC           WebRTCProvider
	Activitypub      *activitypub.Service
	Webhooks         *webhooks.Service
	Chat             *chat.Service
	YP               *yp.YP
	Datastore        *datastore.Datastore
	ConfigRepository configrepository.ConfigRepository
	Config           *config.Config
	// GeoIP is the shared offline database client; nil disables lookups.
	GeoIP *geoip.Client
}

// New constructs an idle stream Service. Call Start(ctx) to bring up the
// storage backend, transcoder, RTMP listener, and associated lifecycle.
func New(deps Deps) *Service {
	s := &Service{
		geoIPClient:      deps.GeoIP,
		fileWriter:       transcoder.NewFileWriterReceiverService(deps.Config),
		sessions:         deps.Sessions,
		webRTC:           deps.WebRTC,
		activitypub:      deps.Activitypub,
		webhooks:         deps.Webhooks,
		chat:             deps.Chat,
		yp:               deps.YP,
		datastore:        deps.Datastore,
		configRepository: deps.ConfigRepository,
		cfg:              deps.Config,
	}
	// The local engine binds every in-process ingest listener (RTMP, and SRT
	// when enabled) and routes inbound sessions back through the service's
	// StreamEvents handlers.
	s.engine = &localStreamEngine{
		listeners:     deps.Ingest,
		onConnect:     s.HandleIngestSession,
		onBroadcaster: s.BroadcasterSet,
	}
	return s
}
