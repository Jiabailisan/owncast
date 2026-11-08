package admin

// Admin configuration API for the Phase 2 SRT and WebRTC features.
//
// The handlers follow the contract in docs/architecture/api-contract.md:
//   - GET returns the current configuration, the model defaults, pending
//     restarts and the live service/listener status;
//   - PUT merges a partial update onto the stored configuration, validates it
//     with the authoritative validators in config/settings, applies it through
//     the owning service, and only then persists it;
//   - secrets are write-only: the response contains <secret>Set booleans and
//     an omitted secret keeps the stored value.
//
// Everything here is additive: no existing endpoint or payload is changed.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config/settings"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/persistence/configrepository"
)

// maxConfigBodyBytes bounds a config PUT/POST body. The configs are small and
// admin-only; a generous cap still protects against accidental huge payloads.
const maxConfigBodyBytes = 1 << 20

// Response map keys and repeated diagnostics strings, kept as constants for
// consistency and to satisfy goconst.
const (
	responseKeySuccess        = "success"
	responseKeyMessage        = "message"
	responseKeyEnabled        = "enabled"
	responseKeyPendingRestart = "pendingRestart"
	warningCodeProbeFailed    = "ffmpeg_probe_failed"
	preflightTargetAll        = "all"
	preflightTargetSRT        = "srt"
	preflightTargetWebRTC     = "webrtc"
	portProtocolUDP           = "udp"
	portProtocolTCP           = "tcp"
	fieldNameDomain           = "domain"
)

// pendingRestart is the shared response shape for settings that need a restart.
type pendingRestart struct {
	Pending bool     `json:"pending"`
	Fields  []string `json:"fields"`
}

// newPendingRestart builds a non-nil pending restart block.
func newPendingRestart(fields []string) pendingRestart {
	if fields == nil {
		fields = []string{}
	}
	return pendingRestart{Pending: len(fields) > 0, Fields: fields}
}

// writeLiveConfigJSON writes a 200 JSON response.
func writeLiveConfigJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Errorln("unable to write live streaming config response:", err)
	}
}

// writeLiveConfigValidationErrors writes the contract's 400 response with a
// non-nil fieldErrors array.
func writeLiveConfigValidationErrors(w http.ResponseWriter, fieldErrors settings.FieldErrors) {
	if fieldErrors == nil {
		fieldErrors = settings.FieldErrors{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		responseKeySuccess: false,
		responseKeyMessage: "validation failed",
		"fieldErrors":      fieldErrors,
	})
}

// writeLiveConfigBadRequest writes a 400 with a message and no field errors;
// used for malformed JSON bodies.
func writeLiveConfigBadRequest(w http.ResponseWriter, message string) {
	writeLiveConfigValidationErrors(w, settings.FieldErrors{{Field: "body", Message: message}})
}

// writeLiveConfigConflict writes the 409 used when a service cannot apply the
// new configuration and the previous one stays active.
func writeLiveConfigConflict(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{responseKeySuccess: false, "message": message})
}

// writeLiveConfigInternalError writes a 500 response.
func writeLiveConfigInternalError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{responseKeySuccess: false, "message": message})
}

// readConfigBody reads and bounds a JSON request body.
func readConfigBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBodyBytes))
	if err != nil {
		writeLiveConfigBadRequest(w, "unable to read request body: "+err.Error())
		return nil, false
	}
	return body, true
}

// jsonMap converts a struct to a JSON-shaped map for response composition.
func jsonMap(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return map[string]any{}
	}
	return decoded
}

// srtConfigPublic returns the admin-visible SRT configuration: every field
// except the passphrase, plus a passphraseSet boolean.
func srtConfigPublic(config models.SRTConfig) map[string]any {
	public := jsonMap(config)
	delete(public, "passphrase")
	public["passphraseSet"] = config.Passphrase != ""
	return public
}

// webrtcConfigPublic returns the admin-visible WebRTC configuration with every
// TURN credential replaced by a set boolean.
func webrtcConfigPublic(config models.WebRTCConfig) map[string]any {
	public := jsonMap(config)
	turnServers := make([]map[string]any, 0, len(config.TURNServers))
	for _, server := range config.TURNServers {
		turnServers = append(turnServers, map[string]any{
			"url":             server.URL,
			"username":        server.Username,
			"credentialSet":   server.Credential != "",
			"staticSecretSet": server.StaticSecret != "",
		})
	}
	public["turnServers"] = turnServers
	return public
}

