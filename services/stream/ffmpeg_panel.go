package stream

import (
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/transcoder"
)

// setTranscoder replaces the active transcoder under the lock.
func (s *Service) setTranscoder(t *transcoder.Transcoder) {
	s.transcoderMu.Lock()
	defer s.transcoderMu.Unlock()
	s.transcoder = t
}

// getTranscoder returns the active transcoder, if any.
func (s *Service) getTranscoder() *transcoder.Transcoder {
	s.transcoderMu.RLock()
	defer s.transcoderMu.RUnlock()
	return s.transcoder
}

// FFmpegProcesses returns the supervised transcoder processes feeding the
// active stream, for the admin FFmpeg panel (spec 6.4). The argv is the exact
// argv passed to exec with configured secrets redacted.
func (s *Service) FFmpegProcesses() []models.FFmpegProcessStatus {
	tr := s.getTranscoder()
	if tr == nil {
		return []models.FFmpegProcessStatus{}
	}

	snapshot := tr.Snapshot()
	secrets := s.configuredSecrets()
	argv := transcoder.RedactArgv(snapshot.Argv, secrets)
	sections := transcoder.ClassifyArgv(argv, nil)

	process := models.FFmpegProcessStatus{
		ID:            snapshot.ID,
		Purpose:       snapshot.Purpose,
		PID:           snapshot.PID,
		StartedAt:     snapshot.StartedAt,
		UptimeSeconds: snapshot.UptimeSeconds,
		RestartCount:  snapshot.RestartCount,
		Argv:          argv,
		Sections: map[string][]string{
			"input":       sections.Input,
			"video":       sections.Video,
			"audio":       sections.Audio,
			"output":      sections.Output,
			"other":       sections.Other,
			"environment": sections.Environment,
		},
	}
	if !snapshot.Running {
		process.Purpose = append(process.Purpose, "exited")
	}
	return []models.FFmpegProcessStatus{process}
}

// configuredSecrets collects the secret values that must never appear in the
// FFmpeg panel or its copy output.
func (s *Service) configuredSecrets() []string {
	secrets := make([]string, 0, 4)

	srtConfig := s.configRepository.GetSRTConfig()
	if srtConfig.Passphrase != "" {
		secrets = append(secrets, srtConfig.Passphrase)
	}

	webrtcConfig := s.configRepository.GetWebRTCConfig()
	for _, server := range webrtcConfig.TURNServers {
		if server.Credential != "" {
			secrets = append(secrets, server.Credential)
		}
		if server.StaticSecret != "" {
			secrets = append(secrets, server.StaticSecret)
		}
	}
	return secrets
}
