package webrtc

import (
	"errors"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(value bool) *bool { return &value }

func eligibleSource() SourceInfo {
	return SourceInfo{
		Protocol:     "srt",
		VideoCodec:   "h264",
		VideoProfile: "constrained-baseline",
		VideoLevel:   "3.1",
		HasBFrames:   boolPtr(false),
		Width:        1280,
		Height:       720,
		FPS:          30,
	}
}

func webRTCVariant(mode string, mutate func(*models.StreamOutputVariant)) models.StreamOutputVariant {
	output := &models.ProtocolOutputSettings{
		Enabled: models.BoolPtr(true),
		Mode:    mode,
	}
	variant := models.StreamOutputVariant{
		Name:         "variant",
		Outputs:      &models.VariantOutputs{HLS: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)}, WebRTC: output},
		VideoBitrate: 2500,
		Framerate:    30,
	}
	if mutate != nil {
		mutate(&variant)
	}

	return variant
}

func hasReason(reasons []string, code string) bool {
	for _, reason := range reasons {
		if strings.HasPrefix(reason, code+":") {
			return true
		}
	}

	return false
}

func hasWarning(warnings []string, code string) bool {
	return hasReason(warnings, code)
}

func TestEligibleForCopy(t *testing.T) {
	mutate := func(change func(*SourceInfo)) SourceInfo {
		source := eligibleSource()
		change(&source)

		return source
	}

	tests := []struct {
		name     string
		source   SourceInfo
		cfg      models.WebRTCConfig
		eligible bool
		reasons  []string
	}{
		{name: "baseline source", source: eligibleSource(), eligible: true},
		{name: "main profile", source: mutate(func(s *SourceInfo) { s.VideoProfile = "main" }), eligible: true},
		{name: "high profile", source: mutate(func(s *SourceInfo) { s.VideoProfile = "high" }), eligible: true},
		{
			name:     "constrained high profile",
			source:   mutate(func(s *SourceInfo) { s.VideoProfile = "constrained-high" }),
			eligible: true,
		},
		{
			name:    "hevc source",
			source:  mutate(func(s *SourceInfo) { s.VideoCodec = "hevc" }),
			reasons: []string{ReasonCodecUnsupported},
		},
		{
			name:    "av1 source",
			source:  mutate(func(s *SourceInfo) { s.VideoCodec = "av1" }),
			reasons: []string{ReasonCodecUnsupported},
		},
		{
			name:    "unknown codec",
			source:  mutate(func(s *SourceInfo) { s.VideoCodec = "" }),
			reasons: []string{ReasonCodecUnsupported},
		},
		{
			name:    "high10 profile",
			source:  mutate(func(s *SourceInfo) { s.VideoProfile = "high-10" }),
			reasons: []string{ReasonProfileUnsupported},
		},
		{
			name:    "high 4:2:2 profile",
			source:  mutate(func(s *SourceInfo) { s.VideoProfile = "high-4:2:2" }),
			reasons: []string{ReasonProfileUnsupported},
		},
		{
			name:    "extended profile",
			source:  mutate(func(s *SourceInfo) { s.VideoProfile = "extended" }),
			reasons: []string{ReasonProfileUnsupported},
		},
		{
			name:    "unknown profile",
			source:  mutate(func(s *SourceInfo) { s.VideoProfile = "" }),
			reasons: []string{ReasonProfileUnknown},
		},
		{
			name:    "b-frames present",
			source:  mutate(func(s *SourceInfo) { s.HasBFrames = boolPtr(true) }),
			reasons: []string{ReasonBFramesPresent},
		},
		{
			name:    "b-frames unknown",
			source:  mutate(func(s *SourceInfo) { s.HasBFrames = nil }),
			reasons: []string{ReasonBFramesUnknown},
		},
		{
			name:    "resolution unknown",
			source:  mutate(func(s *SourceInfo) { s.Width = 0; s.Height = 0 }),
			reasons: []string{ReasonResolutionUnknown},
		},
		{
			name:    "resolution too large",
			source:  mutate(func(s *SourceInfo) { s.Width = 5120; s.Height = 2880 }),
			reasons: []string{ReasonResolutionTooLarge},
		},
		{
			name:    "width too large",
			source:  mutate(func(s *SourceInfo) { s.Width = 5120; s.Height = 2160 }),
			reasons: []string{ReasonResolutionTooLarge},
		},
		{
			name:     "1440p source is copy eligible",
			source:   mutate(func(s *SourceInfo) { s.Width = 2560; s.Height = 1440; s.VideoProfile = "main"; s.VideoLevel = "5.1" }),
			eligible: true,
		},
		{
			name:     "4K source at exactly the cap is copy eligible",
			source:   mutate(func(s *SourceInfo) { s.Width = 4096; s.Height = 2160; s.VideoLevel = "5.1" }),
			eligible: true,
		},
		{
			name:    "odd resolution",
			source:  mutate(func(s *SourceInfo) { s.Width = 1281; s.Height = 721 }),
			reasons: []string{ReasonResolutionOdd},
		},
		{
			name:     "1080p source remains copy eligible",
			source:   mutate(func(s *SourceInfo) { s.Width = 1920; s.Height = 1080; s.VideoLevel = "5.1" }),
			eligible: true,
		},
		{
			name:    "level unknown",
			source:  mutate(func(s *SourceInfo) { s.VideoLevel = "" }),
			reasons: []string{ReasonLevelUnknown},
		},
		{
			name:    "level above browser safe cap",
			source:  mutate(func(s *SourceInfo) { s.VideoLevel = "5.2" }),
			reasons: []string{ReasonLevelUnsupported},
		},
		{
			name:    "invalid level",
			source:  mutate(func(s *SourceInfo) { s.VideoLevel = "not-a-level" }),
			reasons: []string{ReasonLevelUnsupported},
		},
		{
			name:    "configured level lowers the copy cap",
			source:  mutate(func(s *SourceInfo) { s.VideoLevel = "4.2" }),
			cfg:     models.WebRTCConfig{H264Level: "4.0"},
			reasons: []string{ReasonLevelUnsupported},
		},
		{
			name:    "empty source metadata",
			source:  SourceInfo{},
			reasons: []string{ReasonNoSourceInfo},
		},
		{
			name: "rtmp style metadata only knows the codec",
			source: SourceInfo{
				Protocol:   "rtmp",
				VideoCodec: "h264",
			},
			reasons: []string{
				ReasonProfileUnknown,
				ReasonBFramesUnknown,
				ReasonResolutionUnknown,
				ReasonLevelUnknown,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.cfg
			if cfg.H264Level == "" {
				cfg.H264Level = "auto"
			}
			eligible, reasons := EligibleForCopy(test.source, cfg)
			assert.Equal(t, test.eligible, eligible, "reasons: %v", reasons)
			for _, code := range test.reasons {
				assert.True(t, hasReason(reasons, code), "expected reason %s in %v", code, reasons)
			}
			if test.eligible {
				assert.Empty(t, reasons)
			}
		})
	}
}