// changedRestartFields returns the keys whose values differ between before and
// after and whose apply mode requires at least a stream restart. Live settings
// are not pending: they are already in effect.
func changedRestartFields(before, after any, metadata []settings.Field) []string {
	beforeMap := jsonMap(before)
	afterMap := jsonMap(after)
	fields := make([]string, 0, len(metadata))
	for _, field := range metadata {
		if field.ReadOnly || field.ApplyMode == settings.ApplyLive {
			continue
		}
		if !reflect.DeepEqual(beforeMap[field.Key], afterMap[field.Key]) {
			fields = append(fields, field.Key)
		}
	}
	sort.Strings(fields)
	return fields
}

// mergeSRTConfig applies a partial JSON update onto the current configuration.
// Omitted keys keep their value; an omitted passphrase keeps the stored secret
// and an explicit empty string clears it.
func mergeSRTConfig(current models.SRTConfig, body []byte) (models.SRTConfig, error) {
	raw := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(body))) != 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			return current, err
		}
	}

	passphrase, passphrasePresent := raw["passphrase"]
	delete(raw, "passphrase")
	delete(raw, "passphraseSet")

	if len(raw) != 0 {
		partial, err := json.Marshal(raw)
		if err != nil {
			return current, err
		}
		// Unmarshalling into the existing value merges: only present keys
		// overwrite fields.
		if err := json.Unmarshal(partial, &current); err != nil {
			return current, err
		}
	}
	if passphrasePresent {
		var value string
		if err := json.Unmarshal(passphrase, &value); err != nil {
			return current, err
		}
		current.Passphrase = value
	}

	return settings.WithSRTDefaults(current), nil
}

// mergeWebRTCConfig applies a partial JSON update onto the current
// configuration. Omitted TURN secrets keep the stored value, matched by URL.
func mergeWebRTCConfig(current models.WebRTCConfig, body []byte) (models.WebRTCConfig, error) {
	raw := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(body))) != 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			return current, err
		}
	}

	turnServersRaw, turnServersPresent := raw["turnServers"]
	delete(raw, "turnServers")

	// opusFEC and opusDTX are fixed (read-only) settings: no FFmpeg argv can
	// apply them, so they are not accepted update fields. Ignore attempts to
	// change them rather than persisting a value that would never take effect.
	delete(raw, "opusFEC")
	delete(raw, "opusDTX")

	if len(raw) != 0 {
		partial, err := json.Marshal(raw)
		if err != nil {
			return current, err
		}
		if err := json.Unmarshal(partial, &current); err != nil {
			return current, err
		}
	}
	if turnServersPresent {
		merged, err := mergeTURNServers(current.TURNServers, turnServersRaw)
		if err != nil {
			return current, err
		}
		current.TURNServers = merged
	}

	return settings.WithWebRTCDefaults(current), nil
}

// turnServerUpdate is the write shape of one TURN entry: secrets are pointers
// so an omitted secret is distinguishable from an explicit empty string.
type turnServerUpdate struct {
	URL          string  `json:"url"`
	Username     string  `json:"username"`
	Credential   *string `json:"credential"`
	StaticSecret *string `json:"staticSecret"`
}

// mergeTURNServers merges the updated list with the stored list, carrying over
// omitted secrets from the entry with the same URL.
func mergeTURNServers(current []models.TURNServer, raw json.RawMessage) ([]models.TURNServer, error) {
	var updates []turnServerUpdate
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, err
	}
	merged := make([]models.TURNServer, 0, len(updates))
	for _, update := range updates {
		server := models.TURNServer{URL: update.URL, Username: update.Username}
		for _, existing := range current {
			if existing.URL != update.URL {
				continue
			}
			if server.Username == "" {
				server.Username = existing.Username
			}
			if update.Credential == nil {
				server.Credential = existing.Credential
			}
			if update.StaticSecret == nil {
				server.StaticSecret = existing.StaticSecret
			}
			break
		}
		if update.Credential != nil {
			server.Credential = *update.Credential
		}
		if update.StaticSecret != nil {
			server.StaticSecret = *update.StaticSecret
		}
		merged = append(merged, server)
	}
	return merged, nil
}

