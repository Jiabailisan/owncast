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

export function makeEmptyWebRTCConfig(): WebRTCConfig {
  return {
    enabled: false,
    domain: '',
    publicIPs: [],
    strictDomain: false,
    allowedOrigins: [],
    maxViewers: 100,
    maxViewersPerIP: 0,
    signalingRateLimit: 30,
    limitExceededBehavior: 'reject',
    sessionIdleTimeoutSeconds: 30,
    bindAddress: '0.0.0.0',
    udpMode: 'mux',
    udpPort: 8189,
    udpPortMin: 50000,
    udpPortMax: 50100,
    tcpEnabled: true,
    tcpPort: 8189,
    networkTypes: ['udp4', 'udp6', 'tcp4', 'tcp6'],
    interfaceAllow: [],
    interfaceDeny: [],
    mdns: false,
    stunServers: [],
    turnServers: [],
    embeddedTurn: false,
    embeddedTurnPort: 3478,
    embeddedTurnRealm: 'owncast',
    iceLite: false,
    iceDisconnectedTimeoutMs: 5000,
    iceFailedTimeoutMs: 25000,
    iceKeepaliveIntervalMs: 2000,
    h264Profile: 'constrained-baseline',
    h264Level: 'auto',
    keyframeIntervalSeconds: 1,
    opusBitrateKbps: 96,
    opusFrameMs: 20,
    opusFEC: true,
    opusDTX: false,
    rtpPacketSize: 1200,
    nack: true,
    twcc: true,
    bandwidthEstimation: false,
    maxBitratePerViewerKbps: 0,
    minBitratePerViewerKbps: 0,
    rtpReorderBufferMs: 0,
    fallbackToHls: true,
  };
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
