package admin

// Appearance customization: custom player module and custom viewer frontend
// configuration, the custom-player load-error sink and the public WebRTC stats
// beacon (master spec 4.6, 7.3, 7.4; docs/architecture/api-contract.md 6-7).
//
// The precedence/validation logic itself lives in config/settings so the
// serving path, the public config API and this admin report share one source
// of truth.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/owncast/owncast/config/settings"
	"github.com/owncast/owncast/services/webrtc"
	"github.com/owncast/owncast/utils"
	webutils "github.com/owncast/owncast/webserver/utils"
)

const (
	customPlayerErrorBodyBytes = 4 << 10
	customPlayerErrorRateLimit = 10
	webrtcStatsRateLimit       = 240
	requestWindowDuration      = time.Minute
	maxCustomPlayerErrorLength = 512
)

// requestWindow is a fixed one-minute request counter for one rate-limit key.
type requestWindow struct {
	windowStart time.Time
	count       int
}

// appearanceState owns the in-memory custom-player load-error sink and the
// small rate limiters for the two public appearance/metrics beacons. Nothing
// here is persisted and client IPs are only used as rate-limit map keys, never
// logged.
type appearanceState struct {
	mu sync.Mutex

	customPlayerError     string
	customPlayerErrorTime time.Time

	windows map[string]requestWindow
}

// newAppearanceState constructs the in-memory appearance beacon state.
func newAppearanceState() *appearanceState {
	return &appearanceState{windows: map[string]requestWindow{}}
}

// allow implements a fixed-window per-key rate limit.
func (s *appearanceState) allow(key string, limit int, now time.Time) bool {
	if key == "" {
		key = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	window := s.windows[key]
	if window.windowStart.IsZero() || now.Sub(window.windowStart) >= requestWindowDuration {
		s.windows[key] = requestWindow{windowStart: now, count: 1}
		return true
	}
	if window.count >= limit {
		return false
	}
	window.count++
	s.windows[key] = window
	return true
}

// recordCustomPlayerError stores the last reported custom-player load failure.
func (s *appearanceState) recordCustomPlayerError(message string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.customPlayerError = message
	s.customPlayerErrorTime = now
}

// loadCustomPlayerError returns the last reported failure, if any.
func (s *appearanceState) loadCustomPlayerError() (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.customPlayerError, s.customPlayerErrorTime
}

// customPlayerLoadErrorReport is the last custom-player module load failure
// surfaced to the admin Appearance page.
type customPlayerLoadErrorReport struct {
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// appearanceAdminReport is the additive Appearance block of the admin config
// API: the computed active frontend mode plus path validation results.
type appearanceAdminReport struct {
	ActiveFrontend      string                       `json:"activeFrontend"`
	CustomPlayerPath    string                       `json:"customPlayerPath"`
	CustomFrontendDir   string                       `json:"customFrontendDir"`
	CustomPlayerValid   bool                         `json:"customPlayerValid"`
	CustomFrontendValid bool                         `json:"customFrontendValid"`
	CustomPlayerError   string                       `json:"customPlayerError,omitempty"`
	CustomFrontendError string                       `json:"customFrontendError,omitempty"`
	LoadError           *customPlayerLoadErrorReport `json:"customPlayerLoadError,omitempty"`
}

// appearanceReport computes the current appearance status.
func (a *Admin) appearanceReport() appearanceAdminReport {
	state := settings.ResolveAppearance(settings.AppearancePaths{
		CustomPlayerPath:  a.configRepository.GetCustomPlayerPath(),
		CustomFrontendDir: a.configRepository.GetCustomFrontendDir(),
	})
	report := appearanceAdminReport{
		ActiveFrontend:      string(state.Mode),
		CustomPlayerPath:    a.configRepository.GetCustomPlayerPath(),
		CustomFrontendDir:   a.configRepository.GetCustomFrontendDir(),
		CustomPlayerValid:   state.PlayerValid,
		CustomFrontendValid: state.FrontendValid,
		CustomPlayerError:   state.PlayerError,
		CustomFrontendError: state.FrontendError,
	}
	if a.appearance != nil {
		if message, at := a.appearance.loadCustomPlayerError(); message != "" {
			report.LoadError = &customPlayerLoadErrorReport{Message: message, At: at}
		}
	}
	return report
}

// sanitizeCustomPlayerErrorMessage makes a client-reported string safe to store
// and display: single line, no control characters, bounded length.
func sanitizeCustomPlayerErrorMessage(message string) string {
	message = strings.Map(func(character rune) rune {
		if character < 32 || character == 127 {
			return ' '
		}
		return character
	}, message)
	message = strings.TrimSpace(message)
	if len(message) > maxCustomPlayerErrorLength {
		message = message[:maxCustomPlayerErrorLength]
	}
	return message
}

// ReportCustomPlayerError records the last custom-player module load failure.
// Public and rate-limited; the payload has no client identifiers.
func (a *Admin) ReportCustomPlayerError(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	clientKey := utils.GetIPAddressFromRequest(r)
	if a.appearance != nil && !a.appearance.allow("custom-player-error:"+clientKey, customPlayerErrorRateLimit, time.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, customPlayerErrorBodyBytes))
	if err != nil {
		webutils.WriteSimpleResponse(w, false, "unable to read request body")
		return
	}
	var request struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		webutils.WriteSimpleResponse(w, false, "unable to parse error report")
		return
	}
	if a.appearance != nil {
		a.appearance.recordCustomPlayerError(sanitizeCustomPlayerErrorMessage(request.Message), time.Now())
	}

	w.WriteHeader(http.StatusNoContent)
}