func TestResolveWebRTCLegModes(t *testing.T) {
	cfg := models.DefaultWebRTCConfig()

	t.Run("not enabled for WebRTC", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeInherit, func(v *models.StreamOutputVariant) {
			v.Outputs.WebRTC = &models.ProtocolOutputSettings{Enabled: models.BoolPtr(false)}
		})
		decision, err := ResolveWebRTCLeg(eligibleSource(), variant, cfg)
		require.NoError(t, err)
		assert.False(t, decision.Enabled)
		assert.Empty(t, decision.Mode)
	})

	t.Run("inherit from HLS transcode", func(t *testing.T) {
		decision, err := ResolveWebRTCLeg(eligibleSource(), webRTCVariant(models.OutputModeInherit, nil), cfg)
		require.NoError(t, err)
		assert.True(t, decision.Enabled)
		assert.Equal(t, models.OutputModeTranscode, decision.Mode)
		assert.False(t, decision.Copy)
		assert.Equal(t, 2500, decision.VideoBitrateKbps)
		assert.Equal(t, 30, decision.GOPFrames)
		assert.Equal(t, "constrained-baseline", decision.H264Profile)
	})

	t.Run("inherit from HLS copy with eligible source", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeInherit, func(v *models.StreamOutputVariant) {
			v.IsVideoPassthrough = true
			v.VideoBitrate = 0
		})
		decision, err := ResolveWebRTCLeg(eligibleSource(), variant, cfg)
		require.NoError(t, err)
		assert.True(t, decision.Copy)
		assert.Equal(t, models.OutputModeCopy, decision.Mode)
		assert.Zero(t, decision.VideoBitrateKbps)
		assert.Zero(t, decision.GOPFrames)
		// Inherited copy is our automatic policy, not an operator choice;
		// informational warnings are reserved for explicit copy legs.
		assert.Empty(t, decision.Warnings)
	})

	t.Run("inherit from HLS copy but ineligible falls back to transcode", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeInherit, func(v *models.StreamOutputVariant) {
			v.IsVideoPassthrough = true
			v.VideoBitrate = 0
		})
		source := eligibleSource()
		source.HasBFrames = boolPtr(true)
		decision, err := ResolveWebRTCLeg(source, variant, cfg)
		require.NoError(t, err)
		assert.Equal(t, models.OutputModeTranscode, decision.Mode)
		assert.False(t, decision.Copy)
		// Inherited copy falling back to transcode is normal (the operator did
		// not ask for copy), so it must not surface warnings or reasons.
		assert.Empty(t, decision.IneligibilityReasons)
		assert.False(t, hasWarning(decision.Warnings, WarningCopyFallbackTranscode))
		assert.Equal(t, DefaultWebRTCVideoBitrateKbps, decision.VideoBitrateKbps)
	})

	t.Run("inherit with HLS disabled falls back to transcode", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeInherit, func(v *models.StreamOutputVariant) {
			v.Outputs.HLS = &models.ProtocolOutputSettings{Enabled: models.BoolPtr(false)}
		})
		decision, err := ResolveWebRTCLeg(eligibleSource(), variant, cfg)
		require.NoError(t, err)
		assert.Equal(t, models.OutputModeTranscode, decision.Mode)
		assert.True(t, hasWarning(decision.Warnings, WarningInheritNoHLS))
	})

	t.Run("explicit copy with eligible source", func(t *testing.T) {
		decision, err := ResolveWebRTCLeg(eligibleSource(), webRTCVariant(models.OutputModeCopy, nil), cfg)
		require.NoError(t, err)
		assert.True(t, decision.Copy)
		assert.Equal(t, models.OutputModeCopy, decision.Mode)
	})

	t.Run("explicit transcode ignores a copy-eligible source", func(t *testing.T) {
		decision, err := ResolveWebRTCLeg(eligibleSource(), webRTCVariant(models.OutputModeTranscode, nil), cfg)
		require.NoError(t, err)
		assert.False(t, decision.Copy)
		assert.Equal(t, models.OutputModeTranscode, decision.Mode)
	})

	t.Run("copy fallback disable disables the leg", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeCopy, func(v *models.StreamOutputVariant) {
			v.Outputs.WebRTC.CopyFallback = models.CopyFallbackDisable
		})
		source := eligibleSource()
		source.VideoCodec = "hevc"
		decision, err := ResolveWebRTCLeg(source, variant, cfg)
		require.NoError(t, err)
		assert.False(t, decision.Enabled)
		assert.Empty(t, decision.Mode)
		assert.True(t, hasReason(decision.IneligibilityReasons, ReasonCodecUnsupported))
		assert.True(t, hasWarning(decision.Warnings, WarningCopyDisabled))
	})

	t.Run("invalid copy fallback defaults to transcode with a warning", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeCopy, func(v *models.StreamOutputVariant) {
			v.Outputs.WebRTC.CopyFallback = "explode"
		})
		source := eligibleSource()
		source.HasBFrames = boolPtr(true)
		decision, err := ResolveWebRTCLeg(source, variant, cfg)
		require.NoError(t, err)
		assert.Equal(t, models.OutputModeTranscode, decision.Mode)
		assert.True(t, hasWarning(decision.Warnings, WarningCopyFallbackInvalid))
		assert.True(t, hasWarning(decision.Warnings, WarningCopyFallbackTranscode))
	})

	t.Run("invalid mode is a configuration error", func(t *testing.T) {
		variant := webRTCVariant("bogus", nil)
		_, err := ResolveWebRTCLeg(eligibleSource(), variant, cfg)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrInvalidConfig))
	})

	t.Run("copy leg warns when source and configured profiles differ", func(t *testing.T) {
		source := eligibleSource()
		source.VideoProfile = "high"
		cfgBaseline := cfg
		cfgBaseline.H264Profile = "constrained-baseline"
		decision, err := ResolveWebRTCLeg(source, webRTCVariant(models.OutputModeCopy, nil), cfgBaseline)
		require.NoError(t, err)
		assert.True(t, decision.Copy)
		assert.True(t, hasWarning(decision.Warnings, WarningCopyProfileMismatch))
	})

	t.Run("transcode settings honor variant and overrides", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeTranscode, func(v *models.StreamOutputVariant) {
			v.VideoBitrate = 1800
			v.Framerate = 24
			v.Outputs.WebRTC.Framerate = 25
		})
		cfgHalfSecond := cfg
		cfgHalfSecond.KeyframeIntervalSeconds = 0.5
		decision, err := ResolveWebRTCLeg(eligibleSource(), variant, cfgHalfSecond)
		require.NoError(t, err)
		assert.Equal(t, 1800, decision.VideoBitrateKbps)
		assert.Equal(t, 13, decision.GOPFrames) // round(0.5*25) = 13
	})

	t.Run("copy variant overridden to transcode uses the fallback defaults", func(t *testing.T) {
		variant := webRTCVariant(models.OutputModeCopy, func(v *models.StreamOutputVariant) {
			v.IsVideoPassthrough = true
			v.VideoBitrate = 0
			v.Outputs.WebRTC.CopyFallback = models.CopyFallbackTranscode
			v.Outputs.WebRTC.VideoBitrate = 1800
		})
		source := eligibleSource()
		source.Width = 5120
		source.Height = 2880
		decision, err := ResolveWebRTCLeg(source, variant, cfg)
		require.NoError(t, err)
		assert.Equal(t, models.OutputModeTranscode, decision.Mode)
		assert.Equal(t, 1800, decision.VideoBitrateKbps)
		assert.True(t, hasReason(decision.IneligibilityReasons, ReasonResolutionTooLarge))
	})
}

