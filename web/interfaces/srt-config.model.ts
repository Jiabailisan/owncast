// SRT ingest configuration, matching models.SRTConfig's JSON tags
// (owncast/models/srtConfig.go) and the admin contract
// (docs/architecture/api-contract.md section 2).
//
// Secrets are write-only: the API returns passphraseSet instead of a value.

import { PendingRestart } from './settings-metadata.model';

export interface SRTConfig {
  enabled: boolean;
  bindAddress: string;
  port: number;
  streamIdFormats: string[];
  requireStreamId: boolean;
  allowedCIDRs: string[];
  deniedCIDRs: string[];
  authFailureRateLimit: number;
  receiverLatencyMs: number;
  peerLatencyMs: number;
  flowControlWindowPackets: number;
  mss: number;
  payloadSize: number;
  // Only sent on update; GET responses report passphraseSet instead.
  passphrase?: string;
  passphraseSet?: boolean;
  enforcedEncryption: boolean;
  peerIdleTimeoutMs: number;
  ipTTL: number;
  ipTOS: number;
  logTopics: string[];
  debugStatsIntervalMs: number;
  driftTracer: boolean;
  maxPendingHandshakes: number;
}

// Live listener state reported alongside the config
// (webserver/handlers/admin/liveStreamingConfig.go srtListenerState).
export interface SRTActiveSession {
  id: string;
  remoteAddr: string;
  streamKeyID: string;
  inputFormat: string;
  startedAt: string;
  durationSeconds: number;
  payloadBytes: number;
  version: number;
  encrypted: boolean;
}

export interface SRTListenerStatus {
  enabled: boolean;
  // Listener service state, e.g. "running"/"stopped".
  state: string;
  listening: boolean;
  port: number;
  boundAddress: string;
  lastError: string;
  pendingRestart: boolean;
  activeSession: SRTActiveSession | null;
}

// GET /api/admin/config/srt
export interface SRTConfigResponse {
  config: SRTConfig;
  defaults: SRTConfig;
  pendingRestart: PendingRestart;
  listener: SRTListenerStatus;
}

// PUT /api/admin/config/srt returns the same state plus success/message.
export interface SRTConfigSaveResponse extends SRTConfigResponse {
  success: boolean;
  message?: string;
}
