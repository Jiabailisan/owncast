// Client-side WebRTC playback stats beacon.
//
// HLS reports playback health through CMCD, which does not exist for WebRTC.
// This module samples RTCPeerConnection.getStats() every few seconds and posts
// a small summary to POST /api/metrics/webrtc so the admin viewer table can
// show client-reported values. Posting is failure-tolerant and must never
// surface as a console.error: transient telemetry failures are expected.

import { WEBRTC_METRICS } from '../../../utils/apis';

export interface WebRTCStatsSummary {
  sessionId: string;
  decodedWidth?: number;
  decodedHeight?: number;
  fps?: number;
  packetsLost?: number;
  jitterMs?: number;
  roundTripTimeMs?: number;
}

export interface WebRTCStatsBeaconOptions {
  pc: RTCPeerConnection;
  sessionId: string;
  endpoint?: string;
  intervalMs?: number;
  fetchImpl?: typeof fetch;
  getStatsImpl?: () => Promise<RTCStatsReport>;
}

export interface WebRTCStatsBeacon {
  stop: () => void;
  sampleOnce: () => Promise<WebRTCStatsSummary | null>;
}

interface InboundVideoSample {
  framesDecoded: number;
  timestampSeconds: number;
}

// Convert a WebRTC stats report into the compact beacon payload. Missing
// values are omitted (the server already treats absent values as unknown).
export function summarizeWebRTCStats(
  report: RTCStatsReport,
  previousVideoSample?: InboundVideoSample | null,
): { summary: Omit<WebRTCStatsSummary, 'sessionId'>; videoSample: InboundVideoSample | null } {
  const summary: Omit<WebRTCStatsSummary, 'sessionId'> = {};
  let videoSample: InboundVideoSample | null = null;

  report.forEach((stat: Record<string, unknown>) => {
    const type = stat.type as string;
    const kind = stat.kind as string | undefined;

    if (type === 'inbound-rtp' && kind === 'video') {
      if (typeof stat.frameWidth === 'number') summary.decodedWidth = stat.frameWidth;
      if (typeof stat.frameHeight === 'number') summary.decodedHeight = stat.frameHeight;
      if (typeof stat.packetsLost === 'number') summary.packetsLost = stat.packetsLost;
      if (typeof stat.jitter === 'number') summary.jitterMs = Math.round(stat.jitter * 10000) / 10;

      const framesDecoded = stat.framesDecoded as number | undefined;
      const timestamp = stat.timestamp as number | undefined;
      if (typeof framesDecoded === 'number' && typeof timestamp === 'number') {
        videoSample = { framesDecoded, timestampSeconds: timestamp / 1000 };
        if (previousVideoSample) {
          const frameDelta = framesDecoded - previousVideoSample.framesDecoded;
          const timeDelta = videoSample.timestampSeconds - previousVideoSample.timestampSeconds;
          if (frameDelta >= 0 && timeDelta > 0) {
            summary.fps = Math.round((frameDelta / timeDelta) * 10) / 10;
          }
        }
      }
    }

    if (type === 'remote-inbound-rtp' && typeof stat.roundTripTime === 'number') {
      summary.roundTripTimeMs = Math.round(stat.roundTripTime * 1000);
    } else if (
      type === 'candidate-pair' &&
      stat.nominated === true &&
      typeof stat.currentRoundTripTime === 'number'
    ) {
      summary.roundTripTimeMs = Math.round((stat.currentRoundTripTime as number) * 1000);
    }
  });

  return { summary, videoSample };
}

// One Stats-for-nerds sample derived from RTCPeerConnection.getStats(). The
// previous sample is needed for bitrate deltas. Missing values stay null so the
// panel renders N/A.
export interface WebRTCStatsPanelSample {
  videoBitrateKbps: number | null;
  audioBitrateKbps: number | null;
  connectionSpeedKbps: number | null;
  rttMs: number | null;
  jitterMs: number | null;
  packetsLost: number | null;
  packetsReceived: number | null;
  frameWidth: number | null;
  frameHeight: number | null;
  framesPerSecond: number | null;
  framesDropped: number | null;
  framesDecoded: number | null;
  videoCodec: string | null;
  audioCodec: string | null;
  timestamp: number;
  videoBytes: number;
  audioBytes: number;
}

interface WebRTCBytesSample {
  timestamp: number;
  videoBytes: number;
  audioBytes: number;
}

