package stream

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// recordHLSSegmentWritten updates a rolling EWMA of measured output bitrate
// (in kbps) for the HLS variant whose segment was just written to disk.
// Offline segments (stream-offline-*.ts) are ignored.
func (s *Service) recordHLSSegmentWritten(localFilePath string) {
	base := filepath.Base(localFilePath)
	if !strings.HasSuffix(base, ".ts") || strings.Contains(base, "offline") {
		return
	}

	hlsIndex, err := strconv.Atoi(filepath.Base(filepath.Dir(localFilePath)))
	if err != nil || hlsIndex < 0 {
		return
	}

	broadcast := s.GetCurrentBroadcast()
	if broadcast == nil {
		return
	}

	// Map sequential HLS directory index back to configured variant index.
	configIndex := -1
	seq := 0
	for idx, variant := range broadcast.OutputSettings {
		if !variant.HLSEnabled() {
			continue
		}
		if seq == hlsIndex {
			configIndex = idx
			break
		}
		seq++
	}
	if configIndex < 0 {
		return
	}

	segSec := broadcast.LatencyLevel.SecondsPerSegment
	if segSec <= 0 {
		return
	}

	info, err := os.Stat(localFilePath)
	if err != nil || info.Size() <= 0 {
		return
	}

	kbps := float64(info.Size()) * 8 / float64(segSec) / 1000
	s.hlsBitratesMu.Lock()
	if s.hlsMeasuredBitrates == nil {
		s.hlsMeasuredBitrates = make(map[int]float64)
	}
	if prev, ok := s.hlsMeasuredBitrates[configIndex]; ok && prev > 0 {
		s.hlsMeasuredBitrates[configIndex] = prev*0.6 + kbps*0.4
	} else {
		s.hlsMeasuredBitrates[configIndex] = kbps
	}
	s.hlsBitratesMu.Unlock()
}

func (s *Service) clearHLSMeasuredBitrates() {
	s.hlsBitratesMu.Lock()
	s.hlsMeasuredBitrates = nil
	s.hlsBitratesMu.Unlock()
}

// HLSMeasuredBitrates returns a copy of measured HLS output bitrates (kbps)
// keyed by configured output variant index.
func (s *Service) HLSMeasuredBitrates() map[int]float64 {
	s.hlsBitratesMu.RLock()
	defer s.hlsBitratesMu.RUnlock()
	if len(s.hlsMeasuredBitrates) == 0 {
		return nil
	}
	out := make(map[int]float64, len(s.hlsMeasuredBitrates))
	for k, v := range s.hlsMeasuredBitrates {
		out[k] = v
	}
	return out
}
