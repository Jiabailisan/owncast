// WebRTC delivery configuration, matching models.WebRTCConfig's JSON tags
// (owncast/models/webrtcConfig.go) and the admin contract
// (docs/architecture/api-contract.md sections 2 and 3).

import { PendingRestart } from './settings-metadata.model';

// TURN credentials/static secrets are write-only: the API returns
// credentialSet / staticSecretSet instead of the values.
export interface TURNServerConfig {
  url: string;
  username?: string;
  credential?: string;
  credentialSet?: boolean;
  staticSecret?: string;
  staticSecretSet?: boolean;
}

export interface WebRTCConfig {
  enabled: boolean;
  domain: string;
  publicIPs: string[];
  strictDomain: boolean;
  allowedOrigins: string[];
  maxViewers: number;
  maxViewersPerIP: number;
  signalingRateLimit: number;
  limitExceededBehavior: string;
  sessionIdleTimeoutSeconds: number;
  bindAddress: string;
  udpMode: string;
  udpPort: number;
  udpPortMin: number;
  udpPortMax: number;
  tcpEnabled: boolean;
  tcpPort: number;
  networkTypes: string[];
  interfaceAllow: string[];
  interfaceDeny: string[];
  mdns: boolean;
  stunServers: string[];
  turnServers: TURNServerConfig[];
  embeddedTurn: boolean;
  embeddedTurnPort: number;
  embeddedTurnRealm: string;
  iceLite: boolean;
  iceDisconnectedTimeoutMs: number;
  iceFailedTimeoutMs: number;
  iceKeepaliveIntervalMs: number;
  h264Profile: string;
  h264Level: string;
  keyframeIntervalSeconds: number;
  opusBitrateKbps: number;
  opusFrameMs: number;
  opusFEC: boolean;
  opusDTX: boolean;
  rtpPacketSize: number;
  nack: boolean;
  twcc: boolean;
  bandwidthEstimation: boolean;
  maxBitratePerViewerKbps: number;
  minBitratePerViewerKbps: number;
  rtpReorderBufferMs: number;
  fallbackToHls: boolean;
}

// Live WebRTC service snapshot included with the config
// (services/webrtc.StatusSnapshot).
export interface WebRTCServiceStatus {
  enabled: boolean;
  listeningAddress?: string;
  activeSessions: number;
  capacity: { used: number; limit: number };
  warnings: string[];
}

// GET /api/admin/config/webrtc
export interface WebRTCConfigResponse {
  config: WebRTCConfig;
  defaults: WebRTCConfig;
  pendingRestart: PendingRestart;
  service: WebRTCServiceStatus;
}

// PUT /api/admin/config/webrtc returns the same state plus success/message.
export interface WebRTCConfigSaveResponse extends WebRTCConfigResponse {
  success: boolean;
  message?: string;
}

// POST /api/admin/config/webrtc/test-domain
export interface WebRTCDomainTestResponse {
  domain: string;
  resolvedIPs: string[];
  advertisedIPs: string[];
  strictDomain: boolean;
  error: string;
}

export interface PreflightFFmpeg {
  path: string;
  version: string;
  h264Encoders: string[];
  opusEncoder: boolean;
  srtProtocol: boolean;
  warnings: string[];
}

export interface PreflightPort {
  protocol: 'udp' | 'tcp';
  port: number;
  available: boolean;
  error?: string;
}

export interface PreflightWarning {
  code: string;
  message: string;
}

// POST /api/admin/config/preflight
export interface PreflightResponse {
  ffmpeg: PreflightFFmpeg;
  ports: PreflightPort[];
  warnings: PreflightWarning[];
}