// TestResolveWebRTCLegT7Mapping mirrors spec T7: HLS Stream 1 (transcode) and
// HLS Stream 2 (copy) with WebRTC enabled and mode=inherit must produce
// WebRTC Stream 1 (transcode) and WebRTC Stream 2 (copy).
func TestResolveWebRTCLegT7Mapping(t *testing.T) {
	cfg := models.DefaultWebRTCConfig()
	source := eligibleSource()

	hlsTranscode := webRTCVariant(models.OutputModeInherit, func(v *models.StreamOutputVariant) {
		v.Name = "stream-1"
		v.VideoBitrate = 2500
	})
	hlsCopy := webRTCVariant(models.OutputModeInherit, func(v *models.StreamOutputVariant) {
		v.Name = "stream-2"
		v.IsVideoPassthrough = true
		v.VideoBitrate = 0
	})

	transcoded, err := ResolveWebRTCLeg(source, hlsTranscode, cfg)
	require.NoError(t, err)
	copied, err := ResolveWebRTCLeg(source, hlsCopy, cfg)
	require.NoError(t, err)

	assert.Equal(t, models.OutputModeTranscode, transcoded.Mode)
	assert.Equal(t, models.OutputModeCopy, copied.Mode)
	assert.False(t, transcoded.Copy)
	assert.True(t, copied.Copy)

	// With a non-H.264 source both legs transcode (C3).
	hevc := source
	hevc.VideoCodec = "hevc"
	transcodedCopyLeg, err := ResolveWebRTCLeg(hevc, hlsCopy, cfg)
	require.NoError(t, err)
	assert.Equal(t, models.OutputModeTranscode, transcodedCopyLeg.Mode)
	assert.False(t, transcodedCopyLeg.Enabled == false)
}

