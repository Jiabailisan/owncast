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
    } else if (type === 'candidate-pair' && stat.nominated === true
      && typeof stat.currentRoundTripTime === 'number') {
      summary.roundTripTimeMs = Math.round((stat.currentRoundTripTime as number) * 1000);
    }
  });

  return { summary, videoSample };
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
