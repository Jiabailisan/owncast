package webrtc

import (
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPruneOfferKeepsOneH264AndOpus(t *testing.T) {
	_, offer := testBrowserOffer(t)
	cfg := models.DefaultWebRTCConfig()

	pruned, err := pruneOffer(offer, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, pruned.videoPayloadType)
	require.NotEmpty(t, pruned.audioPayloadType)

	assert.Equal(t, 1, countOccurrences(pruned.sdp, "H264/90000"))
	assert.Equal(t, 1, countOccurrences(pruned.sdp, "opus/48000/2"))
	for _, removed := range []string{"VP8/90000", "VP9/90000", "AV1/90000", "H265/90000", "/rtx", "G722/8000", "PCMU/8000", "PCMA/8000"} {
		assert.NotContains(t, pruned.sdp, removed)
	}
	// The offer's recvonly direction is preserved for Pion's answerer.
	assert.Contains(t, pruned.sdp, "a=recvonly")
}

func TestPruneOfferPrefersConfiguredProfile(t *testing.T) {
	_, offer := testBrowserOffer(t)

	pruned, err := pruneOffer(offer, mustWebRTCConfig(t, func(c *models.WebRTCConfig) {
		c.H264Profile = "main"
	}))
	require.NoError(t, err)
	assert.Equal(t, "127", pruned.videoPayloadType, "main profile should select the 4d001f payload type")
	assert.Contains(t, pruned.sdp, "a=rtpmap:127 H264/90000")
}

func TestPruneOfferRejectsUnsupportedMedia(t *testing.T) {
	videoOnlyVP8 := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=rtpmap:96 VP8/90000\r\n"
	_, err := pruneOffer(videoOnlyVP8, models.DefaultWebRTCConfig())
	require.ErrorIs(t, err, ErrUnsupportedMedia)

	noAudio := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0 1\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=rtpmap:96 H264/90000\r\na=fmtp:96 packetization-mode=1;profile-level-id=42e01f\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\nc=IN IP4 0.0.0.0\r\na=mid:1\r\na=rtpmap:111 G722/8000\r\n"
	_, err = pruneOffer(noAudio, models.DefaultWebRTCConfig())
	require.ErrorIs(t, err, ErrUnsupportedMedia)

	_, err = pruneOffer("not an sdp", models.DefaultWebRTCConfig())
	require.ErrorIs(t, err, ErrBadOffer)
}

func TestParseTrickleFragment(t *testing.T) {
	fragment := "a=ice-ufrag:abcd\r\na=ice-pwd:secret\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:0\r\na=candidate:1 1 udp 2122260223 203.0.113.7 51000 typ host generation 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:1\r\na=candidate:2 1 udp 2122260223 203.0.113.7 51001 typ host generation 0\r\na=end-of-candidates\r\n"

	candidates, err := parseTrickleFragment(fragment)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.NotNil(t, candidates[0].SDPMid)
	assert.Equal(t, "0", *candidates[0].SDPMid)
	require.NotNil(t, candidates[0].SDPMLineIndex)
	assert.EqualValues(t, 0, *candidates[0].SDPMLineIndex)
	require.NotNil(t, candidates[1].SDPMid)
	assert.Equal(t, "1", *candidates[1].SDPMid)
	assert.Contains(t, candidates[0].Candidate, "candidate:1 1 udp")

	_, err = parseTrickleFragment("a=ice-ufrag:abcd\r\n")
	require.ErrorIs(t, err, ErrBadOffer)
}

func TestBuildLinkHeaders(t *testing.T) {
	cfg := models.DefaultWebRTCConfig()
	cfg.STUNServers = []string{"stun:stun.example.org:3478"}
	cfg.TURNServers = []models.TURNServer{
		{URL: "turn:turn.example.org:3478?transport=udp", Username: "user", Credential: "pass"},
		{URL: "turn:turn2.example.org:3478?transport=udp", StaticSecret: "shared-secret"},
	}

	headers := buildLinkHeaders(cfg)
	require.Len(t, headers, 3)
	assert.Contains(t, headers[0], "stun:stun.example.org:3478")
	assert.Contains(t, headers[0], "rel=\"ice-server\"")
	assert.Contains(t, headers[1], "username=\"user\"")
	assert.Contains(t, headers[1], "credential=\"pass\"")
	assert.Contains(t, headers[2], "credential-type=\"password\"")

	servers := (&Service{cfg: cfg}).PublicICEServers()
	require.Len(t, servers, 3)
	assert.Equal(t, []string{"stun:stun.example.org:3478"}, servers[0].URLs)
	assert.NotEmpty(t, servers[2].Credential)
}

func TestProfileIDMatches(t *testing.T) {
	assert.True(t, profileIDMatches("42e01f", 0x42))
	assert.True(t, profileIDMatches("42001f", 0x42))
	assert.False(t, profileIDMatches("4d001f", 0x42))
	assert.True(t, profileIDMatches("4d001f", 0x4d))
	assert.False(t, profileIDMatches("zz", 0x42))
}

func mustWebRTCConfig(t *testing.T, mutate func(*models.WebRTCConfig)) models.WebRTCConfig {
	t.Helper()
	cfg := models.DefaultWebRTCConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	require.NoError(t, ValidateConfig(NormalizeConfig(cfg)))

	return cfg
}
