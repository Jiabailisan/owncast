package settings

import "github.com/owncast/owncast/models"

// AppearanceMetadata returns the metadata for the viewer-appearance path
// settings added in Phase 1 (spec section 4.6). These are KV settings, not
// fields of the SRT/WebRTC models.
func AppearanceMetadata() []Field {
	return []Field{
		stringField("customPlayerPath", groupAppearance, "", "path",
			"Path to an ES module that replaces the default video player. Requires the default viewer frontend.", ApplyLive),
		stringField("customFrontendDir", groupAppearance, "", "path",
			"Directory containing a custom viewer frontend (must contain index.html). When set, the custom player path is ignored and Custom CSS/JavaScript are not served. /admin is always the built-in bundle.", ApplyLive),
	}
}

// OutputMetadata returns the metadata for the output process model setting
// added in Phase 1 (spec section 3.4).
func OutputMetadata() []Field {
	return []Field{
		enumField("processModel", groupOutput, models.OutputProcessModelShared,
			OutputProcessModelValues(),
			"FFmpeg process model: shared uses one process for all HLS and WebRTC legs; isolated uses one HLS process and one WebRTC process for independent failure domains and input flags.", ApplyRestartStream),
	}
}

// OutputProcessModelValues returns the accepted output process model values.
func OutputProcessModelValues() []string {
	return []string{models.OutputProcessModelShared, models.OutputProcessModelIsolated}
}

// IsValidOutputProcessModel reports whether value is a known process model.
func IsValidOutputProcessModel(value string) bool {
	for _, candidate := range OutputProcessModelValues() {
		if candidate == value {
			return true
		}
	}
	return false
}
