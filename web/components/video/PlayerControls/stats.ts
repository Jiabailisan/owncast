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
  const rtt = rttMs === null ? NOT_AVAILABLE : Math.round(rttMs) + ' ms';
  const jitter = jitterMs === null ? NOT_AVAILABLE : jitterMs.toFixed(1) + ' ms';
  return rtt + ' / ' + jitter;
}

// Rows mirror the provided player's Stats for nerds panel.
export function buildStatsRows(stats: StatsSnapshot): StatsRow[] {
  return [
    { label: 'Stream / Video ID', value: stats.streamId || NOT_AVAILABLE },
    { label: 'Viewport / Frames', value: stats.frames || NOT_AVAILABLE },
    { label: 'Current / Optimal Res', value: stats.resolution },
    { label: 'Volume / Normalized', value: stats.volume },
    { label: 'Codecs', value: stats.codecs },
    { label: 'Video Bitrate', value: formatStatsBitrate(stats.videoBitrateKbps) },
    {
      label: 'Audio Bitrate',
      value:
        stats.audioBitrateKbps === null
          ? NOT_AVAILABLE
          : formatStatsBitrate(stats.audioBitrateKbps) + (stats.isWebRTC ? '' : ' (ingest)'),
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