// validationContext builds the cross-field context for the settings
// validators from the currently persisted configuration.
func (a *Admin) validationContext(srtConfig *models.SRTConfig, webrtcConfig *models.WebRTCConfig) settings.ValidationContext {
	context := settings.ValidationContext{
		WebServerPort: a.cfg.WebServerPort,
		RTMPPort:      a.configRepository.GetRTMPPortNumber(),
	}
	if srtConfig != nil {
		context.SRT = srtConfig
	}
	if webrtcConfig != nil {
		context.WebRTC = webrtcConfig
	}
	return context
}

// srtListenerState returns the live listener block for the SRT config API.
func (a *Admin) srtListenerState() map[string]any {
	if a.srt == nil {
		return map[string]any{
			responseKeyEnabled:        false,
			"state":                   "stopped",
			"listening":               false,
			"port":                    0,
			"boundAddress":            "",
			"lastError":               "",
			responseKeyPendingRestart: false,
			"activeSession":           nil,
		}
	}

	status := a.srt.Snapshot()
	listener := map[string]any{
		responseKeyEnabled:        status.Enabled,
		"state":                   string(status.State),
		"listening":               status.Listening,
		"port":                    status.Port,
		"boundAddress":            status.BoundAddress,
		"lastError":               status.LastError,
		responseKeyPendingRestart: status.PendingRestart,
		"activeSession":           nil,
	}
	if status.Session != nil {
		listener["activeSession"] = map[string]any{
			"id":              status.Session.ID,
			"remoteAddr":      status.Session.RemoteAddr,
			"streamKeyID":     status.Session.StreamKeyID,
			"inputFormat":     status.Session.InputFormat,
			"startedAt":       status.Session.StartedAt,
			"durationSeconds": status.Session.Duration.Seconds(),
			"payloadBytes":    status.Session.PayloadBytes,
			"version":         status.Session.Version,
			"encrypted":       status.Session.Encrypted,
		}
	}
	return listener
}

// webrtcServiceState returns the live WebRTC service block for the config API.
func (a *Admin) webrtcServiceState(config models.WebRTCConfig) map[string]any {
	if a.webrtc == nil {
		return map[string]any{
			"enabled":          config.Enabled,
			"listeningAddress": "",
			"activeSessions":   0,
			"capacity": map[string]any{
				"used":  0,
				"limit": config.MaxViewers,
			},
			"warnings": []string{},
		}
	}
	return jsonMap(a.webrtc.Snapshot())
}

// srtConfigState builds the GET/PUT response for the SRT configuration. A
// changed restart-service field is only pending when the SRT service reports
// that it deferred the rebind (for example because a publisher is connected);
// a successfully applied change is not pending.
func (a *Admin) srtConfigState(config models.SRTConfig, changedRestartFields []string) map[string]any {
	pending := newPendingRestart(nil)
	if a.srt != nil && a.srt.Snapshot().PendingRestart {
		pending = pendingRestart{Pending: true, Fields: changedRestartFields}
	}
	return map[string]any{
		"config":         srtConfigPublic(config),
		"defaults":       srtConfigPublic(models.DefaultSRTConfig()),
		"pendingRestart": pending,
		"listener":       a.srtListenerState(),
	}
}

// webrtcConfigState builds the GET response for the WebRTC configuration.
func (a *Admin) webrtcConfigState(config models.WebRTCConfig, pendingFields []string) map[string]any {
	return map[string]any{
		"config":         webrtcConfigPublic(config),
		"defaults":       webrtcConfigPublic(models.DefaultWebRTCConfig()),
		"pendingRestart": newPendingRestart(pendingFields),
		"service":        a.webrtcServiceState(config),
	}
}

// GetSettingsMetadata returns the settings metadata registry payload.
func (a *Admin) GetSettingsMetadata(w http.ResponseWriter, _ *http.Request) {
	writeLiveConfigJSON(w, settings.All())
}

// GetSRTConfig returns the current SRT configuration, defaults, pending restart
// state and live listener status.
func (a *Admin) GetSRTConfig(w http.ResponseWriter, _ *http.Request) {
	config := a.configRepository.GetSRTConfig()
	writeLiveConfigJSON(w, a.srtConfigState(config, nil))
}

