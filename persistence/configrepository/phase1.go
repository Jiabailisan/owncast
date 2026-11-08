package configrepository

// Phase 1 SRT/WebRTC configuration accessors.
//
// The values are stored in the existing key/value datastore using the same
// gob-encoded models.ConfigEntry pattern as the S3 and browser-push
// configuration, so adding these settings required no schema migration.
//
// Missing or undecodable values fall back to the model defaults, which keep
// the new services disabled and therefore preserve v0.3.1 behavior. A decoded
// value is passed through the normalizers in config/settings so a partially
// populated or stale stored configuration cannot produce an invalid runtime
// configuration (for example a zero listener port).
//
// Secrets: models.SRTConfig.Passphrase and TURN credentials are stored here
// like any other value, but no function in this file logs a configuration
// value; the warn statements name only the setting key.

import (
	"errors"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config/settings"
	"github.com/owncast/owncast/models"
)

// errInvalidOutputProcessModel is returned for a process model value the
// runtime does not implement. Kept local to the repository so the model
// package is not extended for a single error string.
var errInvalidOutputProcessModel = errors.New("invalid output process model: must be shared or isolated")

// GetSRTConfig returns the persisted SRT ingest configuration, falling back to
// models.DefaultSRTConfig() when the key is missing or cannot be decoded.
func (r *SqlConfigRepository) GetSRTConfig() models.SRTConfig {
	configEntry, err := r.datastore.Get(srtConfigKey)
	if err != nil {
		return models.DefaultSRTConfig()
	}

	var srtConfig models.SRTConfig
	if err := configEntry.GetObject(&srtConfig); err != nil {
		// Never include the encoded value: it can contain the passphrase.
		log.Warnln("unable to decode the stored SRT configuration; using defaults:", err)
		return models.DefaultSRTConfig()
	}

	return settings.WithSRTDefaults(srtConfig)
}

// SetSRTConfig stores the SRT ingest configuration.
func (r *SqlConfigRepository) SetSRTConfig(config models.SRTConfig) error {
	return r.datastore.Save(models.ConfigEntry{Key: srtConfigKey, Value: config})
}

// GetWebRTCConfig returns the persisted WebRTC configuration, falling back to
// models.DefaultWebRTCConfig() when the key is missing or cannot be decoded.
func (r *SqlConfigRepository) GetWebRTCConfig() models.WebRTCConfig {
	configEntry, err := r.datastore.Get(webrtcConfigKey)
	if err != nil {
		return models.DefaultWebRTCConfig()
	}

	var webrtcConfig models.WebRTCConfig
	if err := configEntry.GetObject(&webrtcConfig); err != nil {
		// Never include the encoded value: it can contain TURN secrets.
		log.Warnln("unable to decode the stored WebRTC configuration; using defaults:", err)
		return models.DefaultWebRTCConfig()
	}

	return settings.WithWebRTCDefaults(webrtcConfig)
}

// SetWebRTCConfig stores the WebRTC configuration.
func (r *SqlConfigRepository) SetWebRTCConfig(config models.WebRTCConfig) error {
	return r.datastore.Save(models.ConfigEntry{Key: webrtcConfigKey, Value: config})
}

// GetCustomPlayerPath returns the configured custom player module path ("" for
// the built-in player).
func (r *SqlConfigRepository) GetCustomPlayerPath() string {
	path, err := r.datastore.GetString(customPlayerPathKey)
	if err != nil {
		return ""
	}
	return path
}

// SetCustomPlayerPath stores the custom player module path.
func (r *SqlConfigRepository) SetCustomPlayerPath(path string) error {
	return r.datastore.SetString(customPlayerPathKey, path)
}

// GetCustomFrontendDir returns the configured custom viewer frontend directory
// ("" for the built-in frontend).
func (r *SqlConfigRepository) GetCustomFrontendDir() string {
	directory, err := r.datastore.GetString(customFrontendDirKey)
	if err != nil {
		return ""
	}
	return directory
}

// SetCustomFrontendDir stores the custom viewer frontend directory.
func (r *SqlConfigRepository) SetCustomFrontendDir(directory string) error {
	return r.datastore.SetString(customFrontendDirKey, directory)
}

// GetOutputProcessModel returns the configured FFmpeg process model, defaulting
// to shared when unset or when the stored value is no longer recognized.
func (r *SqlConfigRepository) GetOutputProcessModel() string {
	value, err := r.datastore.GetString(outputProcessModelKey)
	if err != nil {
		return models.OutputProcessModelShared
	}
	if !settings.IsValidOutputProcessModel(value) {
		log.Warnln("unrecognized stored output process model; using shared")
		return models.OutputProcessModelShared
	}
	return value
}

// SetOutputProcessModel stores the FFmpeg process model, rejecting values the
// runtime does not implement.
func (r *SqlConfigRepository) SetOutputProcessModel(model string) error {
	if !settings.IsValidOutputProcessModel(model) {
		return errInvalidOutputProcessModel
	}
	return r.datastore.SetString(outputProcessModelKey, model)
}
