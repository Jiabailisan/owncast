package webrtc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/utils"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/stats"
	"github.com/pion/webrtc/v4"
	log "github.com/sirupsen/logrus"
)

const (
	whepPathPrefix    = "/webrtc"
	whepEndpoint      = "/whep"
	sessionPathPrefix = "/whep/session/"
	maxOfferBytes     = 256 * 1024
	maxFragmentBytes  = 64 * 1024
	iceGatherTimeout  = 5 * time.Second
	sessionIDBytes    = 16
)

type whepHandler struct {
	service *Service
}

func newWHEPHandler(service *Service) http.Handler {
	return &whepHandler{service: service}
}

func (h *whepHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, whepPathPrefix)
	if path == "" {
		path = "/"
	}

	switch {
	case path == whepEndpoint || path == whepEndpoint+"/":
		h.serveWHEP(w, r)
	case strings.HasPrefix(path, sessionPathPrefix):
		h.serveSession(w, r, strings.TrimPrefix(path, sessionPathPrefix))
	default:
		h.writeError(w, r, http.StatusNotFound, CodeSessionNotFound, "unknown webrtc endpoint", 0)
	}
}

func (h *whepHandler) serveWHEP(w http.ResponseWriter, r *http.Request) {
	cfg := h.service.Config()
	h.writeCORS(w, r, cfg)

	if r.Method == http.MethodOptions {
		h.writePreflight(w)

		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "OPTIONS, POST")
		h.writeError(w, r, http.StatusMethodNotAllowed, CodeUnsupported, "method not allowed", 0)

		return
	}
	if !cfg.Enabled {
		h.writeError(w, r, http.StatusServiceUnavailable, CodeDisabled, "webrtc delivery is disabled", 0)

		return
	}
	if err := checkRequestOrigin(r, cfg); err != nil {
		h.writeError(w, r, http.StatusForbidden, CodeDomainMismatch, err.Error(), 0)

		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/sdp" {
		h.writeError(w, r, http.StatusUnsupportedMediaType, CodeUnsupported, "Content-Type must be application/sdp", 0)

		return
	}

	// Resolve the client address once: the rate limit, the capacity slot and the
	// session record must all use the same value, or one client could be counted
	// under two keys.
	clientIP := h.service.clientIP(r)

	if !h.service.limiter.allow(clientIP) {
		retryAfter := 60 / max(1, cfg.SignalingRateLimit)
		h.writeError(w, r, http.StatusTooManyRequests, CodeRateLimited, "signaling rate limit exceeded", retryAfter)

		return
	}

	offer, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOfferBytes))
	if err != nil {
		h.writeError(w, r, http.StatusRequestEntityTooLarge, CodeBadOffer, "offer too large", 0)

		return
	}

	variantIndex, err := h.selectedVariant(r)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, CodeVariantNotFound, err.Error(), 0)

		return
	}

	sess, answer, err := h.service.createViewerSession(r, clientIP, variantIndex, string(offer))
	if err != nil {
		h.writeSessionError(w, r, err)

		return
	}

	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", whepPathPrefix+sessionPathPrefix+sess.id)
	w.Header().Set("ETag", "\""+sess.id+"-1\"")
	// The server picks the variant when the client did not pin one, so it tells the
	// client which rendition this session got; without it a WebRTC viewer cannot
	// name the quality it is actually watching.
	w.Header().Set(variantHeader, strconv.Itoa(variantIndex))
	for _, link := range buildLinkHeaders(cfg) {
		w.Header().Add("Link", link)
	}
	w.WriteHeader(http.StatusCreated)
	if _, err := io.WriteString(w, answer.SDP); err != nil {
		log.Debugf("webrtc: writing whep answer: %v", err)
	}
}

