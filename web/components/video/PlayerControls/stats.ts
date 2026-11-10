// Stats-for-nerds presentation helpers. Kept transport-agnostic so both the
// Video.js/HLS and the WHEP/WebRTC players feed the same panel.

import { StatsSnapshot } from './types';

export interface StatsRow {
  label: string;
  value: string;
}

// The provided player renders unavailable stats as an em dash, never "N/A".
const NOT_AVAILABLE = '—';

export function formatStatsBitrate(kbps: number | null | undefined): string {
  if (kbps === null || kbps === undefined || !Number.isFinite(kbps) || kbps <= 0) {
    return NOT_AVAILABLE;
  }
  if (kbps >= 1000) {
    return (kbps / 1000).toFixed(2) + ' Mbps';
  }
  return Math.round(kbps) + ' kbps';
}

function formatSeconds(seconds: number | null | undefined, digits = 1): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) {
    return NOT_AVAILABLE;
  }
  return seconds.toFixed(digits) + 's';
}

function formatPacketLoss(lost: number | null, received: number | null): string {
  if (lost === null || received === null || received < 0 || lost < 0) {
    return NOT_AVAILABLE;
  }
  const total = lost + Math.max(0, received);
  if (total === 0) return '0.00% (0/0)';
  return ((lost / total) * 100).toFixed(2) + '% (' + lost + '/' + total + ')';
}

function formatRttJitter(rttMs: number | null, jitterMs: number | null): string {
  // HLS cannot measure either value: the reference shows a single em dash,
  // not "— / —". WebRTC always supplies at least the RTT.
  if (rttMs === null && jitterMs === null) return NOT_AVAILABLE;
  const rtt = rttMs === null ? NOT_AVAILABLE : Math.round(rttMs) + ' ms';
  const jitter = jitterMs === null ? NOT_AVAILABLE : jitterMs.toFixed(1) + ' ms';
  return rtt + ' / ' + jitter;
}

// Rows mirror the provided player's Stats for nerds panel.
export function buildStatsRows(stats: StatsSnapshot): StatsRow[] {
  return [
    { label: 'Stream / Video ID', value: stats.streamId || NOT_AVAILABLE },
    { label: 'Viewport / Frames', value: stats.frames || NOT_AVAILABLE },
    {
      label: 'Current / Optimal Res',
      value:
        (stats.resolution || NOT_AVAILABLE) + ' / ' + (stats.optimalResolution || NOT_AVAILABLE),
    },
    { label: 'Volume / Normalized', value: stats.volume },
    { label: 'Codecs', value: stats.codecs },
    {
      label: 'Quality / SDP',
      value: (stats.qualityName || 'Auto') + ' / ' + (stats.sdp || NOT_AVAILABLE),
    },
    { label: 'Connection Speed', value: formatStatsBitrate(stats.connectionSpeedKbps) },
    { label: 'RTT / Jitter', value: formatRttJitter(stats.rttMs, stats.jitterMs) },
    {
      label: 'Packet Loss (video)',
      value: formatPacketLoss(stats.packetsLost, stats.packetsReceived),
    },
    { label: 'Buffer Health', value: formatSeconds(stats.bufferSeconds) },
    { label: 'Live Latency', value: formatSeconds(stats.liveLatencySeconds) },
    { label: 'Live Mode', value: stats.liveMode || NOT_AVAILABLE },
    { label: 'Date', value: stats.date || NOT_AVAILABLE },
  ];
}

export const SPARKLINE_POINTS = 60;

// Append a sample to a sparkline series, keeping the last N samples.
export function pushSparkline(series: number[], value: number | null | undefined): number[] {
  const safe =
    value !== null && value !== undefined && Number.isFinite(value) ? Math.max(0, value) : 0;
  return [...series.slice(-(SPARKLINE_POINTS - 1)), safe];
}

// Build an SVG polyline "points" attribute for a sparkline sized to a viewBox.
export function sparklinePoints(values: number[], width = 200, height = 30): string {
  if (values.length === 0) return '';
  const max = Math.max(...values, 1);
  const step = values.length > 1 ? width / (values.length - 1) : width;
  return values
    .map((value, index) => {
      const x = (values.length > 1 ? index * step : width / 2).toFixed(2);
      const y = (height - (Math.max(0, value) / max) * height).toFixed(2);
      return x + ',' + y;
    })
    .join(' ');
}

export function formatViewportFrames(
  width: number,
  height: number,
  droppedFrames: number | null,
  totalFrames: number | null,
): string {
  const viewport = width > 0 && height > 0 ? width + 'x' + height : NOT_AVAILABLE;
  const dropped = droppedFrames === null ? NOT_AVAILABLE : String(droppedFrames);
  const total = totalFrames === null ? NOT_AVAILABLE : String(totalFrames);
  return viewport + ' / ' + dropped + ' dropped of ' + total;
}

