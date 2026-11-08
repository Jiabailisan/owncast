package webrtc

import (
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeConfigFillsDefaults(t *testing.T) {
	// A partially populated configuration gets usable network/media defaults.
	cfg := NormalizeConfig(models.WebRTCConfig{})
	assert.Equal(t, defaultBindAddress, cfg.BindAddress)
	assert.Equal(t, "mux", cfg.UDPMode)
	assert.Equal(t, 8189, cfg.UDPPort)
	assert.Equal(t, []string{"udp4", "udp6", "tcp4", "tcp6"}, cfg.NetworkTypes)
	assert.Equal(t, 30, cfg.SignalingRateLimit)
	assert.Equal(t, "constrained-baseline", cfg.H264Profile)
	assert.Equal(t, 1.0, cfg.KeyframeIntervalSeconds)
	// Zero means unlimited for maxViewers and off for maxViewersPerIP; the
	// persisted default (100) comes from models.DefaultWebRTCConfig().
	assert.Zero(t, cfg.MaxViewers)
	assert.Zero(t, cfg.MaxViewersPerIP)

	persisted := NormalizeConfig(models.DefaultWebRTCConfig())
	assert.Equal(t, 100, persisted.MaxViewers)
	assert.True(t, persisted.FallbackToHLS)
	assert.True(t, persisted.NACK)
	assert.True(t, persisted.TWCC)
	assert.True(t, persisted.TCPEnabled)
}

func TestValidateConfig(t *testing.T) {
	valid := NormalizeConfig(models.DefaultWebRTCConfig())

	tests := []struct {
		name   string
		mutate func(*models.WebRTCConfig)
	}{
		{"bad udp mode", func(c *models.WebRTCConfig) { c.UDPMode = "bogus" }},
		{"bad mux port", func(c *models.WebRTCConfig) { c.UDPPort = 80 }},
		{"range inverted", func(c *models.WebRTCConfig) { c.UDPMode = "range"; c.UDPPortMin = 60000; c.UDPPortMax = 50000 }},
		{"bad bind address", func(c *models.WebRTCConfig) { c.BindAddress = "not-an-ip" }},
		{"timeout ordering", func(c *models.WebRTCConfig) { c.ICEFailedTimeoutMs = c.ICEDisconnectedTimeoutMs }},
		{"keepalive too large", func(c *models.WebRTCConfig) { c.ICEKeepaliveIntervalMs = c.ICEDisconnectedTimeoutMs }},
		{"unknown network type", func(c *models.WebRTCConfig) { c.NetworkTypes = []string{"carrier-pigeon"} }},
		{"bad public ip", func(c *models.WebRTCConfig) { c.PublicIPs = []string{"300.1.2.3"} }},
		{"bad stun url", func(c *models.WebRTCConfig) { c.STUNServers = []string{"https://example.com"} }},
		{"h264 profile", func(c *models.WebRTCConfig) { c.H264Profile = "ultra" }},
		{"bwe without twcc", func(c *models.WebRTCConfig) { c.TWCC = false; c.BandwidthEstimation = true }},
		{"embedded turn", func(c *models.WebRTCConfig) { c.EmbeddedTurn = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			cfg.PublicIPs = append([]string{}, valid.PublicIPs...)
			tc.mutate(&cfg)
			require.Error(t, ValidateConfig(cfg))
		})
	}

	require.NoError(t, ValidateConfig(valid))
}

func TestRestartRequiredFields(t *testing.T) {
	base := NormalizeConfig(models.DefaultWebRTCConfig())

	// Live settings do not require a restart.
	live := base
	live.MaxViewers = 12
	live.StrictDomain = true
	live.SessionIdleTimeoutSeconds = 45
	assert.Empty(t, RestartRequiredFields(base, live))

	// Restart-service settings are reported by key.
	restart := base
	restart.UDPPort = 9000
	restart.NetworkTypes = []string{"udp4"}
	restart.NACK = false
	fields := RestartRequiredFields(base, restart)
	assert.Equal(t, []string{"nack", "networkTypes", "udpPort"}, fields)
}

func TestIceNetworkTypes(t *testing.T) {
	cfg := NormalizeConfig(models.WebRTCConfig{TCPEnabled: false, NetworkTypes: []string{"udp4"}})
	types, err := iceNetworkTypes(cfg)
	require.NoError(t, err)
	assert.Equal(t, []int{int(webrtc.NetworkTypeUDP4)}, []int{int(types[0])})

	cfg.TCPEnabled = true
	cfg.NetworkTypes = []string{"udp4"}
	types, err = iceNetworkTypes(cfg)
	require.NoError(t, err)
	require.Len(t, types, 3)
	assert.Equal(t, []int{int(webrtc.NetworkTypeUDP4), int(webrtc.NetworkTypeTCP4), int(webrtc.NetworkTypeTCP6)},
		[]int{int(types[0]), int(types[1]), int(types[2])})

	cfg.NetworkTypes = []string{"nope"}
	_, err = iceNetworkTypes(cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestCanonicalH264Fmtp(t *testing.T) {
	for _, profile := range []string{"constrained-baseline", "main", "high"} {
		fmtp, err := canonicalH264Fmtp(profile)
		require.NoError(t, err)
		assert.Contains(t, fmtp, "packetization-mode=1")
	}
	_, err := canonicalH264Fmtp("ultra")
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestAddressRewriteRules(t *testing.T) {
	assert.Empty(t, addressRewriteRules(nil))

	ips := []netip.Addr{
		netip.MustParseAddr("203.0.113.5"),
		netip.MustParseAddr("2001:db8::5"),
	}
	rules := addressRewriteRules(ips)
	require.Len(t, rules, 2)
	assert.Equal(t, []string{"203.0.113.5"}, rules[0].External)
	assert.Equal(t, []string{"2001:db8::5"}, rules[1].External)
	assert.Equal(t, webrtc.ICECandidateTypeHost, rules[0].AsCandidateType)
}

func TestPublicIPsOverrideDomain(t *testing.T) {
	cfg := NormalizeConfig(models.WebRTCConfig{Domain: "unresolvable.invalid", PublicIPs: []string{"203.0.113.5"}})
	ips, err := advertisedIPs(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, ips, 1)
	assert.Equal(t, "203.0.113.5", ips[0].String())
}

func TestHostAndOriginChecks(t *testing.T) {
	cfg := NormalizeConfig(models.WebRTCConfig{
		Domain:         "live.example.com",
		StrictDomain:   true,
		AllowedOrigins: []string{"https://embed.example.com"},
	})

	req := httptest.NewRequest("POST", "https://live.example.com/webrtc/whep", nil)
	req.Host = "live.example.com:443"
	assert.True(t, hostMatchesDomain(req, cfg))

	req.Host = "evil.example.com"
	assert.False(t, hostMatchesDomain(req, cfg))

	originReq := httptest.NewRequest("POST", "https://live.example.com/webrtc/whep", nil)
	originReq.Header.Set("Origin", "https://embed.example.com")
	assert.True(t, originAllowed(originReq, cfg))
	assert.Equal(t, "https://embed.example.com", allowedOriginHeader(originReq, cfg))

	originReq.Header.Set("Origin", "https://evil.example.com")
	assert.False(t, originAllowed(originReq, cfg))
	assert.Empty(t, allowedOriginHeader(originReq, cfg))

	// Missing Origin (native clients) is allowed.
	noOrigin := httptest.NewRequest("POST", "https://live.example.com/webrtc/whep", nil)
	assert.True(t, originAllowed(noOrigin, cfg))
}

func TestSameOriginAcceptedWithEmptyConfig(t *testing.T) {
	// This is the production viewer-page case: no domain configured, no
	// allowlist, strictDomain off. A same-origin Origin must be accepted, and
	// no CORS header is needed.
	cfg := NormalizeConfig(models.WebRTCConfig{})

	sameOrigin := httptest.NewRequest("POST", "http://127.0.0.1:18080/webrtc/whep", nil)
	sameOrigin.Host = "127.0.0.1:18080"
	sameOrigin.Header.Set("Origin", "http://127.0.0.1:18080")
	assert.True(t, originAllowed(sameOrigin, cfg))
	assert.True(t, requestIsSameOrigin(sameOrigin))
	assert.Empty(t, allowedOriginHeader(sameOrigin, cfg))

	crossOrigin := httptest.NewRequest("POST", "http://127.0.0.1:18080/webrtc/whep", nil)
	crossOrigin.Host = "127.0.0.1:18080"
	crossOrigin.Header.Set("Origin", "http://evil.example.com")
	assert.False(t, originAllowed(crossOrigin, cfg), "cross-origin must require an allowlist entry")
	assert.Empty(t, allowedOriginHeader(crossOrigin, cfg))

	allowCfg := NormalizeConfig(models.WebRTCConfig{AllowedOrigins: []string{"http://embed.example.com"}})
	allowedCrossOrigin := httptest.NewRequest("POST", "http://127.0.0.1:18080/webrtc/whep", nil)
	allowedCrossOrigin.Host = "127.0.0.1:18080"
	allowedCrossOrigin.Header.Set("Origin", "http://embed.example.com")
	assert.True(t, originAllowed(allowedCrossOrigin, allowCfg))
	assert.Equal(t, "http://embed.example.com", allowedOriginHeader(allowedCrossOrigin, allowCfg))
}

func TestRequestIsSameOriginHostNormalization(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"ipv4 with port", "127.0.0.1:18080", "http://127.0.0.1:18080", true},
		{"hostname with port", "live.example.com:8443", "https://live.example.com:8443", true},
		{"portless host", "live.example.com", "https://live.example.com", true},
		{"different host", "127.0.0.1:18080", "http://evil.example.com:18080", false},
		{"different port still same host", "127.0.0.1:18080", "http://127.0.0.1:9999", true},
		{"ipv6", "[::1]:18080", "http://[::1]:18080", true},
		{"trailing dot", "live.example.com.", "https://live.example.com", true},
		{"missing origin", "127.0.0.1:18080", "", false},
		{"null origin", "127.0.0.1:18080", "null", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+test.host+"/webrtc/whep", nil)
			req.Host = test.host
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			assert.Equal(t, test.want, requestIsSameOrigin(req))
		})
	}
}

func TestParsePublicIPs(t *testing.T) {
	cfg := models.WebRTCConfig{PublicIPs: []string{"203.0.113.5", " 2001:db8::1 ", "bogus"}}
	addrs := ParsePublicIPs(cfg)
	require.Len(t, addrs, 2)
	assert.Equal(t, netip.MustParseAddr("203.0.113.5"), addrs[0])
}

func TestWarnings(t *testing.T) {
	warnings := Warnings(NormalizeConfig(models.WebRTCConfig{BandwidthEstimation: true}))
	assert.NotEmpty(t, warnings)
}

func TestReservationTTLConstant(t *testing.T) {
	assert.Greater(t, reservationTTL, time.Duration(0))
}
