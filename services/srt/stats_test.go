package srt

import (
	"testing"
	"time"

	gosrt "github.com/datarhei/gosrt"
)

func TestMapStatisticsComputesDeltas(t *testing.T) {
	previous := gosrt.Statistics{
		Accumulated: gosrt.StatisticsAccumulated{
			PktRecv:        100,
			PktRecvLoss:    2,
			PktRecvRetrans: 7,
			PktRecvDrop:    1,
			PktSentNAK:     3,
			PktRecvUnique:  90,
		},
	}
	current := gosrt.Statistics{
		Accumulated: gosrt.StatisticsAccumulated{
			PktRecv:        200,
			PktRecvLoss:    4,
			PktRecvRetrans: 10,
			PktRecvDrop:    3,
			PktSentNAK:     5,
			PktRecvUnique:  180,
		},
		Instantaneous: gosrt.StatisticsInstantaneous{
			MsRTT:            12.5,
			MsRecvTsbPdDelay: 120,
			MsSendTsbPdDelay: 120,
			PktFlowWindow:    25600,
			MsRecvBuf:        80,
			MbpsLinkCapacity: 25,
			MbpsRecvRate:     3.5,
			PktRecvLossRate:  1.5,
		},
	}

	stats := mapStatistics(previous, current, 10000, 26320, time.Second)

	if stats.BitrateKbps == nil {
		t.Fatal("BitrateKbps should be populated")
	}
	wantBitrate := float64(26320-10000) * 8 / 1000
	if *stats.BitrateKbps != wantBitrate {
		t.Errorf("BitrateKbps = %v, want %v", *stats.BitrateKbps, wantBitrate)
	}

	if stats.PacketLossPct == nil {
		t.Fatal("PacketLossPct should be populated")
	}
	wantLoss := 2.0 / 102.0 * 100
	if *stats.PacketLossPct != wantLoss {
		t.Errorf("PacketLossPct = %v, want %v", *stats.PacketLossPct, wantLoss)
	}

	if stats.RTTMs == nil || *stats.RTTMs != 12.5 {
		t.Errorf("RTTMs = %v, want 12.5", stats.RTTMs)
	}
	if stats.RetransmittedPackets == nil || *stats.RetransmittedPackets != 3 {
		t.Errorf("RetransmittedPackets = %v, want 3", stats.RetransmittedPackets)
	}
	if stats.DroppedPackets == nil || *stats.DroppedPackets != 2 {
		t.Errorf("DroppedPackets = %v, want 2", stats.DroppedPackets)
	}

	wantExtra := map[string]float64{
		"recv_latency_ms":     120,
		"send_latency_ms":     120,
		"flow_window_pkts":    25600,
		"recv_buf_ms":         80,
		"link_capacity_mbps":  25,
		"estimated_recv_mbps": 3.5,
		"pkt_sent_nak":        2,
		"pkt_recv_loss_rate":  1.5,
		"pkt_recv_unique":     90,
		"payload_bytes_total": 26320,
		"stats_interval_ms":   1000,
	}
	for key, want := range wantExtra {
		got, ok := stats.Extra[key]
		if !ok {
			t.Errorf("Extra[%q] missing", key)
			continue
		}
		if got != want {
			t.Errorf("Extra[%q] = %v, want %v", key, got, want)
		}
	}
}

func TestMapStatisticsFirstSampleAndEmptyWindow(t *testing.T) {
	current := gosrt.Statistics{
		Accumulated: gosrt.StatisticsAccumulated{
			PktRecv: 50,
		},
		Instantaneous: gosrt.StatisticsInstantaneous{MsRTT: 4},
	}

	stats := mapStatistics(gosrt.Statistics{}, current, 0, 5000, time.Second)
	if stats.BitrateKbps == nil || *stats.BitrateKbps != 40 {
		t.Errorf("first sample BitrateKbps = %v, want 40", stats.BitrateKbps)
	}
	if stats.PacketLossPct == nil || *stats.PacketLossPct != 0 {
		t.Errorf("loss should be 0 when packets arrived without loss: %v", stats.PacketLossPct)
	}

	empty := mapStatistics(gosrt.Statistics{}, gosrt.Statistics{}, 100, 100, time.Second)
	if empty.PacketLossPct != nil {
		t.Errorf("loss should be nil when nothing was measured: %v", *empty.PacketLossPct)
	}
	if empty.BitrateKbps == nil || *empty.BitrateKbps != 0 {
		t.Errorf("bitrate should be zero when no payload arrived: %v", empty.BitrateKbps)
	}
}

func TestMapStatisticsUnderflowProtection(t *testing.T) {
	previous := gosrt.Statistics{
		Accumulated: gosrt.StatisticsAccumulated{PktRecv: 100, PktRecvLoss: 5},
	}
	current := gosrt.Statistics{
		Accumulated: gosrt.StatisticsAccumulated{PktRecv: 10, PktRecvLoss: 0},
	}

	stats := mapStatistics(previous, current, 5000, 100, time.Second)
	if stats.BitrateKbps == nil || *stats.BitrateKbps != 0 {
		t.Errorf("bitrate should clamp a reordered payload counter: %v", stats.BitrateKbps)
	}
	if stats.RetransmittedPackets == nil || *stats.RetransmittedPackets != 0 {
		t.Errorf("counters should clamp to zero on reset: %v", stats.RetransmittedPackets)
	}
}

func TestSubtractCounter(t *testing.T) {
	if got := subtractCounter(5, 3); got != 2 {
		t.Errorf("subtractCounter(5,3) = %d, want 2", got)
	}
	if got := subtractCounter(3, 5); got != 0 {
		t.Errorf("subtractCounter(3,5) = %d, want 0", got)
	}
}
