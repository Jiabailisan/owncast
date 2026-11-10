// Shared types for the ported overlay player controls.

export type TransportPreference = 'auto' | 'webrtc' | 'hls';
export type ActiveTransport = 'webrtc' | 'hls';

export interface PlayerQuality {
  id: string;
  name: string;
}

// Normalized values rendered by the Stats for nerds panel. Every numeric field
// is optional: transports only expose what they can measure and the panel shows
// N/A instead of estimating (repo-wide policy).
export interface StatsSnapshot {
  collecting: boolean;
  isWebRTC: boolean;
  streamId: string;
  viewport: string;
  frames: string;
  resolution: string;
  optimalResolution: string;
  volume: string;
  normalizedVolume: string;
  codecs: string;
  // Negotiated SDP summary (codec/profile-level-id/candidates) for the stats
  // panel. HLS has no SDP negotiation, so it renders an em dash.
  sdp: string;
  qualityName: string;
  videoBitrateKbps: number | null;
  audioBitrateKbps: number | null;
  connectionSpeedKbps: number | null;
  rttMs: number | null;
  jitterMs: number | null;
  packetsLost: number | null;
  packetsReceived: number | null;
  bufferSeconds: number | null;
  liveLatencySeconds: number | null;
  liveMode: string;
  date: string;
}

export interface SparklineState {
  video: number[];
  audio: number[];
}

export function makeEmptyStats(isWebRTC = false): StatsSnapshot {
  return {
    collecting: true,
    isWebRTC,
    streamId: 'N/A',
    viewport: 'N/A',
    frames: 'N/A',
    resolution: 'N/A',
    optimalResolution: 'N/A',
    volume: 'N/A',
    normalizedVolume: 'N/A',
    codecs: isWebRTC ? 'H.264 / Opus (WebRTC)' : 'H.264 / AAC (HLS)',
    sdp: '—',
    qualityName: 'Auto',
    videoBitrateKbps: null,
    audioBitrateKbps: null,
    connectionSpeedKbps: null,
    rttMs: null,
    jitterMs: null,
    packetsLost: null,
    packetsReceived: null,
    bufferSeconds: null,
    liveLatencySeconds: null,
    liveMode: isWebRTC ? 'WebRTC' : 'HLS',
    date: 'N/A',
  };
}