export function formatVolume(
  volume: number | null | undefined,
  muted: boolean,
): {
  volume: string;
  normalizedVolume: string;
} {
  const pct =
    volume === null || volume === undefined || !Number.isFinite(volume)
      ? NOT_AVAILABLE
      : Math.round(Math.max(0, Math.min(1, volume)) * 100) + '%';
  return {
    volume: pct + ' / ' + (muted ? 'Muted' : '100%'),
    normalizedVolume: muted ? 'Muted' : '100%',
  };
}

// --- HLS transport metering (provided-player algorithm) ---------------------
// The provided player measures HLS from the VHS segment loader instead of the
// playlist BANDWIDTH: video bitrate from transferred media bytes / loaded media
// seconds (or decoded-byte deltas when the browser exposes them), connection
// speed from transferred bytes / transfer duration (or the ABR system
// bandwidth), fps from decoded frame deltas with a playlist FRAME-RATE
// fallback, and resolution from the rendered video size plus fps.

export interface HlsMeterInput {
  // Cumulative bytes of media segments downloaded by VHS.
  mediaBytesTransferred?: number | null;
  // Cumulative media seconds covered by those segments.
  mediaSecondsLoaded?: number | null;
  // Cumulative time spent downloading media segments, in milliseconds.
  mediaTransferDurationMs?: number | null;
  // VHS ABR system-bandwidth estimate, in bits per second.
  systemBandwidthBps?: number | null;
  // Bandwidth advertised by the currently enabled playlist representation.
  representationBandwidthBps?: number | null;
  // Decoded media bits per second, when the browser exposes a decoded-byte
  // counter (WebKit). Preferred over the segment-average bitrate.
  decodedBitsPerSecond?: number | null;
  // Ingest audio bitrate in kbps from /api/status (null when unavailable).
  audioBitrateKbps?: number | null;
}

function positiveNumber(value: unknown): number | null {
  const numeric = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(numeric) && numeric > 0 ? numeric : null;
}

// Video bitrate in kbps: decoded-byte rate > segment bytes / loaded seconds >
// enabled playlist BANDWIDTH. Returns null when nothing measurable is known.
export function hlsVideoBitrateKbps(input: HlsMeterInput): number | null {
  const decoded = positiveNumber(input.decodedBitsPerSecond);
  if (decoded !== null) return decoded / 1000;

  const bytes = positiveNumber(input.mediaBytesTransferred);
  const seconds = positiveNumber(input.mediaSecondsLoaded);
  if (bytes !== null && seconds !== null) return (8 * bytes) / seconds / 1000;

  const advertised = positiveNumber(input.representationBandwidthBps);
  return advertised !== null ? advertised / 1000 : null;
}

// Connection speed in kbps: measured segment-download throughput, falling back
// to the ABR system bandwidth, and never below the playing stream's own
// video+audio bitrate (provided-player behavior).
export function hlsConnectionSpeedKbps(
  input: HlsMeterInput,
  videoBitrateKbps: number | null,
): number | null {
  const bytes = positiveNumber(input.mediaBytesTransferred);
  const durationMs = positiveNumber(input.mediaTransferDurationMs);
  const measured = bytes !== null && durationMs !== null ? (8e3 * bytes) / durationMs / 1000 : null;
  const system = positiveNumber(input.systemBandwidthBps);
  const systemKbps = system !== null ? system / 1000 : null;
  const throughput = measured ?? systemKbps;
  const streamKbps = (videoBitrateKbps ?? 0) + (positiveNumber(input.audioBitrateKbps) ?? 0);
  const candidates = [throughput, systemKbps, streamKbps].filter(
    (value): value is number => value !== null && value > 0,
  );
  return candidates.length > 0 ? Math.max(...candidates) : null;
}

// Decoded frames per second from a playbackQuality.totalVideoFrames delta.
export function hlsMeasuredFps(
  framesDelta: number | null | undefined,
  seconds: number | null | undefined,
): number | null {
  const frames = positiveNumber(framesDelta);
  const elapsed = positiveNumber(seconds);
  if (frames === null || elapsed === null) return null;
  const fps = Math.round(frames / elapsed);
  return fps >= 1 && fps <= 240 ? fps : null;
}

