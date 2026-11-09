package webrtc

import (
	"math"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func floatPtr(value float64) *float64 { return &value }

func TestSanitizedClientReportRejectsAbsurdValues(t *testing.T) {
	report := sanitizedClientReport(ClientReport{
		DecodedWidth:     8,
		DecodedHeight:    8,
		FPS:              571,
		PacketsLost:      -3,
		PacketsReceived:  -1,
		PacketLossPct:    floatPtr(140),
		JitterMs:         -1,
		RoundTripTimeMs:  99_999,
		Transport:        "SCTP ",
		VideoBitrateKbps: floatPtr(-5),
	})

	assert.Zero(t, report.DecodedWidth)
	assert.Zero(t, report.DecodedHeight)
	assert.Zero(t, report.FPS)
	assert.Zero(t, report.PacketsLost)
	assert.Zero(t, report.PacketsReceived)
	assert.Nil(t, report.PacketLossPct)
	assert.Zero(t, report.JitterMs)
	assert.Zero(t, report.RoundTripTimeMs)
	assert.Empty(t, report.Transport)
	assert.Nil(t, report.VideoBitrateKbps)
}

func TestSanitizedClientReportAcceptsValidMeasurements(t *testing.T) {
	report := sanitizedClientReport(ClientReport{
		DecodedWidth:     2560,
		DecodedHeight:    1440,
		FPS:              59.9,
		PacketsLost:      126,
		PacketsReceived:  8763,
		PacketLossPct:    floatPtr(1.42),
		JitterMs:         0.8,
		RoundTripTimeMs:  3,
		Transport:        " TCP ",
		VideoBitrateKbps: floatPtr(5980),
	})

	assert.Equal(t, 2560, report.DecodedWidth)
	assert.Equal(t, 1440, report.DecodedHeight)
	assert.InDelta(t, 59.9, report.FPS, 0.001)
	assert.Equal(t, int64(126), report.PacketsLost)
	assert.Equal(t, int64(8763), report.PacketsReceived)
	require.NotNil(t, report.PacketLossPct)
	assert.InDelta(t, 1.42, *report.PacketLossPct, 0.001)
	assert.Equal(t, "tcp", report.Transport)
	require.NotNil(t, report.VideoBitrateKbps)
	assert.InDelta(t, 5980, *report.VideoBitrateKbps, 0.001)
}

func TestSanitizedClientReportDerivesPacketLossFromCounters(t *testing.T) {
	report := sanitizedClientReport(ClientReport{
		PacketsLost:     126,
		PacketsReceived: 8763,
	})

	require.NotNil(t, report.PacketLossPct)
	assert.InDelta(t, 126.0/8889.0*100, *report.PacketLossPct, 0.001)
}

func TestSanitizedClientReportBoundaryValues(t *testing.T) {
	atLimit := sanitizedClientReport(ClientReport{FPS: 240, PacketLossPct: floatPtr(100)})
	assert.InDelta(t, 240, atLimit.FPS, 0.001)

	overLimit := sanitizedClientReport(ClientReport{FPS: 240.1, PacketLossPct: floatPtr(100.1)})
	assert.Zero(t, overLimit.FPS)
	assert.Nil(t, overLimit.PacketLossPct)

	nan := sanitizedClientReport(ClientReport{FPS: math.NaN()})
	assert.Zero(t, nan.FPS)
}

func TestLiveViewerRowPrefersValidatedClientMeasurements(t *testing.T) {
	snapshot := ViewerSnapshot{
		SessionID:     "session",
		ClientID:      "session",
		Variant:       1,
		VariantName:   "source copy",
		Protocol:      "webrtc",
		BitrateKbps:   floatPtr(1000),
		Resolution:    "",
		PacketLossPct: floatPtr(0),
		JitterMs:      floatPtr(2),
		ICETransport:  "",
		Source:        "client",
		Client: &ClientReport{
			DecodedWidth:     2560,
			DecodedHeight:    1440,
			FPS:              52.4,
			PacketsLost:      126,
			PacketsReceived:  8763,
			PacketLossPct:    floatPtr(1.42),
			Transport:        "udp",
			VideoBitrateKbps: floatPtr(5980),
		},
	}

	serverFPS := 30.0
	row := liveViewerRow(snapshot, &serverFPS)

	assert.Equal(t, "2560x1440", row.Resolution)
	require.NotNil(t, row.FPS)
	assert.InDelta(t, 52.4, *row.FPS, 0.001)
	require.NotNil(t, row.BitrateKbps)
	assert.InDelta(t, 5980, *row.BitrateKbps, 0.001)
	require.NotNil(t, row.PacketLossPct)
	assert.InDelta(t, 1.42, *row.PacketLossPct, 0.001)
	assert.Equal(t, "udp", row.ICETransport)
	assert.Equal(t, string(models.PlaybackSourceClient), row.Source)
}

func TestLiveViewerRowServerTransportWinsOverClient(t *testing.T) {
	snapshot := ViewerSnapshot{
		ICETransport: "tcp",
		Client:       &ClientReport{FPS: 30, Transport: "udp"},
	}

	row := liveViewerRow(snapshot, nil)

	assert.Equal(t, "tcp", row.ICETransport)
	assert.Equal(t, string(models.PlaybackSourceClient), row.Source)
}

func TestLiveViewerRowFallsBackToServerValues(t *testing.T) {
	serverFPS := 24.0
	snapshot := ViewerSnapshot{
		BitrateKbps:   floatPtr(1200),
		Resolution:    "1280x720",
		PacketLossPct: floatPtr(0.5),
		JitterMs:      floatPtr(3),
		ICETransport:  "udp",
		Client: &ClientReport{
			DecodedWidth:  8,
			DecodedHeight: 8,
			FPS:           571,
			PacketLossPct: floatPtr(150),
			Transport:     "sctp",
		},
	}

	row := liveViewerRow(snapshot, &serverFPS)

	assert.Equal(t, "1280x720", row.Resolution)
	require.NotNil(t, row.FPS)
	assert.InDelta(t, 24, *row.FPS, 0.001, "invalid client fps must fall back to the media-leg measurement")
	require.NotNil(t, row.BitrateKbps)
	assert.InDelta(t, 1200, *row.BitrateKbps, 0.001)
	require.NotNil(t, row.PacketLossPct)
	assert.InDelta(t, 0.5, *row.PacketLossPct, 0.001)
	assert.Equal(t, "udp", row.ICETransport)
	assert.Equal(t, string(models.PlaybackSourceServer), row.Source,
		"a report with no usable client measurement must not be labelled client")
}

func TestLiveViewerRowWithoutClientReport(t *testing.T) {
	serverFPS := 30.0
	row := liveViewerRow(ViewerSnapshot{
		Resolution:   "1280x720",
		ICETransport: "udp",
	}, &serverFPS)

	require.NotNil(t, row.FPS)
	assert.InDelta(t, 30, *row.FPS, 0.001)
	assert.Equal(t, "udp", row.ICETransport)
	assert.Equal(t, string(models.PlaybackSourceServer), row.Source)
}
