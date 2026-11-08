package webrtc

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEngineConfig builds a configuration that does not bind any port: range
// mode only applies the ephemeral port range to the ICE agent, and TCP is off.
func testEngineConfig() models.WebRTCConfig {
	cfg := NormalizeConfig(models.DefaultWebRTCConfig())
	cfg.Enabled = true
	cfg.BindAddress = "127.0.0.1"
	cfg.UDPMode = "range"
	cfg.UDPPortMin = 50000
	cfg.UDPPortMax = 50100
	cfg.TCPEnabled = false
	cfg.NetworkTypes = []string{"udp4"}
	cfg.STUNServers = []string{}

	return cfg
}

func TestEngineBuildsWithoutBindingSockets(t *testing.T) {
	cfg := testEngineConfig()
	eng, err := newEngine(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(eng.close)

	require.NotNil(t, eng.api)

	pc, getter, estimator, err := eng.newPeerConnection()
	require.NoError(t, err)
	require.NotNil(t, pc)
	require.NotNil(t, getter, "the stats interceptor getter must be captured")
	assert.Nil(t, estimator, "no bandwidth estimator unless bandwidthEstimation is enabled")
	require.NoError(t, pc.Close())
}

func TestEngineRejectsInvalidConfig(t *testing.T) {
	cfg := testEngineConfig()
	cfg.UDPMode = "bogus"
	_, err := newEngine(context.Background(), cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

// TestLoopbackBindAdvertisesICECandidates is the regression test for the WHEP
// answer-SDP bug: Pion filters loopback ICE candidates by default, so a server
// bound to 127.0.0.1 answered WHEP offers without any a=candidate lines and
// browsers could not connect. The engine must call SetIncludeLoopbackCandidate
// for loopback bind addresses.
func TestLoopbackBindAdvertisesICECandidates(t *testing.T) {
	cfg := loopbackEngineConfig(t, "127.0.0.1")

	eng, err := newEngine(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(eng.close)

	candidates := gatherAnswerCandidates(t, eng)
	require.NotEmpty(t, candidates,
		"loopback bind must advertise at least one ICE candidate in the answer SDP")
	assert.Contains(t, candidates, "127.0.0.1")
}

// TestNonLoopbackBindKeepsDefaultLoopbackFiltering pins the other half of the
// behavior: a wildcard/non-loopback bind must not force loopback candidates on.
func TestNonLoopbackBindKeepsDefaultLoopbackFiltering(t *testing.T) {
	if !hostHasNonLoopbackIPv4() {
		t.Skip("host has no non-loopback IPv4 interface; cannot assert Pion default loopback filtering")
	}
	cfg := loopbackEngineConfig(t, "0.0.0.0")

	eng, err := newEngine(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(eng.close)

	candidates := gatherAnswerCandidates(t, eng)
	require.NotEmpty(t, candidates, "wildcard bind must advertise the host's interface candidates")
	assert.NotContains(t, candidates, "127.0.0.1",
		"non-loopback bind must keep Pion's default filtering of loopback candidates")
}

// loopbackEngineConfig builds a mux-mode engine config bound to bindAddress.
// Mux mode is the production path that reproduced the bug: the single UDP
// socket's listen address is the only candidate source.
func loopbackEngineConfig(t *testing.T, bindAddress string) models.WebRTCConfig {
	t.Helper()

	cfg := NormalizeConfig(models.DefaultWebRTCConfig())
	cfg.Enabled = true
	cfg.BindAddress = bindAddress
	cfg.UDPMode = udpModeMux
	cfg.UDPPort = freeUDPPortOn(t, bindAddress)
	cfg.TCPEnabled = false
	cfg.NetworkTypes = []string{networkTypeUDP4}
	cfg.STUNServers = []string{}

	return cfg
}

// gatherAnswerCandidates performs a minimal answerer exchange and returns the
// IPs of the ICE candidates in the local description.
func gatherAnswerCandidates(t *testing.T, eng *engine) []string {
	t.Helper()

	_, offer := testBrowserOffer(t)
	pc, _, _, err := eng.newPeerConnection()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })

	require.NoError(t, pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	}))
	answer, err := pc.CreateAnswer(nil)
	require.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	require.NoError(t, pc.SetLocalDescription(answer))
	select {
	case <-gatherComplete:
	case <-time.After(5 * time.Second):
		t.Fatal("ICE gathering did not complete within 5s")
	}
	local := pc.LocalDescription()
	require.NotNil(t, local)

	return candidateIPs(local.SDP)
}

func candidateIPs(sdp string) []string {
	var ips []string
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			ips = append(ips, fields[4])
		}
	}

	return ips
}

func freeUDPPortOn(t *testing.T, ip string) int {
	t.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip)})
	require.NoError(t, err)
	port := conn.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, conn.Close())

	return port
}

func hostHasNonLoopbackIPv4() bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip != nil && !ip.IsLoopback() {
			return true
		}
	}

	return false
}
