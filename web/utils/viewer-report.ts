// Player heartbeat. Beyond keeping the viewer count fresh it carries the
// playback details the server cannot measure itself: protocol, the quality the
// player settled on, the latency it is experiencing and the display name of the
// viewer. Everything is optional; a player that reports nothing still counts.
const ENDPOINT = '/api/ping';
// The heartbeat runs at the same cadence the admin viewer table refreshes, so a
// row never shows values older than one refresh cycle.
export const VIEWER_REFRESH_MS = 10000;

const DISPLAY_NAME_KEY = 'displayName';
const ACCESS_TOKEN_KEY = 'accessToken';

export type ViewerReport = {
  protocol: 'hls' | 'webrtc';
  name?: string | null;
  quality?: string | null;
  latencyMs?: number | null;
  resolution?: string | null;
  fps?: number | null;
  bitrateKbps?: number | null;
};

const readStorage = (key: string): string | null => {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
};

const appendNumber = (params: URLSearchParams, key: string, value?: number | null) => {
  if (typeof value === 'number' && Number.isFinite(value)) {
    params.set(key, String(Math.round(value)));
  }
};

export class ViewerReporter {
  private timer: ReturnType<typeof setInterval> | null = null;

  private readonly collect: () => ViewerReport | Promise<ViewerReport>;

  constructor(collect: () => ViewerReport | Promise<ViewerReport>) {
    this.collect = collect;
  }

  start() {
    this.stop();
    this.send();
    this.timer = setInterval(() => this.send(), VIEWER_REFRESH_MS);
  }

  stop() {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  private async send() {
    let report: ViewerReport;
    try {
      report = await this.collect();
    } catch {
      return;
    }

    const params = new URLSearchParams({ protocol: report.protocol });
    const name = report.name ?? readStorage(DISPLAY_NAME_KEY);
    if (name) params.set('name', name);

    // The token lets the server read the current display name of the chat user
    // this player belongs to, so a rename shows up and a chat-only client on the
    // same address is never mistaken for the viewer.
    const token = readStorage(ACCESS_TOKEN_KEY);
    if (token) params.set('token', token);
    if (report.quality) params.set('quality', report.quality);
    if (report.resolution) params.set('resolution', report.resolution);
    appendNumber(params, 'latency', report.latencyMs);
    appendNumber(params, 'fps', report.fps);
    appendNumber(params, 'bitrate', report.bitrateKbps);

    // A failed heartbeat must never surface to the viewer: the next tick retries.
    if (typeof fetch !== 'function') return;
    fetch(`${ENDPOINT}?${params.toString()}`).catch(() => {});
  }
}

// videoResolution reports the decoded size, or null before the first frame.
export const videoResolution = (video: HTMLVideoElement | null): string | null => {
  if (!video || !video.videoWidth || !video.videoHeight) return null;

  return `${video.videoWidth}x${video.videoHeight}`;
};

// webrtcLatencyMs estimates end-to-end latency from the receiver's jitter buffer
// statistics. Returns null until the first statistics sample is available.
export const webrtcLatencyMs = async (pc: RTCPeerConnection | null): Promise<number | null> => {
  if (!pc) return null;

  try {
    const stats = await pc.getStats();
    let latency: number | null = null;
    stats.forEach(report => {
      if (report.type !== 'inbound-rtp' || report.kind !== 'video') return;
      const delay = Number(report.jitterBufferDelay);
      const count = Number(report.jitterBufferEmittedCount);
      if (Number.isFinite(delay) && Number.isFinite(count) && count > 0) {
        latency = (delay / count) * 1000;
      }
    });

    return latency;
  } catch {
    return null;
  }
};