func (h *whepHandler) serveSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	cfg := h.service.Config()
	h.writeCORS(w, r, cfg)

	if r.Method == http.MethodOptions {
		h.writePreflight(w)

		return
	}
	if sessionID == "" {
		h.writeError(w, r, http.StatusNotFound, CodeSessionNotFound, "unknown session", 0)

		return
	}
	// Do not reject DELETE/PATCH when cfg.Enabled is false: disabling WebRTC
	// blocks new POST offers in serveOffer, while existing sessions must still
	// be able to finish trickle ICE or tear down cleanly.
	if err := checkRequestOrigin(r, cfg); err != nil {
		h.writeError(w, r, http.StatusForbidden, CodeDomainMismatch, err.Error(), 0)

		return
	}

	switch r.Method {
	case http.MethodDelete:
		h.service.dropSessionByID(sessionID, "client delete")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		h.patchSession(w, r, sessionID)
	default:
		w.Header().Set("Allow", "OPTIONS, PATCH, DELETE")
		h.writeError(w, r, http.StatusMethodNotAllowed, CodeUnsupported, "method not allowed", 0)
	}
}

func (h *whepHandler) patchSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != trickleContentType {
		h.writeError(w, r, http.StatusUnsupportedMediaType, CodeUnsupported,
			"Content-Type must be application/trickle-ice-sdpfrag", 0)

		return
	}

	sess, ok := h.service.sessions.get(sessionID)
	if !ok {
		h.writeError(w, r, http.StatusNotFound, CodeSessionNotFound, "unknown session", 0)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFragmentBytes))
	if err != nil {
		h.writeError(w, r, http.StatusRequestEntityTooLarge, CodeBadOffer, "fragment too large", 0)

		return
	}

	candidates, err := parseTrickleFragment(string(body))
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, CodeBadOffer, err.Error(), 0)

		return
	}
	for _, candidate := range candidates {
		if addErr := sess.pc.AddICECandidate(candidate); addErr != nil {
			h.writeError(w, r, http.StatusBadRequest, CodeBadOffer, "invalid ice candidate", 0)

			return
		}
	}
	sess.touch()
	w.WriteHeader(http.StatusNoContent)
}

// variantHeader reports the variant index a WHEP session was created for.
const variantHeader = "X-Owncast-Variant"

func (h *whepHandler) selectedVariant(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("variant"))
	if raw == "" {
		index, ok := h.service.firstVariant()
		if !ok {
			return 0, errors.New("no webrtc variant is enabled")
		}

		return index, nil
	}

	index, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid variant %q", raw)
	}
	// A variant that is configured but whose RTP leg is not ready is refused here,
	// on the same predicate the default selection and the final registration use.
	if _, ok := h.service.deliverableVariant(index); !ok {
		return 0, fmt.Errorf("variant %d is not webrtc-enabled", index)
	}

	return index, nil
}

func (h *whepHandler) writeSessionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrDisabled):
		h.writeError(w, r, http.StatusServiceUnavailable, CodeDisabled, "webrtc delivery is disabled", 0)
	case errors.Is(err, ErrCapacity):
		h.writeError(w, r, http.StatusServiceUnavailable, CodeCapacity, "webrtc capacity exhausted", 2)
	case errors.Is(err, ErrPerIPCapacity):
		h.writeError(w, r, http.StatusServiceUnavailable, CodePerIPCapacity, "per-ip webrtc capacity exhausted", 2)
	case errors.Is(err, ErrVariantNotFound):
		h.writeError(w, r, http.StatusBadRequest, CodeVariantNotFound, "variant not found", 0)
	case errors.Is(err, ErrUnsupportedMedia), errors.Is(err, ErrCodecMismatch):
		h.writeError(w, r, http.StatusUnprocessableEntity, CodeUnsupportedMedia, err.Error(), 0)
	case errors.Is(err, ErrBadOffer):
		h.writeError(w, r, http.StatusBadRequest, CodeBadOffer, err.Error(), 0)
	case errors.Is(err, ErrICEGatherTimeout):
		h.writeError(w, r, http.StatusServiceUnavailable, CodeICEGatherTimeout, "ice gathering timed out", 1)
	default:
		log.Errorf("webrtc: creating session: %v", err)
		h.writeError(w, r, http.StatusInternalServerError, CodeInternal, "internal webrtc error", 0)
	}
}

func (h *whepHandler) writeCORS(w http.ResponseWriter, r *http.Request, cfg models.WebRTCConfig) {
	origin := allowedOriginHeaderFor(r, cfg)
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Expose-Headers", "Location, ETag, Link, Retry-After, "+variantHeader)
}

