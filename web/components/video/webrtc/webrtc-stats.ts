// Client-side WebRTC playback stats beacon.
//
// HLS reports playback health through CMCD, which does not exist for WebRTC.
// This module samples RTCPeerConnection.getStats() every few seconds and posts
// a small summary to POST /api/metrics/webrtc so the admin viewer table can
// show client-reported values. Posting is failure-tolerant and must never
// surface as a console.error: transient telemetry failures are expected.

import { WEBRTC_METRICS } from '../../../utils/apis';
import { WebRTCVariantInfo } from '../../../interfaces/client-config.model';

export interface WebRTCStatsSummary {
  sessionId: string;
  decodedWidth?: number;
  decodedHeight?: number;
  fps?: number;
  packetsLost?: number;
  packetsReceived?: number;
  packetLossPct?: number;
  jitterMs?: number;
  roundTripTimeMs?: number;
  // Transport of the selected ICE candidate pair ("udp" / "tcp"), used by the
  // admin viewer table and the Stats for nerds "WebRTC-U / WebRTC-T" label.
  transport?: 'udp' | 'tcp';
  // Inbound video bitrate measured between beacon samples (admin row bitrate).
  videoBitrateKbps?: number;
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
  bytesReceived?: number;
}

export type IceTransport = 'udp' | 'tcp';

// A receiver-reported or window-measured frame rate is only accepted when it is
// physically plausible; values outside 1..240 are dropped instead of being
// forwarded to the admin table (a 571 FPS row came from an unvalidated delta).
export function plausibleFrameRate(value: unknown): number | null {
  const numeric = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(numeric) || numeric < 1 || numeric > 240) return null;
  return Math.round(numeric * 10) / 10;
}

// Shared packet-loss formula for the panel and the client stats beacon:
// lost / (lost + received) * 100, rounded to two decimals. Negative cumulative
// counters (common after receiver restarts) are clamped to zero.
export function packetLossPercent(
  packetsLost: number | null | undefined,
  packetsReceived: number | null | undefined,
): number | null {
  if (
    packetsLost === null ||
    packetsLost === undefined ||
    packetsReceived === null ||
    packetsReceived === undefined ||
    !Number.isFinite(packetsLost) ||
    !Number.isFinite(packetsReceived)
  ) {
    return null;
  }
  const lost = Math.max(0, packetsLost);
  const received = Math.max(0, packetsReceived);
  const total = lost + received;
  if (total <= 0) return 0;
  return Math.round((lost / total) * 10000) / 100;
}

// "WebRTC-U" / "WebRTC-T" for the Live Mode row (per the field report), with a
// plain "WebRTC" when the ICE transport cannot be determined yet.
export function webRTCLiveMode(transport: IceTransport | null | undefined): string {
  if (transport === 'udp') return 'WebRTC-U';
  if (transport === 'tcp') return 'WebRTC-T';
  return 'WebRTC';
}

// "2560x1440@60fps" style resolution for the Current / Optimal Res rows.
export function formatWebRTCResolution(
  width: number | null | undefined,
  height: number | null | undefined,
  fps: number | null,
): string {
  if (!width || !height || width <= 0 || height <= 0) return 'N/A';
  const suffix = fps !== null && fps > 0 ? '@' + Math.round(fps) + 'fps' : '';
  return width + 'x' + height + suffix;
}

// Transport of the selected ICE candidate pair. Prefer a nominated+succeeded
// pair, then any nominated pair, then any succeeded pair with a known
// local/remote candidate protocol.
export function selectedIceTransport(report: RTCStatsReport): IceTransport | null {
  const candidateProtocols = new Map<string, string>();
  const pairs: Record<string, unknown>[] = [];
  report.forEach((stat: Record<string, unknown>) => {
    // Chrome reports ICE candidate stats as "local-candidate"/"remote-candidate"
    // while other implementations use the spec name "candidate".
    if (
      stat.type === 'candidate' ||
      stat.type === 'local-candidate' ||
      stat.type === 'remote-candidate'
    ) {
      candidateProtocols.set(String(stat.id), String(stat.protocol || '').toLowerCase());
    }
    if (stat.type === 'candidate-pair') pairs.push(stat);
  });
  const nominated = pairs.filter(pair => pair.nominated === true);
  const succeeded = pairs.filter(pair => pair.state === 'succeeded');
  const selected =
    nominated.find(pair => pair.state === 'succeeded') ||
    nominated[0] ||
    succeeded.find(pair => Number(pair.bytesReceived) > 0) ||
    succeeded[0] ||
    null;
  if (!selected) return null;
  const protocol =
    candidateProtocols.get(String(selected.localCandidateId || '')) ||
    candidateProtocols.get(String(selected.remoteCandidateId || ''));
  if (protocol === 'udp') return 'udp';
  if (protocol === 'tcp') return 'tcp';
  return null;
}