// UpdateSRTConfig validates, applies and persists a partial SRT configuration
// update. The previous configuration stays active on a validation or bind
// failure.
func (a *Admin) UpdateSRTConfig(w http.ResponseWriter, r *http.Request) {
	body, ok := readConfigBody(w, r)
	if !ok {
		return
	}

	current := a.configRepository.GetSRTConfig()
	updated, err := mergeSRTConfig(current, body)
	if err != nil {
		writeLiveConfigBadRequest(w, "unable to parse SRT configuration: "+err.Error())
		return
	}

	webrtcConfig := a.configRepository.GetWebRTCConfig()
	context := a.validationContext(nil, &webrtcConfig)
	if fieldErrors := settings.ValidateSRT(updated, context); len(fieldErrors) != 0 {
		writeLiveConfigValidationErrors(w, fieldErrors)
		return
	}

	if a.srt != nil {
		if err := a.srt.ApplyConfig(updated); err != nil {
			log.Warnln("srt config apply failed:", err)
			writeLiveConfigConflict(w, err.Error())
			return
		}
	}
	if err := a.configRepository.SetSRTConfig(updated); err != nil {
		writeLiveConfigInternalError(w, "unable to persist SRT configuration: "+err.Error())
		return
	}

	pendingFields := changedRestartFields(current, updated, settings.SRTMetadata())
	state := a.srtConfigState(updated, pendingFields)
	state[responseKeySuccess] = true
	state[responseKeyMessage] = "SRT configuration updated"
	writeLiveConfigJSON(w, state)
}

// GetWebRTCConfig returns the current WebRTC configuration, defaults, pending
// restart state and live service status.
func (a *Admin) GetWebRTCConfig(w http.ResponseWriter, _ *http.Request) {
	config := a.configRepository.GetWebRTCConfig()
	pendingFields := []string{}
	if a.webrtc != nil {
		applied := a.webrtc.Config()
		pendingFields = changedRestartFields(applied, config, settings.WebRTCMetadata())
	}
	writeLiveConfigJSON(w, a.webrtcConfigState(config, pendingFields))
}

// webrtcRestartTimeout bounds one bounded WebRTC service stop/start cycle.
const webrtcRestartTimeout = 20 * time.Second

// restartWebRTCService stops and starts the WebRTC service so restart-service
// settings can be applied. The service is idempotent for Start and serializes
// its own lifecycle; the handler only decides when a restart is required.
func (a *Admin) restartWebRTCService() error {
	stopContext, stopCancel := context.WithTimeout(context.Background(), webrtcRestartTimeout)
	defer stopCancel()
	if err := a.webrtc.Stop(stopContext); err != nil {
		return fmt.Errorf("stopping the WebRTC service: %w", err)
	}

	startContext, startCancel := context.WithTimeout(context.Background(), webrtcRestartTimeout)
	defer startCancel()
	if err := a.webrtc.Start(startContext); err != nil {
		return fmt.Errorf("starting the WebRTC service: %w", err)
	}
	return nil
}

// restoreWebRTCConfig re-applies the previous configuration and restarts the
// service so a failed new configuration cannot leave WebRTC down.
func (a *Admin) restoreWebRTCConfig(previous models.WebRTCConfig) {
	if _, err := a.webrtc.UpdateConfig(previous); err != nil {
		log.Errorln("unable to restore the previous WebRTC configuration:", err)
		return
	}
	if err := a.restartWebRTCService(); err != nil {
		log.Errorln("unable to restore the previous WebRTC service state:", err)
	}
}