func (h *whepHandler) writePreflight(w http.ResponseWriter) {
	w.Header().Set("Accept-Post", "application/sdp")
	w.Header().Set("Access-Control-Allow-Methods", "POST, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-Match")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Set("Allow", "OPTIONS")
	w.WriteHeader(http.StatusNoContent)
}

func (h *whepHandler) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryAfterSeconds int) {
	h.writeCORS(w, r, h.service.Config())
	if retryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]string{"code": code, "message": message}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Debugf("webrtc: writing error response: %v", err)
	}
}

func checkRequestOrigin(r *http.Request, cfg models.WebRTCConfig) error {
	if !cfg.StrictDomain {
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !originAllowed(r, cfg) {
			return errors.New("origin not allowed")
		}

		return nil
	}
	if !hostMatchesDomain(r, cfg) {
		return errors.New("host does not match the configured domain")
	}
	if !originAllowed(r, cfg) {
		return errors.New("origin does not match the configured domain")
	}

	return nil
}

func (s *Service) clientIP(r *http.Request) string {
	if s.deps.ClientIP != nil {
		if ip := s.deps.ClientIP(r); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	return utils.NormalizeIP(host)
}

// negotiatedPeer is the freshly created PeerConnection plus the Pion objects a
// session needs to own after a successful offer/answer exchange.
type negotiatedPeer struct {
	pc          *webrtc.PeerConnection
	getter      stats.Getter
	estimator   cc.BandwidthEstimator
	videoSender *webrtc.RTPSender
	audioSender *webrtc.RTPSender
	answer      *webrtc.SessionDescription
}

func (s *Service) createViewerSession(r *http.Request, clientIP string, variantIndex int, offer string) (*session, *webrtc.SessionDescription, error) {
	cfg := s.Config()
	leg := s.mediaLeg(variantIndex)
	preferredProfile := cfg.H264Profile
	if leg != nil {
		if copyProfile := leg.copyProfile(); copyProfile != "" {
			preferredProfile = copyProfile
		}
	}
	pruned, err := pruneOfferForProfile(offer, preferredProfile)
	if err != nil {
		return nil, nil, err
	}
	tracks, ok := s.deliverableVariant(variantIndex)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %d", ErrVariantNotFound, variantIndex)
	}
	eng := s.engineInstance()
	if eng == nil {
		return nil, nil, ErrDisabled
	}

	reservation, err := s.capacity.reserve(clientIP)
	if err != nil {
		return nil, nil, err
	}
	reservationActive := true
	defer func() {
		if reservationActive {
			s.capacity.releaseReservation(reservation)
		}
	}()

	peer, err := s.negotiate(eng, tracks, pruned)
	if err != nil {
		return nil, nil, err
	}
	// Every path that fails before the session owns the connection has to release
	// the engine's per-viewer entries too, not just close the connection.
	owned := true
	defer func() {
		if owned {
			_ = peer.pc.Close()
			eng.release(peer.pc)
		}
	}()

	sessionID, err := newSessionID()
	if err != nil {
		return nil, nil, err
	}
	sess := newSession(sessionID, tracks, peer.pc, peer.getter, peer.estimator,
		clientIP, r.UserAgent(), peer.videoSender, peer.audioSender)
	// Start the drains before the session becomes reachable through the registry
	// so a concurrent DELETE/janitor cannot close it before its goroutines are
	// tracked by the WaitGroup.
	sess.startRTCPDrains()
	if err := s.registerSessionWithCapacity(sess, reservation); err != nil {
		// Either the variant was withdrawn between negotiation and registration, or
		// the capacity slot could not be taken. The deferred close releases the engine
		// entries and the reservation, so nothing is left behind either way.
		sess.close("session registration failed")

		return nil, nil, err
	}
	reservationActive = false
	owned = false
	rewriteAnswerH264LevelForLeg(peer.answer, s.Config(), s.mediaLeg(variantIndex))
	log.Infof("webrtc: viewer session %s started for variant %d from %s", sess.id, variantIndex, sess.ip)

	return sess, peer.answer, nil
}

var answerH264LevelPattern = regexp.MustCompile("profile-level-id=([0-9a-fA-F]{4})[0-9a-fA-F]{2}")