// Convert a WebRTC stats report into the compact beacon payload. Missing
// values are omitted (the server already treats absent values as unknown).
export function summarizeWebRTCStats(
  report: RTCStatsReport,
  previousVideoSample?: InboundVideoSample | null,
): { summary: Omit<WebRTCStatsSummary, 'sessionId'>; videoSample: InboundVideoSample | null } {
  const summary: Omit<WebRTCStatsSummary, 'sessionId'> = {};
  let videoSample: InboundVideoSample | null = null;
  let reportedFrameRate: number | null = null;
  let measuredFrameRate: number | null = null;

  report.forEach((stat: Record<string, unknown>) => {
    const type = stat.type as string;
    const kind = stat.kind as string | undefined;

    if (type === 'inbound-rtp' && kind === 'video') {
      if (typeof stat.frameWidth === 'number') summary.decodedWidth = stat.frameWidth;
      if (typeof stat.frameHeight === 'number') summary.decodedHeight = stat.frameHeight;
      const packetsLost =
        typeof stat.packetsLost === 'number' ? Math.max(0, stat.packetsLost) : null;
      const packetsReceived =
        typeof stat.packetsReceived === 'number' ? Math.max(0, stat.packetsReceived) : null;
      if (packetsLost !== null) summary.packetsLost = packetsLost;
      if (packetsReceived !== null) summary.packetsReceived = packetsReceived;
      const lossPct = packetLossPercent(packetsLost, packetsReceived);
      if (lossPct !== null) summary.packetLossPct = lossPct;
      if (typeof stat.jitter === 'number') summary.jitterMs = Math.round(stat.jitter * 10000) / 10;
      reportedFrameRate = plausibleFrameRate(stat.framesPerSecond);

      const framesDecoded = stat.framesDecoded as number | undefined;
      const timestamp = stat.timestamp as number | undefined;
      const bytesReceived =
        typeof stat.bytesReceived === 'number' ? Math.max(0, stat.bytesReceived) : undefined;
      if (typeof framesDecoded === 'number' && typeof timestamp === 'number') {
        videoSample = { framesDecoded, timestampSeconds: timestamp / 1000, bytesReceived };
        if (previousVideoSample) {
          if (bytesReceived !== undefined && previousVideoSample.bytesReceived !== undefined) {
            const byteDelta = bytesReceived - previousVideoSample.bytesReceived;
            const bitrateSeconds =
              videoSample.timestampSeconds - previousVideoSample.timestampSeconds;
            if (byteDelta > 0 && bitrateSeconds >= 1) {
              const kbps = Math.round((byteDelta * 8) / bitrateSeconds / 10) / 100;
              if (kbps > 0 && kbps <= 1_000_000) summary.videoBitrateKbps = kbps;
            }
          }
          const frameDelta = framesDecoded - previousVideoSample.framesDecoded;
          const timeDelta = videoSample.timestampSeconds - previousVideoSample.timestampSeconds;
          // Measured decoded frames over a >=1s window are the truest values;
          // shorter windows or absurd rates are ignored (571 FPS report).
          if (frameDelta > 0 && timeDelta >= 1) {
            measuredFrameRate = plausibleFrameRate(frameDelta / timeDelta);
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

  const frameRate = measuredFrameRate ?? reportedFrameRate;
  if (frameRate !== null) summary.fps = frameRate;
  const transport = selectedIceTransport(report);
  if (transport !== null) summary.transport = transport;
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
  transport: IceTransport | null;
  // Estimated playout latency: jitter-buffer delay + half the candidate RTT +
  // per-frame processing delay. Buffer health remains HLS-specific.
  liveLatencySeconds: number | null;
  timestamp: number;
  videoBytes: number;
  audioBytes: number;
}

interface WebRTCBytesSample {
  timestamp: number;
  videoBytes: number;
  audioBytes: number;
  framesDecoded: number | null;
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
  const transport = selectedIceTransport(report);
  const framesDecoded = typeof video.framesDecoded === 'number' ? video.framesDecoded : null;
  const packetsLost = typeof video.packetsLost === 'number' ? Math.max(0, video.packetsLost) : null;
  const packetsReceived =
    typeof video.packetsReceived === 'number' ? Math.max(0, video.packetsReceived) : null;

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

  // Measure decoded frames over a window of at least one second first; the
  // receiver-reported framesPerSecond is a fallback because it can lag or
  // report values that disagree with the actually decoded frames.
  let framesPerSecond: number | null = null;
  if (previous && framesDecoded !== null && previous.framesDecoded !== null) {
    const frameDelta = framesDecoded - previous.framesDecoded;
    const seconds = (timestamp - previous.timestamp) / 1000;
    if (frameDelta > 0 && seconds >= 1) {
      framesPerSecond = plausibleFrameRate(frameDelta / seconds);
    }
  }
  if (framesPerSecond === null) {
    framesPerSecond = plausibleFrameRate(video.framesPerSecond);
  }

  const jitterBufferEmitted = Number(video.jitterBufferEmittedCount) || 0;
  const jitterBufferDelay = Number(video.jitterBufferDelay) || 0;
  const jitterBufferSeconds =
    jitterBufferEmitted > 0 ? jitterBufferDelay / jitterBufferEmitted : null;
  const decodedFrames = Number(video.framesDecoded) || 0;
  const processingDelay = Number(video.totalProcessingDelay) || 0;
  const processingSeconds = decodedFrames > 0 ? processingDelay / decodedFrames : 0;
  const estimatedPlayout = Number(video.estimatedPlayoutTimestamp);
  let liveLatencySeconds: number | null = null;
  if (Number.isFinite(estimatedPlayout) && estimatedPlayout > 0) {
    const delta = (timestamp - estimatedPlayout) / 1000;
    if (delta >= 0 && delta <= 30) liveLatencySeconds = delta;
  }
  if (liveLatencySeconds === null && jitterBufferSeconds !== null) {
    liveLatencySeconds = jitterBufferSeconds + processingSeconds + (candidateRtt || 0) / 2000;
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
      packetsLost,
      packetsReceived,
      frameWidth: typeof video.frameWidth === 'number' ? video.frameWidth : null,
      frameHeight: typeof video.frameHeight === 'number' ? video.frameHeight : null,
      framesPerSecond,
      framesDropped: typeof video.framesDropped === 'number' ? video.framesDropped : null,
      framesDecoded,
      videoCodec: simplifyCodec(codecs[videoCodecId]),
      audioCodec: simplifyCodec(codecs[audioCodecId]),
      transport,
      liveLatencySeconds,
      timestamp,
      videoBytes,
      audioBytes,
    },
    bytes: { timestamp, videoBytes, audioBytes, framesDecoded },
  };
}

// summarizeSDP returns a compact description of the negotiated media for the
// Stats panel: H.264 profile-level-id, audio codec and candidate count.
export function summarizeSDP(sdp: string | null | undefined): string {
  if (!sdp) return '—';
  const profileMatch = sdp.match(/profile-level-id=([0-9a-fA-F]{6})/);
  const codecMatch = sdp.match(/a=rtpmap:\d+ (H264|H265|AV1|VP8|VP9)\//i);
  const audioMatch = sdp.match(/a=rtpmap:\d+ (OPUS|opus|MP4A|G722)\//i);
  const candidates = (sdp.match(/a=candidate:/g) || []).length;
  const parts: string[] = [];
  if (codecMatch) parts.push(codecMatch[1].toUpperCase());
  if (profileMatch) parts.push('profile-level-id=' + profileMatch[1].toLowerCase());
  if (audioMatch) parts.push(audioMatch[1].toUpperCase());
  if (candidates > 0) parts.push('ICE candidates=' + candidates);
  return parts.length > 0 ? parts.join(' / ') : '—';
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

// ---------------------------------------------------------------------------
// Auto WebRTC quality policy.
//
// WebRTC has no ABR ladder: each WHEP session pins one encoded variant. Auto
// therefore starts on the server default (the passthrough/source copy when it
// exists) and only falls back to an encoded variant when the passthrough path
// is losing enough packets that the picture would freeze.
// ---------------------------------------------------------------------------

export interface VideoInboundTotals {
  packetsLost: number;
  packetsReceived: number;
}

export const AUTO_QUALITY_SWITCH_LOSS_PCT = 10;
export const AUTO_QUALITY_SWITCH_SAMPLES = 2;
export const AUTO_QUALITY_SAMPLE_INTERVAL_MS = 3000;

// collectVideoInboundTotals aggregates the inbound video counters used by the
// auto quality policy.
export async function collectVideoInboundTotals(
  pc: RTCPeerConnection,
  getStatsImpl?: () => Promise<RTCStatsReport>,
): Promise<VideoInboundTotals> {
  const report = await (getStatsImpl ? getStatsImpl() : pc.getStats());
  let packetsLost = 0;
  let packetsReceived = 0;
  report.forEach(entry => {
    if (entry.type === 'inbound-rtp' && entry.kind === 'video') {
      packetsLost += Number(entry.packetsLost) || 0;
      packetsReceived += Number(entry.packetsReceived) || 0;
    }
  });
  return { packetsLost, packetsReceived };
}

// packetLossPctFor computes the loss percentage between two cumulative video
// inbound samples. It returns null when the window has no packets or a reset
// makes the delta unusable.
export function packetLossPctFor(
  previous: VideoInboundTotals,
  next: VideoInboundTotals,
): number | null {
  const lost = next.packetsLost - previous.packetsLost;
  const received = next.packetsReceived - previous.packetsReceived;
  if (lost < 0 || received < 0 || lost + received === 0) return null;
  return (lost / (lost + received)) * 100;
}

// chooseEncodedWebRTCVariant returns the first variant that is not a
// passthrough/source copy, preferring an explicit encoded quality. It returns
// null when every advertised variant is passthrough (nothing to fall back to).
export function chooseEncodedWebRTCVariant(
  variants: WebRTCVariantInfo[] | undefined | null,
): WebRTCVariantInfo | null {
  if (!variants || variants.length === 0) return null;
  const encoded = variants.find(variant => !/pass|copy|source/i.test(variant.name || ''));
  return encoded || null;
}