export function collectWebRTCStatsPanel(
  report: RTCStatsReport,
  previous?: WebRTCBytesSample | null,
): { sample: WebRTCStatsPanelSample; bytes: WebRTCBytesSample } {
  const codecs: Record<string, string> = {};
  const inbound: { video?: Record<string, unknown>; audio?: Record<string, unknown> } = {};
  let candidateRtt: number | null = null;

  report.forEach((stat: Record<string, unknown>) => {
    if (stat.type === 'codec') {
      const mimeType = typeof stat.mimeType === 'string' ? stat.mimeType : '';
      codecs[String(stat.id)] = mimeType;
    }
    if (stat.type === 'inbound-rtp') {
      if (stat.kind === 'video') inbound.video = stat;
      if (stat.kind === 'audio') inbound.audio = stat;
    }
    if (
      stat.type === 'candidate-pair' &&
      (stat.nominated === true || stat.state === 'succeeded') &&
      typeof stat.currentRoundTripTime === 'number'
    ) {
      candidateRtt = stat.currentRoundTripTime * 1000;
    }
  });

  const video = inbound.video || {};
  const audio = inbound.audio || {};
  const timestamp = Date.now();
  const videoBytes = Number(video.bytesReceived) || 0;
  const audioBytes = Number(audio.bytesReceived) || 0;

  const kbps = (bytes: number, seconds: number): number | null => {
    if (!Number.isFinite(bytes) || seconds <= 0 || bytes < 0) return null;
    return (bytes * 8) / seconds / 1000;
  };

  let videoBitrateKbps: number | null = null;
  let audioBitrateKbps: number | null = null;
  let connectionSpeedKbps: number | null = null;
  if (previous && timestamp > previous.timestamp) {
    const seconds = (timestamp - previous.timestamp) / 1000;
    videoBitrateKbps = kbps(videoBytes - previous.videoBytes, seconds);
    audioBitrateKbps = kbps(audioBytes - previous.audioBytes, seconds);
    if (videoBitrateKbps !== null || audioBitrateKbps !== null) {
      connectionSpeedKbps = (videoBitrateKbps || 0) + (audioBitrateKbps || 0);
    }
  }

  const videoCodecId = video.codecId ? String(video.codecId) : '';
  const audioCodecId = audio.codecId ? String(audio.codecId) : '';
  const simplifyCodec = (mime: string | undefined): string | null => {
    if (!mime) return null;
    const matched = mime.match(/\/([^;]+)/i);
    return matched ? matched[1].toUpperCase() : mime;
  };

  return {
    sample: {
      videoBitrateKbps,
      audioBitrateKbps,
      connectionSpeedKbps,
      rttMs: candidateRtt,
      jitterMs: typeof video.jitter === 'number' ? video.jitter * 1000 : null,
      packetsLost: typeof video.packetsLost === 'number' ? video.packetsLost : null,
      packetsReceived: typeof video.packetsReceived === 'number' ? video.packetsReceived : null,
      frameWidth: typeof video.frameWidth === 'number' ? video.frameWidth : null,
      frameHeight: typeof video.frameHeight === 'number' ? video.frameHeight : null,
      framesPerSecond: typeof video.framesPerSecond === 'number' ? video.framesPerSecond : null,
      framesDropped: typeof video.framesDropped === 'number' ? video.framesDropped : null,
      framesDecoded: typeof video.framesDecoded === 'number' ? video.framesDecoded : null,
      videoCodec: simplifyCodec(codecs[videoCodecId]),
      audioCodec: simplifyCodec(codecs[audioCodecId]),
      timestamp,
      videoBytes,
      audioBytes,
    },
    bytes: { timestamp, videoBytes, audioBytes },
  };
}

export function startWebRTCStatsBeacon(options: WebRTCStatsBeaconOptions): WebRTCStatsBeacon {
  const {
    pc,
    sessionId,
    endpoint = WEBRTC_METRICS,
    intervalMs = 5000,
    fetchImpl = fetch,
    getStatsImpl,
  } = options;

  let previousVideoSample: InboundVideoSample | null = null;
  let stopped = false;

  const sampleOnce = async (): Promise<WebRTCStatsSummary | null> => {
    if (stopped) return null;
    try {
      const report = await (getStatsImpl ? getStatsImpl() : pc.getStats());
      const { summary, videoSample } = summarizeWebRTCStats(report, previousVideoSample);
      previousVideoSample = videoSample;
      const payload: WebRTCStatsSummary = { sessionId, ...summary };
      if (stopped) return payload;
      try {
        await fetchImpl(endpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
      } catch {
        // Telemetry is best-effort; do not surface transient network errors.
      }
      return payload;
    } catch {
      // getStats can fail while the peer connection is closing.
      return null;
    }
  };

  const timer = setInterval(() => {
    void sampleOnce();
  }, intervalMs);

  return {
    stop: () => {
      stopped = true;
      clearInterval(timer);
    },
    sampleOnce,
  };
}