// rewriteAnswerH264Level raises the level of the answer's H.264 fmtp to the
// level of the encoded stream, while preserving the profile/constraint bytes
// the browser offered. Preserving the profile is required for strict clients:
// Firefox rejects an answer whose profile-level-id does not match the offered
// H.264 payload (42e01f -> 4d0033 causes "no codecs in common"), but accepts
// the same profile with a higher level (42e033).
func rewriteAnswerH264Level(answer *webrtc.SessionDescription, cfg models.WebRTCConfig) {
	rewriteAnswerH264LevelForLeg(answer, cfg, nil)
}

// rewriteAnswerH264LevelForLeg mirrors rewriteAnswerH264Level while ensuring a
// copy leg whose browser-safe source level (e.g. 5.1) exceeds the global
// transcode level setting (e.g. 4.2) advertises at least the source's level.
func rewriteAnswerH264LevelForLeg(answer *webrtc.SessionDescription, cfg models.WebRTCConfig, leg *MediaLeg) {
	if answer == nil {
		return
	}
	levelHex := effectiveAnswerH264LevelHex(cfg, leg)
	answer.SDP = answerH264LevelPattern.ReplaceAllStringFunc(answer.SDP, func(match string) string {
		index := strings.Index(match, "profile-level-id=")
		if index < 0 {
			return match
		}
		prefix := match[:index+len("profile-level-id=")+4]
		return prefix + levelHex
	})
}

func effectiveAnswerH264LevelHex(cfg models.WebRTCConfig, leg *MediaLeg) string {
	baseHex := h264LevelHex(cfg.H264Level, cfg.H264Profile)
	if leg == nil {
		return baseHex
	}
	copyLevel, ok := leg.copyLevel()
	if !ok || copyLevel <= 0 {
		return baseHex
	}
	if copyLevel > browserSafeMaxLevel {
		copyLevel = browserSafeMaxLevel
	}
	baseVal, err := strconv.ParseInt(baseHex, 16, 32)
	if err != nil || copyLevel > int(baseVal) {
		return fmt.Sprintf("%02x", copyLevel)
	}

	return baseHex
}

// negotiate creates the PeerConnection, adds the variant tracks and performs
// the WHEP offer/answer exchange with a bounded ICE gathering wait.
func (s *Service) negotiate(eng *engine, tracks *variantTracks, pruned prunedOffer) (*negotiatedPeer, error) {
	pc, getter, estimator, err := eng.newPeerConnection()
	if err != nil {
		return nil, err
	}
	peer := &negotiatedPeer{pc: pc, getter: getter, estimator: estimator}

	if err := addVariantTracks(peer, tracks); err != nil {
		// The engine registered a stats getter and a bandwidth estimator for this
		// connection; closing it without releasing them leaked both for the life of
		// the process.
		_ = pc.Close()
		eng.release(pc)

		return nil, err
	}
	if err := answerOffer(peer, pruned); err != nil {
		_ = pc.Close()
		eng.release(pc)

		return nil, err
	}

	return peer, nil
}

func addVariantTracks(peer *negotiatedPeer, tracks *variantTracks) error {
	videoSender, err := peer.pc.AddTrack(tracks.video)
	if err != nil {
		return fmt.Errorf("add video track: %w", err)
	}
	audioSender, err := peer.pc.AddTrack(tracks.audio)
	if err != nil {
		return fmt.Errorf("add audio track: %w", err)
	}
	peer.videoSender = videoSender
	peer.audioSender = audioSender

	return nil
}

func answerOffer(peer *negotiatedPeer, pruned prunedOffer) error {
	if err := peer.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  pruned.sdp,
	}); err != nil {
		return fmt.Errorf("%w: set remote description: %v", ErrBadOffer, err)
	}
	answer, err := peer.pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("%w: create answer: %v", ErrBadOffer, err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(peer.pc)
	if err := peer.pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("%w: set local description: %v", ErrBadOffer, err)
	}
	select {
	case <-gatherComplete:
	case <-time.After(iceGatherTimeout):
		return ErrICEGatherTimeout
	}
	localAnswer := peer.pc.LocalDescription()
	if localAnswer == nil {
		return errors.New("webrtc: answer is empty after ice gathering")
	}
	peer.answer = localAnswer

	return nil
}

func (s *Service) engineInstance() *engine {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.engine
}

func newSessionID() (string, error) {
	var buf [sessionIDBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf[:]), nil
}