// UpdateWebRTCConfig validates, applies and persists a partial WebRTC
// configuration update.
func (a *Admin) UpdateWebRTCConfig(w http.ResponseWriter, r *http.Request) {
	body, ok := readConfigBody(w, r)
	if !ok {
		return
	}

	current := a.configRepository.GetWebRTCConfig()
	updated, err := mergeWebRTCConfig(current, body)
	if err != nil {
		writeLiveConfigBadRequest(w, "unable to parse WebRTC configuration: "+err.Error())
		return
	}

	srtConfig := a.configRepository.GetSRTConfig()
	context := a.validationContext(&srtConfig, nil)
	if fieldErrors := settings.ValidateWebRTC(updated, context); len(fieldErrors) != 0 {
		writeLiveConfigValidationErrors(w, fieldErrors)
		return
	}

	pendingFields := changedRestartFields(current, updated, settings.WebRTCMetadata())
	if a.webrtc != nil {
		previousConfig := a.webrtc.Config()
		pending, err := a.webrtc.UpdateConfig(updated)
		if err != nil {
			log.Warnln("webrtc config apply failed:", err)
			writeLiveConfigValidationErrors(w, settings.FieldErrors{{Field: "body", Message: err.Error()}})
			return
		}
		if len(pending) != 0 {
			// restart-service settings: restart only the WebRTC service now.
			if restartErr := a.restartWebRTCService(); restartErr != nil {
				log.Warnln("webrtc service restart failed:", restartErr)
				a.restoreWebRTCConfig(previousConfig)
				writeLiveConfigConflict(w, "unable to restart the WebRTC service with the new configuration: "+restartErr.Error())
				return
			}
			pending = []string{}
		}
		pendingFields = pending
	}
	if err := a.configRepository.SetWebRTCConfig(updated); err != nil {
		writeLiveConfigInternalError(w, "unable to persist WebRTC configuration: "+err.Error())
		return
	}

	state := a.webrtcConfigState(updated, pendingFields)
	state[responseKeySuccess] = true
	state[responseKeyMessage] = "WebRTC configuration updated"
	writeLiveConfigJSON(w, state)
}

// ffmpegCapabilities is the FFmpeg build probe result.
type ffmpegCapabilities struct {
	Path         string   `json:"path"`
	Version      string   `json:"version"`
	H264Encoders []string `json:"h264Encoders"`
	OpusEncoder  bool     `json:"opusEncoder"`
	SRTProtocol  bool     `json:"srtProtocol"`
	Warnings     []string `json:"warnings"`
}

// portProbe is one port availability check.
type portProbe struct {
	Protocol  string `json:"protocol"`
	Port      int    `json:"port"`
	Available bool   `json:"available"`
}

// preflightWarning is a machine-readable preflight warning.
type preflightWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// preflightRequest is the optional body of the preflight endpoint.
type preflightRequest struct {
	Target string `json:"target"`
}

// resolveFFmpegPath returns the configured FFmpeg path, falling back to PATH.
func resolveFFmpegPath(configRepository configrepository.ConfigRepository) string {
	if path := configRepository.GetFfMpegPath(); path != "" {
		return path
	}
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path
	}
	return ""
}

// probeFFmpeg inspects the configured FFmpeg build for the capabilities the
// SRT and WebRTC pipelines require.
func probeFFmpeg(configRepository configrepository.ConfigRepository) (ffmpegCapabilities, []preflightWarning) {
	capabilities := ffmpegCapabilities{
		H264Encoders: []string{},
		Warnings:     []string{},
	}
	warnings := make([]preflightWarning, 0)

	path := resolveFFmpegPath(configRepository)
	if path == "" {
		warnings = append(warnings, preflightWarning{
			Code:    "ffmpeg_missing",
			Message: "No FFmpeg binary found. Configure Configuration -> Server Setup -> FFmpeg path.",
		})
		capabilities.Warnings = append(capabilities.Warnings, "No FFmpeg binary found")
		return capabilities, warnings
	}
	capabilities.Path = path

	versionOutput, err := exec.Command(path, "-hide_banner", "-version").Output() //nolint:gosec
	if err != nil {
		warnings = append(warnings, preflightWarning{Code: warningCodeProbeFailed, Message: "Unable to run ffmpeg -version: " + err.Error()})
		capabilities.Warnings = append(capabilities.Warnings, "Unable to run ffmpeg -version")
	} else {
		capabilities.Version = parseFFmpegVersion(string(versionOutput))
	}

	encodersOutput, err := exec.Command(path, "-hide_banner", "-encoders").Output() //nolint:gosec
	if err != nil {
		warnings = append(warnings, preflightWarning{Code: warningCodeProbeFailed, Message: "Unable to run ffmpeg -encoders: " + err.Error()})
	} else {
		capabilities.H264Encoders, capabilities.OpusEncoder = parseFFmpegEncoders(string(encodersOutput))
	}

	protocolsOutput, err := exec.Command(path, "-hide_banner", "-protocols").Output() //nolint:gosec
	if err != nil {
		warnings = append(warnings, preflightWarning{Code: warningCodeProbeFailed, Message: "Unable to run ffmpeg -protocols: " + err.Error()})
	} else {
		capabilities.SRTProtocol = hasFFmpegProtocol(string(protocolsOutput), "srt")
	}

	if len(capabilities.H264Encoders) == 0 {
		warnings = append(warnings, preflightWarning{Code: "no_h264_encoder", Message: "No H.264 encoder found in the configured FFmpeg; WebRTC delivery requires one."})
		capabilities.Warnings = append(capabilities.Warnings, "No H.264 encoder")
	}
	if !capabilities.OpusEncoder {
		warnings = append(warnings, preflightWarning{Code: "no_opus_encoder", Message: "libopus is missing from the configured FFmpeg; WebRTC audio requires it."})
		capabilities.Warnings = append(capabilities.Warnings, "libopus missing")
	}
	if !capabilities.SRTProtocol {
		warnings = append(warnings, preflightWarning{Code: "no_srt_protocol", Message: "The configured FFmpeg does not report SRT protocol support."})
		capabilities.Warnings = append(capabilities.Warnings, "SRT protocol missing")
	}
	if !hasHardwareH264Encoder(capabilities.H264Encoders) {
		warnings = append(warnings, preflightWarning{Code: "no_hw_encoder", Message: "No hardware H.264 encoder detected; software encoding will be used."})
	}

	return capabilities, warnings
}