// Playlist FRAME-RATE attribute (or a quality name such as "1.2Mbps@24fps").
export function hlsPlaylistFps(
  attributes?: Record<string, unknown> | null,
  qualityName?: string | null,
): number | null {
  const raw =
    attributes?.['FRAME-RATE'] ?? attributes?.['FRAME_RATE'] ?? attributes?.['frame-rate'];
  const fromAttributes = positiveNumber(raw);
  if (fromAttributes !== null) return Math.round(fromAttributes);
  const match = typeof qualityName === 'string' ? qualityName.match(/@(\d+(?:\.\d+)?)fps/i) : null;
  const fromName = match ? positiveNumber(match[1]) : null;
  return fromName !== null ? Math.round(fromName) : null;
}

// "1280x720@24fps" when width/height are known; em dash otherwise.
export function hlsResolution(
  width: number | null | undefined,
  height: number | null | undefined,
  fps: number | null,
): string {
  const w = positiveNumber(width);
  const h = positiveNumber(height);
  if (w === null || h === null) return NOT_AVAILABLE;
  const suffix = fps !== null && fps > 0 ? '@' + Math.round(fps) + 'fps' : '';
  return Math.round(w) + 'x' + Math.round(h) + suffix;
}

// Reference "Live Mode" label for the HLS engine ("HLS (hls)").
export function hlsLiveMode(videoPipeline?: string | null): string {
  const pipeline =
    typeof videoPipeline === 'string' && videoPipeline.trim() ? videoPipeline.trim() : 'hls';
  return 'HLS (' + pipeline + ')';
}

// hlsForwardBufferSeconds is the media buffered ahead of the playhead. It only
// counts the range that actually contains the playhead, so a stale range left
// behind after a seek cannot inflate the number.
export function hlsForwardBufferSeconds(
  ranges: { start: number; end: number }[] | null | undefined,
  currentTime: number,
): number | null {
  if (!ranges || ranges.length === 0) return null;
  const playhead = Number.isFinite(currentTime) ? currentTime : 0;
  for (const range of ranges) {
    if (playhead >= range.start - 0.001 && playhead <= range.end) {
      return Math.max(0, range.end - playhead);
    }
  }

  return 0;
}

export interface HlsPlaylistSegment {
  start?: number;
  end?: number;
  duration?: number;
  dateTimeObject?: Date | null;
}

// hlsLiveLatencySeconds measures the real delay between the playhead and the
// wall clock using the playlist's EXT-X-PROGRAM-DATE-TIME stamps. video.js'
// liveTracker only reports the distance to the seekable edge, which excludes the
// segments the packager has not published yet, so it under-reports latency by
// roughly one target duration plus the encoder delay.
export function hlsLiveLatencySeconds(input: {
  nowMs: number;
  currentTime: number;
  segments?: HlsPlaylistSegment[] | null;
  liveEdgeLatencySeconds?: number | null;
}): number | null {
  const playhead = Number.isFinite(input.currentTime) ? input.currentTime : 0;
  const segments = input.segments || [];
  let anchor: HlsPlaylistSegment | null = null;
  for (const segment of segments) {
    const start = Number(segment.start);
    if (!Number.isFinite(start) || start > playhead + 0.001) continue;
    if (!(segment.dateTimeObject instanceof Date)) continue;
    if (anchor === null || start >= Number(anchor.start)) anchor = segment;
  }
  if (anchor && anchor.dateTimeObject instanceof Date) {
    const mediaWallMs = anchor.dateTimeObject.getTime() + (playhead - Number(anchor.start)) * 1000;
    const latency = (input.nowMs - mediaWallMs) / 1000;
    if (Number.isFinite(latency) && latency >= 0 && latency <= 120) return latency;
  }
  const fallback = input.liveEdgeLatencySeconds;
  if (fallback !== null && fallback !== undefined && Number.isFinite(fallback) && fallback >= 0) {
    return fallback;
  }

  return null;
}

// hlsPlaylistSegments extracts the media playlist segments from video.js VHS so
// the stats panel can read the program-date-time of the current playhead.
export function hlsPlaylistSegments(vhs: unknown): HlsPlaylistSegment[] | null {
  const tech = vhs as
    | {
        playlistController_?: { media?: () => { segments?: HlsPlaylistSegment[] } | null };
        masterPlaylistController_?: { media?: () => { segments?: HlsPlaylistSegment[] } | null };
      }
    | null
    | undefined;
  if (!tech) return null;
  for (const controller of [tech.playlistController_, tech.masterPlaylistController_]) {
    try {
      const media = typeof controller?.media === 'function' ? controller.media() : null;
      if (media && Array.isArray(media.segments) && media.segments.length > 0) {
        return media.segments;
      }
    } catch {
      // Playlist controller may not be ready yet.
    }
  }

  return null;
}
