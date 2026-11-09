package webrtc

import (
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func candidate(id, protocol string) webrtc.ICECandidateStats {
	return webrtc.ICECandidateStats{ID: id, Protocol: protocol}
}

func pair(id, localID, remoteID string, nominated bool, state webrtc.StatsICECandidatePairState, packetsSent uint32) webrtc.ICECandidatePairStats {
	return webrtc.ICECandidatePairStats{
		ID:                id,
		LocalCandidateID:  localID,
		RemoteCandidateID: remoteID,
		Nominated:         nominated,
		State:             state,
		PacketsSent:       packetsSent,
	}
}

func statsReport(entries ...webrtc.Stats) webrtc.StatsReport {
	report := webrtc.StatsReport{}
	for index, entry := range entries {
		report[string(rune('a'+index))] = entry
	}

	return report
}

func TestICETransportFromStatsSelectedUDPServerPair(t *testing.T) {
	report := statsReport(
		candidate("local-udp", "udp"),
		candidate("remote-udp", "udp"),
		pair("pair-udp", "local-udp", "remote-udp", true, webrtc.StatsICECandidatePairStateSucceeded, 120),
	)

	assert.Equal(t, "udp", iceTransportFromStats(report))
}

func TestICETransportFromStatsSelectedTCPServerPair(t *testing.T) {
	report := statsReport(
		candidate("local-udp", "udp"),
		candidate("remote-udp", "udp"),
		candidate("local-tcp", "tcp"),
		candidate("remote-tcp", "tcp"),
		pair("pair-udp", "local-udp", "remote-udp", false, webrtc.StatsICECandidatePairStateSucceeded, 500),
		pair("pair-tcp", "local-tcp", "remote-tcp", true, webrtc.StatsICECandidatePairStateSucceeded, 2),
	)

	assert.Equal(t, "tcp", iceTransportFromStats(report))
}

func TestICETransportFromStatsNominatedPairBeforeSucceeded(t *testing.T) {
	report := statsReport(
		candidate("local", "udp"),
		candidate("remote", "udp"),
		pair("pair", "local", "remote", true, webrtc.StatsICECandidatePairStateInProgress, 0),
	)

	assert.Equal(t, "udp", iceTransportFromStats(report))
}

func TestICETransportFromStatsBusiestSucceededPairFallback(t *testing.T) {
	report := statsReport(
		candidate("local-a", "udp"),
		candidate("remote-a", "udp"),
		candidate("local-b", "udp"),
		candidate("remote-b", "udp"),
		pair("pair-a", "local-a", "remote-a", false, webrtc.StatsICECandidatePairStateSucceeded, 10),
		pair("pair-b", "local-b", "remote-b", false, webrtc.StatsICECandidatePairStateSucceeded, 900),
	)

	// Both are succeeded and none is nominated yet; the higher-volume pair is
	// the one actually carrying media.
	assert.Equal(t, "udp", iceTransportFromStats(report))
}

func TestICETransportFromStatsRemoteCandidateFallback(t *testing.T) {
	report := statsReport(
		candidate("remote", "TCP"),
		pair("pair", "missing-local", "remote", true, webrtc.StatsICECandidatePairStateSucceeded, 1),
	)

	assert.Equal(t, "tcp", iceTransportFromStats(report))
}

func TestICETransportFromStatsEmpty(t *testing.T) {
	assert.Empty(t, iceTransportFromStats(webrtc.StatsReport{}))
	assert.Empty(t, iceTransportFromStats(statsReport(candidate("local", "udp"))))
}

func TestSelectedCandidatePairPrefersNominatedSucceeded(t *testing.T) {
	pairs := []webrtc.ICECandidatePairStats{
		pair("succeeded", "local", "remote", false, webrtc.StatsICECandidatePairStateSucceeded, 1000),
		pair("nominated", "local", "remote", true, webrtc.StatsICECandidatePairStateSucceeded, 5),
	}

	selected, ok := selectedCandidatePair(pairs)
	require.True(t, ok)
	assert.Equal(t, "nominated", selected.ID)
}
