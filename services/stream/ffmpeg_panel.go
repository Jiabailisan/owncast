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
// active stream, for the admin FFmpeg panel (spec 6.4). Each entry's argv is the
// exact argv passed to exec with configured secrets redacted. The isolated
// process model reports both the HLS and WebRTC processes.
func (s *Service) FFmpegProcesses() []models.FFmpegProcessStatus {
	transcoders := []*transcoder.Transcoder{}
	if tr := s.getTranscoder(); tr != nil {
		transcoders = append(transcoders, tr)
	}
	if tr := s.getWebRTCTranscoder(); tr != nil {
		transcoders = append(transcoders, tr)
	}
	if len(transcoders) == 0 {
		return []models.FFmpegProcessStatus{}
	}

	processes := make([]models.FFmpegProcessStatus, 0, len(transcoders))
	for _, tr := range transcoders {
		processes = append(processes, s.transcoderProcessStatus(tr))
	}
	return processes
}

// transcoderProcessStatus converts one transcoder snapshot into the panel
// payload: exact argv, secrets redacted, structured sections.
func (s *Service) transcoderProcessStatus(tr *transcoder.Transcoder) models.FFmpegProcessStatus {
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
			"input":       nonNilSection(sections.Input),
			"video":       nonNilSection(sections.Video),
			"audio":       nonNilSection(sections.Audio),
			"output":      nonNilSection(sections.Output),
			"other":       nonNilSection(sections.Other),
			"environment": nonNilSection(sections.Environment),
		},
	}
	if !snapshot.Running {
		process.Purpose = append(process.Purpose, "exited")
	}
	return process
}

// nonNilSection keeps the panel JSON stable: every section is always an array,
// never null.
func nonNilSection(section []string) []string {
	if section == nil {
		return []string{}
	}
	return section
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