func TestSourceInfoFromDetails(t *testing.T) {
	hasBFrames := false
	info := SourceInfoFromDetails("SRT", models.InboundStreamDetails{
		VideoCodec:     "H.264",
		VideoProfile:   "High",
		VideoLevel:     " 4.1 ",
		HasBFrames:     &hasBFrames,
		Width:          1920,
		Height:         1080,
		VideoFramerate: 29.97,
	})

	assert.Equal(t, "srt", info.Protocol)
	assert.Equal(t, "h264", info.VideoCodec)
	assert.Equal(t, "high", info.VideoProfile)
	assert.Equal(t, "4.1", info.VideoLevel)
	assert.Equal(t, 1920, info.Width)
	assert.Equal(t, 1080, info.Height)
	assert.InDelta(t, 29.97, info.FPS, 0.001)
	require.NotNil(t, info.HasBFrames)
	assert.False(t, *info.HasBFrames)

	hevc := SourceInfoFromDetails("rtmp", models.InboundStreamDetails{VideoCodec: "H.265"})
	assert.Equal(t, "hevc", hevc.VideoCodec)

	unknown := SourceInfoFromDetails("rtmp", models.InboundStreamDetails{VideoCodec: "Unknown"})
	assert.Equal(t, "unknown", unknown.VideoCodec)
}

func TestSourceInfoFromBroadcaster(t *testing.T) {
	assert.Equal(t, SourceInfo{}, SourceInfoFromBroadcaster(nil))

	broadcaster := &models.Broadcaster{
		StreamDetails: models.InboundStreamDetails{
			VideoCodec:   "H.264",
			VideoProfile: "main",
			VideoLevel:   "3.1",
			Width:        1280,
			Height:       720,
		},
	}
	info := SourceInfoFromBroadcaster(broadcaster)
	assert.Equal(t, "h264", info.VideoCodec)
	assert.Equal(t, "main", info.VideoProfile)
	assert.Nil(t, info.HasBFrames)
}

func TestParseH264Level(t *testing.T) {
	tests := []struct {
		raw   string
		level int
		valid bool
	}{
		{"3.1", 31, true},
		{"4.0", 40, true},
		{"5.1", 51, true},
		{"51", 51, true},
		{"4", 40, true},
		{"", 0, false},
		{"auto", 0, false},
		{"4.x", 0, false},
		{"4.10", 0, false},
	}
	for _, test := range tests {
		level, valid := parseH264Level(test.raw)
		assert.Equal(t, test.valid, valid, "raw %q", test.raw)
		if test.valid {
			assert.Equal(t, test.level, level, "raw %q", test.raw)
		}
	}
}