// webrtcClientReportRequest is the public WebRTC stats beacon body.
type webrtcClientReportRequest struct {
	SessionID       string  `json:"sessionId"`
	DecodedWidth    int     `json:"decodedWidth"`
	DecodedHeight   int     `json:"decodedHeight"`
	FPS             float64 `json:"fps"`
	PacketsLost     int64   `json:"packetsLost"`
	JitterMs        float64 `json:"jitterMs"`
	RoundTripTimeMs float64 `json:"roundTripTimeMs"`
}

// ReportWebRTCClientReport accepts the optional browser playback stats beacon
// and enriches the matching WebRTC viewer row. Unknown sessions are answered
// with 404 without revealing anything; client IPs are never logged.
func (a *Admin) ReportWebRTCClientReport(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	clientKey := utils.GetIPAddressFromRequest(r)
	if a.appearance != nil && !a.appearance.allow("webrtc-stats:"+clientKey, webrtcStatsRateLimit, time.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, customPlayerErrorBodyBytes))
	if err != nil {
		webutils.WriteSimpleResponse(w, false, "unable to read request body")
		return
	}
	request := webrtcClientReportRequest{}
	if err := json.Unmarshal(body, &request); err != nil || request.SessionID == "" {
		webutils.WriteSimpleResponse(w, false, "unable to parse WebRTC stats report")
		return
	}
	if a.webrtc == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	accepted := a.webrtc.ApplyClientReport(request.SessionID, webrtc.ClientReport{
		DecodedWidth:    request.DecodedWidth,
		DecodedHeight:   request.DecodedHeight,
		FPS:             request.FPS,
		PacketsLost:     request.PacketsLost,
		JitterMs:        request.JitterMs,
		RoundTripTimeMs: request.RoundTripTimeMs,
	})
	if !accepted {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// SetCustomPlayerPath validates, stores or clears the custom player module path.
func (a *Admin) SetCustomPlayerPath(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}
	path, ok := configValue.Value.(string)
	if !ok {
		webutils.WriteSimpleResponse(w, false, "custom player path must be a string")
		return
	}

	if path != "" {
		if _, err := settings.ValidatePlayerPath(path); err != nil {
			webutils.WriteSimpleResponse(w, false, err.Error())
			return
		}
	}
	if err := a.configRepository.SetCustomPlayerPath(path); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}
	webutils.WriteSimpleResponse(w, true, "custom player path updated")
}

// SetCustomFrontendDir validates, stores or clears the custom viewer frontend
// directory.
func (a *Admin) SetCustomFrontendDir(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}
	directory, ok := configValue.Value.(string)
	if !ok {
		webutils.WriteSimpleResponse(w, false, "custom frontend directory must be a string")
		return
	}

	if directory != "" {
		if _, err := settings.ValidateFrontendDir(directory); err != nil {
			webutils.WriteSimpleResponse(w, false, err.Error())
			return
		}
	}
	if err := a.configRepository.SetCustomFrontendDir(directory); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}
	webutils.WriteSimpleResponse(w, true, "custom frontend directory updated")
}