// parseFFmpegVersion extracts the version token from ffmpeg -version output.
func parseFFmpegVersion(output string) string {
	firstLine := strings.SplitN(output, "\n", 2)[0]
	fields := strings.Fields(firstLine)
	for index, field := range fields {
		if field == "version" && index+1 < len(fields) {
			return fields[index+1]
		}
	}
	return ""
}

// parseFFmpegEncoders extracts H.264 encoder names and whether libopus exists.
func parseFFmpegEncoders(output string) ([]string, bool) {
	h264Encoders := make([]string, 0)
	opus := false
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "libopus") || strings.Contains(lower, " opus ") {
			opus = true
		}
		if !strings.Contains(line, "H.264") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			h264Encoders = append(h264Encoders, fields[1])
		}
	}
	sort.Strings(h264Encoders)
	return h264Encoders, opus
}

// hasFFmpegProtocol reports whether the ffmpeg -protocols output lists the
// given protocol name exactly.
func hasFFmpegProtocol(output, protocol string) bool {
	for _, line := range strings.Split(output, "\n") {
		for _, field := range strings.Fields(line) {
			if field == protocol {
				return true
			}
		}
	}
	return false
}

// hasHardwareH264Encoder reports whether any known hardware encoder name is
// present in the list.
func hasHardwareH264Encoder(encoders []string) bool {
	for _, encoder := range encoders {
		for _, prefix := range []string{"h264_nvenc", "h264_vaapi", "h264_qsv", "h264_videotoolbox", "h264_amf", "h264_omx", "h264_v4l2m2m"} {
			if encoder == prefix {
				return true
			}
		}
	}
	return false
}

