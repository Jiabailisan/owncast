package webrtc

import (
	"context"
	"testing"

	"github.com/owncast/owncast/models"
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