// probeUDPPort reports whether a UDP port can be bound on the given address.
func probeUDPPort(bindAddress string, port int) bool {
	address := &net.UDPAddr{Port: port}
	if bindAddress != "" {
		address.IP = net.ParseIP(bindAddress)
	}
	connection, err := net.ListenUDP("udp", address)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

// probeTCPPort reports whether a TCP port can be bound on the given address.
func probeTCPPort(bindAddress string, port int) bool {
	address := net.JoinHostPort(bindAddress, strconv.Itoa(port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// RunConfigPreflight probes the configured FFmpeg build and ports.
func (a *Admin) RunConfigPreflight(w http.ResponseWriter, r *http.Request) {
	request := preflightRequest{Target: preflightTargetAll}
	if body, ok := readConfigBody(w, r); ok && len(strings.TrimSpace(string(body))) != 0 {
		if err := json.Unmarshal(body, &request); err != nil {
			writeLiveConfigBadRequest(w, "unable to parse preflight request: "+err.Error())
			return
		}
	}
	if request.Target == "" {
		request.Target = preflightTargetAll
	}
	if request.Target != preflightTargetAll && request.Target != preflightTargetSRT && request.Target != preflightTargetWebRTC {
		writeLiveConfigValidationErrors(w, settings.FieldErrors{{Field: "target", Message: "must be one of all, srt, webrtc"}})
		return
	}

	capabilities, warnings := probeFFmpeg(a.configRepository)
	ports := a.probeConfiguredPorts(request.Target)
	warnings = append(warnings, portWarnings(ports)...)

	writeLiveConfigJSON(w, map[string]any{
		"ffmpeg":   capabilities,
		"ports":    ports,
		"warnings": warnings,
	})
}

// probeConfiguredPorts probes the ports belonging to the requested target.
func (a *Admin) probeConfiguredPorts(target string) []portProbe {
	ports := make([]portProbe, 0, 4)
	if target == preflightTargetAll || target == preflightTargetSRT {
		srtConfig := a.configRepository.GetSRTConfig()
		ports = append(ports, portProbe{
			Protocol:  portProtocolUDP,
			Port:      srtConfig.Port,
			Available: probeUDPPort(srtConfig.BindAddress, srtConfig.Port),
		})
	}
	if target != preflightTargetAll && target != preflightTargetWebRTC {
		return ports
	}

	webrtcConfig := a.configRepository.GetWebRTCConfig()
	ports = append(ports, portProbe{
		Protocol:  portProtocolUDP,
		Port:      webrtcConfig.UDPPort,
		Available: probeUDPPort(webrtcConfig.BindAddress, webrtcConfig.UDPPort),
	})
	if webrtcConfig.TCPEnabled {
		ports = append(ports, portProbe{
			Protocol:  portProtocolTCP,
			Port:      webrtcConfig.TCPPort,
			Available: probeTCPPort(webrtcConfig.BindAddress, webrtcConfig.TCPPort),
		})
	}
	if webrtcConfig.UDPMode == "range" {
		ports = append(
			ports,
			portProbe{Protocol: portProtocolUDP, Port: webrtcConfig.UDPPortMin, Available: probeUDPPort(webrtcConfig.BindAddress, webrtcConfig.UDPPortMin)},
			portProbe{Protocol: portProtocolUDP, Port: webrtcConfig.UDPPortMax, Available: probeUDPPort(webrtcConfig.BindAddress, webrtcConfig.UDPPortMax)},
		)
	}
	return ports
}

// portWarnings turns unavailable ports into preflight warnings.
func portWarnings(ports []portProbe) []preflightWarning {
	warnings := make([]preflightWarning, 0)
	for _, port := range ports {
		if port.Available {
			continue
		}
		warnings = append(warnings, preflightWarning{
			Code:    "port_in_use",
			Message: port.Protocol + " port " + strconv.Itoa(port.Port) + " is unavailable",
		})
	}
	return warnings
}

// testDomainRequest is the body of the test-domain endpoint.
type testDomainRequest struct {
	Domain string `json:"domain"`
}

// TestWebRTCDomain resolves the given WebRTC domain and reports the ICE
// addresses that would be advertised.
func (a *Admin) TestWebRTCDomain(w http.ResponseWriter, r *http.Request) {
	body, ok := readConfigBody(w, r)
	if !ok {
		return
	}
	request := testDomainRequest{}
	if len(strings.TrimSpace(string(body))) != 0 {
		if err := json.Unmarshal(body, &request); err != nil {
			writeLiveConfigBadRequest(w, "unable to parse domain request: "+err.Error())
			return
		}
	}
	config := a.configRepository.GetWebRTCConfig()
	domain := request.Domain
	if domain == "" {
		domain = config.Domain
	}
	if domain == "" {
		writeLiveConfigValidationErrors(w, settings.FieldErrors{{Field: fieldNameDomain, Message: "a domain is required"}})
		return
	}
	if err := settings.ValidateDomain(domain); err != nil {
		writeLiveConfigValidationErrors(w, settings.FieldErrors{{Field: fieldNameDomain, Message: err.Error()}})
		return
	}

	context, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	resolvedIPs, err := net.DefaultResolver.LookupHost(context, domain)
	if resolvedIPs == nil {
		resolvedIPs = []string{}
	}
	sort.Strings(resolvedIPs)

	advertisedIPs := append([]string{}, resolvedIPs...)
	if len(config.PublicIPs) != 0 {
		advertisedIPs = append([]string{}, config.PublicIPs...)
	}
	sort.Strings(advertisedIPs)

	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}

	writeLiveConfigJSON(w, map[string]any{
		"domain":        domain,
		"resolvedIPs":   resolvedIPs,
		"advertisedIPs": advertisedIPs,
		"strictDomain":  config.StrictDomain,
		"error":         errorMessage,
	})
}
