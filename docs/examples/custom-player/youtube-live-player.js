// YouTube Live-style custom player module for Owncast (task-24).
//
// This file is served verbatim at /custom-player/player.js when an operator sets
// Appearance -> Custom player path to its absolute path, so it MUST be a single
// self-contained ES module: no relative imports, no bundler-only syntax, no
// inline scripts/eval (the viewer CSP forbids those).
//
// Contract: docs/examples/custom-player/README.md. Host-provided libraries are
// optional: ctx.libs.videojs plays HLS in Chromium through video.js VHS;
// ctx.libs.hls (hls.js constructor) is used when a host provides it; without
// either, HLS falls back to native <video src> (Safari/iOS) and reports N/A.
//
// Tested in Chromium (WHEP + video.js VHS/HLS). WebRTC uses the public WHEP
// endpoint; HLS uses ctx.hlsUrl. Pure helpers below are exported so unit tests
// can cover transport selection, persistence and stats formatting without a
// browser.

export const SOURCE_AUTO = 'auto';
export const SOURCE_WEBRTC = 'webrtc';
export const SOURCE_HLS = 'hls';
export const SOURCE_CHOICES = [SOURCE_AUTO, SOURCE_WEBRTC, SOURCE_HLS];

export const STORAGE_PREFIX = 'owncast-youtube-player';
export const STORAGE_KEYS = {
  source: STORAGE_PREFIX + ':source',
  volume: STORAGE_PREFIX + ':volume',
  muted: STORAGE_PREFIX + ':muted',
  hlsQuality: STORAGE_PREFIX + ':hls-quality',
  webrtcVariant: STORAGE_PREFIX + ':webrtc-variant',
  speed: STORAGE_PREFIX + ':speed',
  stats: STORAGE_PREFIX + ':stats',
};

export const AUTO_QUALITY = 'auto';
export const SPEED_CHOICES = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];
export const DEFAULT_PREFERENCES = {
  source: SOURCE_AUTO,
  volume: 1,
  muted: false,
  hlsQuality: AUTO_QUALITY,
  webrtcVariant: AUTO_QUALITY,
  speed: 1,
  stats: false,
};

const SERVER_STATUS_POLL_MS = 5000;
const STATS_POLL_MS = 1000;
const CONTROLS_HIDE_MS = 2600;
const LIVE_EDGE_TOLERANCE_SECONDS = 3;
// A large natural drift (e.g. after a long stall) also turns on go-to-live.
const LIVE_EDGE_DRIFT_SECONDS = 15;
const WHEP_REQUEST_TIMEOUT_MS = 15000;

// ---- Pure helpers (unit-tested; no DOM access) ------------------------------

export function normalizeSourceChoice(value) {
  return SOURCE_CHOICES.indexOf(value) === -1 ? SOURCE_AUTO : value;
}

export function clampNumber(value, min, max) {
  const number = Number(value);
  if (!isFinite(number)) return min;
  if (number < min) return min;
  if (number > max) return max;
  return number;
}

export function clampVolume(value) {
  return clampNumber(value, 0, 1);
}

export function isWebRTCAvailable(context) {
  return Boolean(context && context.whepUrl && typeof RTCPeerConnection !== 'undefined');
}

export function isWebRTCSupported() {
  return typeof RTCPeerConnection !== 'undefined';
}

// Returns the transport that will actually be used for a saved choice.
export function resolveTransport(source, availability) {
  const choice = normalizeSourceChoice(source);
  const hasWebRTC = Boolean(availability && availability.webrtc);
  if (choice === SOURCE_WEBRTC) return hasWebRTC ? SOURCE_WEBRTC : SOURCE_HLS;
  if (choice === SOURCE_HLS) return SOURCE_HLS;
  return hasWebRTC ? SOURCE_WEBRTC : SOURCE_HLS;
}

export function nextSourceChoice(current) {
  const index = SOURCE_CHOICES.indexOf(normalizeSourceChoice(current));
  return SOURCE_CHOICES[(index + 1) % SOURCE_CHOICES.length];
}

export function formatTime(totalSeconds) {
  const seconds = Number(totalSeconds);
  if (!isFinite(seconds) || seconds < 0) return '0:00';
  const whole = Math.floor(seconds);
  const hours = Math.floor(whole / 3600);
  const minutes = Math.floor((whole % 3600) / 60);
  const secs = whole % 60;
  if (hours > 0) {
    return hours + ':' + String(minutes).padStart(2, '0') + ':' + String(secs).padStart(2, '0');
  }
  return minutes + ':' + String(secs).padStart(2, '0');
}

export function formatStatValue(value) {
  if (value === null || value === undefined || value === '') return 'N/A';
  if (typeof value === 'number' && !isFinite(value)) return 'N/A';
  return String(value);
}

export function formatKbps(kbps) {
  const number = Number(kbps);
  if (!isFinite(number) || number <= 0) return 'N/A';
  return Math.round(number) + ' kbps';
}

export function formatSeconds(seconds, digits) {
  const number = Number(seconds);
  if (!isFinite(number)) return 'N/A';
  return number.toFixed(digits === undefined ? 1 : digits) + ' s';
}

export function formatMilliseconds(milliseconds) {
  const number = Number(milliseconds);
  if (!isFinite(number)) return 'N/A';
  return number.toFixed(1) + ' ms';
}

export function levelLabel(level, index) {
  if (level && level.height) return level.height + 'p';
  if (level && level.name) return String(level.name);
  if (level && level.bitrate) return Math.round(level.bitrate / 1000) + ' kbps';
  if (index === undefined || index === null) return 'Auto';
  return 'Level ' + index;
}

export function qualityLabel(id, levels, fallbackPrefix) {
  if (id === AUTO_QUALITY) return 'Auto';
  const index = Number(id);
  if (isNaN(index)) return fallbackPrefix ? fallbackPrefix : 'Auto';
  if (levels && levels[index]) return levelLabel(levels[index], index);
  return 'Quality ' + index;
}

export function normalizeQualityPreference(value, validCount) {
  if (value === AUTO_QUALITY || value === undefined || value === null || value === '') return AUTO_QUALITY;
  const index = Number(value);
  if (!isFinite(index) || index < 0 || (validCount > 0 && index >= validCount)) return AUTO_QUALITY;
  return index;
}

// Appends the WebRTC variant query the WHEP endpoint understands (?variant=N).
export function pickWhepUrl(whepUrl, variant) {
  if (!whepUrl) return '';
  if (variant === AUTO_QUALITY || variant === undefined || variant === null || variant === '') return whepUrl;
  const index = Number(variant);
  if (!isFinite(index) || index < 0) return whepUrl;
  const separator = whepUrl.indexOf('?') === -1 ? '?' : '&';
  return whepUrl + separator + 'variant=' + Math.floor(index);
}

// TimeRanges-like object -> seconds buffered ahead of currentTime.
export function bufferHealthAhead(buffered, currentTime) {
  if (!buffered || typeof buffered.length !== 'number' || buffered.length === 0) return null;
  try {
    const time = Number(currentTime) || 0;
    for (let i = 0; i < buffered.length; i += 1) {
      if (buffered.start(i) - 0.1 <= time && time <= buffered.end(i)) {
        return Math.max(0, buffered.end(i) - time);
      }
    }
    return Math.max(0, buffered.end(buffered.length - 1) - time);
  } catch (error) {
    return null;
  }
}

export function liveWindow(buffered) {
  if (!buffered || typeof buffered.length !== 'number' || buffered.length === 0) return null;
  try {
    return {
      start: buffered.start(0),
      end: buffered.end(buffered.length - 1),
    };
  } catch (error) {
    return null;
  }
}

// Bitrate between two samples of a monotonically increasing byte counter.
export function bitrateKbpsFromSamples(previous, current) {
  if (!previous || !current) return null;
  const bytes = Number(current.bytes) - Number(previous.bytes);
  const seconds = (Number(current.time) - Number(previous.time)) / 1000;
  if (!isFinite(bytes) || !isFinite(seconds) || bytes < 0 || seconds <= 0) return null;
  return (bytes * 8) / seconds / 1000;
}

export function parseServerStatus(payload) {
  if (!payload || typeof payload !== 'object') return { online: null, viewerCount: null, inputProtocol: null };
  return {
    online: typeof payload.online === 'boolean' ? payload.online : null,
    viewerCount: typeof payload.viewerCount === 'number' ? payload.viewerCount : null,
    inputProtocol: typeof payload.inputProtocol === 'string' ? payload.inputProtocol : null,
  };
}

export function loadPreferences(storage) {
  const preferences = Object.assign({}, DEFAULT_PREFERENCES);
  if (!storage) return preferences;
  try {
    const source = storage.getItem(STORAGE_KEYS.source);
    if (source !== null) preferences.source = normalizeSourceChoice(source);
    const volume = storage.getItem(STORAGE_KEYS.volume);
    if (volume !== null) preferences.volume = clampVolume(volume);
    const muted = storage.getItem(STORAGE_KEYS.muted);
    if (muted !== null) preferences.muted = muted === 'true';
    const hlsQuality = storage.getItem(STORAGE_KEYS.hlsQuality);
    if (hlsQuality !== null) preferences.hlsQuality = normalizeQualityPreference(hlsQuality, 0);
    const webrtcVariant = storage.getItem(STORAGE_KEYS.webrtcVariant);
    if (webrtcVariant !== null) preferences.webrtcVariant = normalizeQualityPreference(webrtcVariant, 0);
    const speed = storage.getItem(STORAGE_KEYS.speed);
    if (speed !== null) preferences.speed = clampNumber(speed, 0.5, 2);
    const stats = storage.getItem(STORAGE_KEYS.stats);
    if (stats !== null) preferences.stats = stats === 'true';
  } catch (error) {
    // Storage can be unavailable (private mode, quota); defaults are fine.
  }
  return preferences;
}

export function savePreferences(storage, preferences) {
  if (!storage || !preferences) return;
  try {
    storage.setItem(STORAGE_KEYS.source, normalizeSourceChoice(preferences.source));
    storage.setItem(STORAGE_KEYS.volume, String(clampVolume(preferences.volume)));
    storage.setItem(STORAGE_KEYS.muted, preferences.muted ? 'true' : 'false');
    storage.setItem(STORAGE_KEYS.hlsQuality, String(preferences.hlsQuality));
    storage.setItem(STORAGE_KEYS.webrtcVariant, String(preferences.webrtcVariant));
    storage.setItem(STORAGE_KEYS.speed, String(clampNumber(preferences.speed, 0.5, 2)));
    storage.setItem(STORAGE_KEYS.stats, preferences.stats ? 'true' : 'false');
  } catch (error) {
    // Best-effort persistence.
  }
}

// Stats for nerds rows (YouTube layout: label left, value right, optional bar).
// Pure so the labels/N-A handling are unit-tested. Adapts YouTube's reference
// rows to the values an Owncast viewer can actually observe.
export function getStatsRows(stats, serverStatus, ui) {
  const s = stats || {};
  const status = serverStatus || {};
  const extras = ui || {};
  const clamp01 = (value) => clampNumber(value, 0, 1);
  const viewportW = extras.viewportWidth || s.width;
  const viewportH = extras.viewportHeight || s.height;
  const fpsSuffix = s.fps ? '@' + Math.round(s.fps) : '';
  const currentRes = s.width && s.height ? s.width + 'x' + s.height + fpsSuffix : 'N/A';
  const optimalRes = s.optimalWidth && s.optimalHeight
    ? s.optimalWidth + 'x' + s.optimalHeight
    : s.width && s.height ? s.width + 'x' + s.height : 'N/A';
  const frames = s.droppedFrames === null || s.droppedFrames === undefined
    ? 'N/A'
    : s.droppedFrames + ' dropped of ' + (s.totalFrames === null || s.totalFrames === undefined ? 'N/A' : s.totalFrames);
  const codecs = [s.videoCodec, s.audioCodec].filter(Boolean).join(' / ') || 'N/A';
  const bitrate = Number(s.bitrateKbps);
  const speedBar = isFinite(bitrate) && bitrate > 0 ? clamp01(bitrate / 20000) : 0;
  const networkKB = typeof s.bytesReceived === 'number' ? (s.bytesReceived / 1024).toFixed(2) + ' KB' : 'N/A';
  const buffer = s.bufferAheadSeconds;
  const bufferBar = typeof buffer === 'number' ? clamp01(buffer / 30) : 0;
  const latency = s.liveLatencySeconds;
  const latencyBar = typeof latency === 'number' ? clamp01(latency / 30) : 0;
  const volume = extras.volumePct === undefined || extras.volumePct === null ? 'N/A' : String(extras.volumePct);
  const normalized = extras.normalizedPct === undefined || extras.normalizedPct === null ? volume : String(extras.normalizedPct);
  const ingest = (status.inputProtocol ? String(status.inputProtocol).toUpperCase() : 'N/A')
    + ' / viewers ' + (status.viewerCount === null || status.viewerCount === undefined ? 'N/A' : String(status.viewerCount))
    + ' / ingest bitrate N/A (admin)';
  return [
    { label: 'Video ID / sCPN', value: s.streamId ? String(s.streamId) : 'N/A' },
    { label: 'Viewport / Frames', value: (viewportW && viewportH ? viewportW + 'x' + viewportH : 'N/A') + ' / ' + frames },
    { label: 'Current / Optimal Res', value: currentRes + ' / ' + optimalRes },
    { label: 'Volume / Normalized', value: volume + '%/' + normalized + '%' },
    { label: 'Codecs', value: codecs },
    { label: 'Connection Speed', value: isFinite(bitrate) && bitrate > 0 ? Math.round(bitrate) + ' Kbps' : 'N/A', bar: speedBar },
    { label: 'Network Activity', value: networkKB, bar: speedBar },
    { label: 'Buffer Health', value: typeof buffer === 'number' ? buffer.toFixed(2) + ' s' : 'N/A', bar: bufferBar },
    { label: 'Live Latency', value: typeof latency === 'number' ? latency.toFixed(2) + 's' : 'N/A', bar: latencyBar },
    { label: 'Live Mode', value: s.liveMode ? String(s.liveMode) : 'N/A' },
    { label: 'Server / Ingest', value: ingest },
    { label: 'Mystery Text', value: s.mysteryText ? String(s.mysteryText) : 'N/A' },
    { label: 'Date', value: extras.date ? String(extras.date) : new Date().toString() },
  ];
}

// ---- Injected stylesheet ----------------------------------------------------

const CSS = [
  '.oyt-root { position: relative; width: 100%; aspect-ratio: 16 / 9; background: #000; color: #fff; font-family: Roboto, Arial, Helvetica, sans-serif; overflow: hidden; user-select: none; -webkit-tap-highlight-color: transparent; }',
  '.oyt-root.oyt-fullscreen { aspect-ratio: auto; width: 100vw; height: 100vh; border-radius: 0; }',
  '.oyt-root * { box-sizing: border-box; }',
  // Force the rendered video box to the player box for every transport. In',
  // HLS mode video.js sets intrinsic inline pixel sizes on .vjs-tech; without',
  // !important the video overflows and the picture is cropped/only visible in',
  // fullscreen (field report). object-fit: contain keeps a different aspect',
  // ratio stream fully visible instead of cropping it.',
  '.oyt-root video, .oyt-root .vjs-tech { position: absolute !important; inset: 0 !important; width: 100% !important; height: 100% !important; max-width: none !important; max-height: none !important; object-fit: contain !important; background: #000; }',
  '.oyt-root .video-js { position: absolute !important; inset: 0 !important; width: 100% !important; height: 100% !important; background: #000; }',
  '.oyt-media { position: absolute; inset: 0; z-index: 1; background: #000; overflow: hidden; }',
  '.oyt-watermark { position: absolute; top: 10px; left: 10px; z-index: 3; display: inline-flex; align-items: center; gap: 4px; height: 20px; padding: 0 6px; border-radius: 2px; background: rgba(204,0,0,0.88); color: #fff; font-size: 11px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; pointer-events: none; }',
  '.oyt-watermark[hidden] { display: none; }',
  '.oyt-watermark .oyt-dot { width: 6px; height: 6px; border-radius: 50%; background: #fff; }',
  '.oyt-buffer { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; background: rgba(0,0,0,0.12); pointer-events: none; z-index: 3; }',
  '.oyt-buffer[hidden] { display: none; }',
  '.oyt-spinner { width: 46px; height: 46px; border-radius: 50%; border: 3px solid rgba(255,255,255,0.22); border-top-color: #fff; animation: oyt-spin 0.9s linear infinite; }',
  '@keyframes oyt-spin { to { transform: rotate(360deg); } }',
  '.oyt-center { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; z-index: 4; background: rgba(0,0,0,0.08); cursor: pointer; }',
  '.oyt-center[hidden] { display: none; }',
  '.oyt-bigplay { width: 76px; height: 54px; border-radius: 10px; border: 0; background: rgba(20,20,20,0.72); color: #fff; display: flex; align-items: center; justify-content: center; cursor: pointer; transition: background 0.15s ease, transform 0.15s ease; }',
  '.oyt-bigplay:hover { background: #ff0000; transform: scale(1.04); }',
  '.oyt-bigplay svg { width: 30px; height: 30px; fill: currentColor; }',
  '.oyt-controls { position: absolute; left: 0; right: 0; bottom: 0; height: 56px; padding: 0 12px; background: linear-gradient(to top, rgba(0,0,0,0.82) 0%, rgba(0,0,0,0.45) 60%, rgba(0,0,0,0) 100%); z-index: 5; opacity: 1; transition: opacity 0.18s ease; }',
  '.oyt-controls.oyt-hidden { opacity: 0; pointer-events: none; }',
  '.oyt-progress { position: absolute; top: -3px; left: 0; right: 0; height: 3px; background: rgba(255,255,255,0.22); cursor: pointer; transition: height 0.1s ease; }',
  '.oyt-root:hover .oyt-progress { height: 4px; }',
  '.oyt-progress.oyt-progress-live { cursor: default; }',
  '.oyt-progress-buffer { position: absolute; left: 0; top: 0; bottom: 0; width: 0%; background: rgba(255,255,255,0.4); }',
  '.oyt-progress-played { position: absolute; left: 0; top: 0; bottom: 0; width: 0%; background: #ff0000; }',
  '.oyt-progress-thumb { position: absolute; top: 50%; width: 12px; height: 12px; margin-left: -6px; margin-top: -6px; border-radius: 50%; background: #ff0000; transform: scale(0); transition: transform 0.1s ease; }',
  '.oyt-root:hover .oyt-progress-thumb { transform: scale(1); }',
  '.oyt-progress.oyt-progress-live .oyt-progress-thumb { display: none; }',
  '.oyt-bar { display: flex; align-items: center; height: 56px; gap: 0; color: #eee; }',
  '.oyt-btn { width: 40px; height: 40px; padding: 0; border: 0; border-radius: 0; background: transparent; color: #eee; display: flex; align-items: center; justify-content: center; cursor: pointer; opacity: 1; }',
  '.oyt-btn:hover { color: #fff; background: transparent; transform: scale(1.06); }',
  '.oyt-btn:focus-visible { outline: 2px solid #3ea6ff; outline-offset: 1px; }',
  '.oyt-btn:disabled { opacity: 0.35; cursor: default; }',
  '.oyt-btn svg { width: 24px; height: 24px; fill: currentColor; }',
  '.oyt-spacer { flex: 1 1 auto; }',
  '.oyt-volume { display: flex; align-items: center; }',
  '.oyt-volume-slider { width: 0; opacity: 0; transition: width 0.15s ease, opacity 0.15s ease; accent-color: #ff0000; cursor: pointer; }',
  '.oyt-volume:hover .oyt-volume-slider, .oyt-volume:focus-within .oyt-volume-slider { width: 52px; opacity: 1; margin: 0 6px; }',
  // Single integrated live indicator (no separate go-to-live button): red dot +',
  // "Live"; at the live edge it is static, behind live it becomes the red',
  // jump-to-live control with the YouTube aria/tooltip text.',
  '.oyt-live-pill { display: inline-flex; align-items: center; gap: 6px; height: 24px; margin-left: 10px; padding: 0 4px; border: 0; border-radius: 2px; background: transparent; color: #eee; font-size: 14px; font-family: inherit; cursor: default; }',
  '.oyt-live-pill .oyt-dot { width: 8px; height: 8px; border-radius: 50%; background: #ff0000; }',
  '.oyt-live-pill.oyt-behind { padding: 0 8px; background: #ff0000; color: #fff; cursor: pointer; }',
  '.oyt-live-pill.oyt-behind .oyt-dot { background: #fff; }',
  '.oyt-live-pill.oyt-behind:hover { background: #d90000; }',
  '.oyt-live-pill:disabled { opacity: 1; }',
  '.oyt-settings { position: absolute; right: 12px; bottom: 60px; width: 302px; padding: 8px 0; background: #212121; color: #eee; border-radius: 2px; box-shadow: 0 2px 12px rgba(0,0,0,0.5); z-index: 7; font-size: 13px; }',
  '.oyt-settings[hidden] { display: none; }',
  '.oyt-settings-header { display: flex; align-items: center; height: 40px; padding: 0 4px; }',
  '.oyt-settings-back { width: 36px; height: 36px; border: 0; background: transparent; color: #eee; cursor: pointer; display: flex; align-items: center; justify-content: center; }',
  '.oyt-settings-back svg { width: 24px; height: 24px; fill: currentColor; }',
  '.oyt-settings-title { font-size: 13px; color: #eee; }',
  '.oyt-menu-item { display: flex; align-items: center; width: 100%; min-height: 40px; padding: 0 12px 0 6px; border: 0; background: transparent; color: #eee; font-size: 13px; font-family: inherit; cursor: pointer; text-align: left; gap: 6px; }',
  '.oyt-menu-item:hover { background: rgba(255,255,255,0.1); }',
  '.oyt-menu-icon { width: 36px; height: 36px; display: flex; align-items: center; justify-content: center; flex: 0 0 36px; color: #eee; }',
  '.oyt-menu-icon svg { width: 24px; height: 24px; fill: currentColor; }',
  '.oyt-menu-label { flex: 1 1 auto; }',
  '.oyt-menu-value { color: #aaa; white-space: nowrap; }',
  '.oyt-menu-chevron { color: #aaa; margin-left: 6px; font-size: 15px; line-height: 1; }',
  '.oyt-menu-check { width: 24px; flex: 0 0 24px; color: #eee; font-size: 15px; text-align: right; }',
  '.oyt-menu-item.oyt-menu-disabled { opacity: 0.45; cursor: default; }',
  '.oyt-stats { position: absolute; top: 10px; left: 10px; width: 496px; max-width: calc(100% - 20px); padding: 5px; background: rgba(0,0,0,0.75); color: #fff; font-family: "YouTube Noto", Roboto, Arial, Helvetica, sans-serif; font-size: 11px; line-height: 14.3px; z-index: 6; }',
  '.oyt-stats[hidden] { display: none; }',
  '.oyt-stats-close { position: absolute; top: 1px; right: 5px; border: 0; background: transparent; color: #fff; font-size: 13px; line-height: 1; cursor: pointer; padding: 2px; }',
  '.oyt-sfn-row { display: flex; align-items: center; gap: 4px; min-height: 14px; }',
  '.oyt-sfn-label { flex: 0 0 auto; }',
  '.oyt-sfn-bar { flex: 1 1 auto; height: 11px; min-width: 30px; margin: 0 6px; background: rgba(255,255,255,0.08); position: relative; overflow: hidden; }',
  '.oyt-sfn-bar-fill { position: absolute; left: 0; top: 0; bottom: 0; background: linear-gradient(90deg, #00c853, #ffd600 70%, #2979ff); opacity: 0.9; }',
  '.oyt-sfn-value { margin-left: auto; text-align: right; white-space: nowrap; }',
  '.oyt-toast { position: absolute; left: 50%; bottom: 72px; transform: translateX(-50%); padding: 8px 14px; border-radius: 4px; background: rgba(28,28,28,0.95); color: #fff; font-size: 12px; z-index: 9; }',
  '.oyt-toast[hidden] { display: none; }',
  '.oyt-error { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; background: rgba(0,0,0,0.8); color: #eee; font-size: 14px; z-index: 10; padding: 24px; text-align: center; }',
  '.oyt-error[hidden] { display: none; }',
  '.oyt-error button { border: 1px solid #555; border-radius: 4px; background: rgba(255,255,255,0.1); color: #fff; padding: 6px 14px; cursor: pointer; }',
  '@media (max-width: 640px) { .oyt-btn { width: 36px; } .oyt-live-pill { font-size: 12px; margin-left: 4px; } .oyt-settings { width: 260px; } .oyt-stats { width: calc(100% - 20px); } .oyt-sfn-bar { min-width: 12px; margin: 0 3px; } }',
].join(String.fromCharCode(10));

// ---- Inline SVG icons (no external assets) ----------------------------------

const ICONS = {
  play: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5v14l11-7z"/></svg>',
  pause: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 5h4v14H6zm8 0h4v14h-4z"/></svg>',
  volumeHigh: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 9v6h4l5 4V5L7 9H3zm13.5 3a4.5 4.5 0 0 0-2.5-4.03v8.05A4.5 4.5 0 0 0 16.5 12zM14 3.23v2.06a7 7 0 0 1 0 13.42v2.06a9 9 0 0 0 0-17.54z"/></svg>',
  volumeLow: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 9v6h4l5 4V5L7 9H3zm13.5 3a4.5 4.5 0 0 0-2.5-4.03v8.05A4.5 4.5 0 0 0 16.5 12z"/></svg>',
  muted: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 9v6h4l5 4V5L7 9H3zm13.59 3 2.5-2.5-1.41-1.41-2.5 2.5-2.5-2.5-1.41 1.41 2.5 2.5-2.5 2.5 1.41 1.41 2.5-2.5 2.5 2.5 1.41-1.41-2.5-2.5z"/></svg>',
  gear: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M19.14 12.94a7.1 7.1 0 0 0 0-1.88l2.03-1.58-1.92-3.32-2.39.96a7.3 7.3 0 0 0-1.62-.94L14.87 3h-3.74l-.37 2.18c-.58.24-1.12.56-1.62.94l-2.39-.96-1.92 3.32 2.03 1.58a7.1 7.1 0 0 0 0 1.88l-2.03 1.58 1.92 3.32 2.39-.96c.5.38 1.04.7 1.62.94l.37 2.18h3.74l.37-2.18c.58-.24 1.12-.56 1.62-.94l2.39.96 1.92-3.32-2.03-1.58zM12 15.5A3.5 3.5 0 1 1 12 8.5a3.5 3.5 0 0 1 0 7z"/></svg>',
  pip: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M19 11h-8v6h8v-6zm4 8V5a2 2 0 0 0-2-2H3a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2zm-2 .02H3V5h18v14.02z"/></svg>',
  fullscreen: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 14H5v5h5v-2H7v-3zm-2-4h2V7h3V5H5v5zm12 7h-3v2h5v-5h-2v3zM14 5v2h3v3h2V5h-5z"/></svg>',
  fullscreenExit: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 16h3v3h2v-5H5v2zm3-8H5v2h5V5H8v3zm6 11h2v-3h3v-2h-5v5zm2-11V5h-2v5h5V8h-3z"/></svg>',
  cast: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M1 18v3h3a3 3 0 0 0-3-3zm0-4v2a5 5 0 0 1 5 5h2a7 7 0 0 0-7-7zm0-4v2a9 9 0 0 1 9 9h2A11 11 0 0 0 1 10zm20-7H3a2 2 0 0 0-2 2v3h2V5h18v14h-7v2h7a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2z"/></svg>',
  stats: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 19h4V9H5v10zm6 0h4V5h-4v14zm6 0h4v-7h-4v7z"/></svg>',
  speed: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4a9 9 0 0 0-9 9c0 2.1.72 4.03 1.93 5.56L6.3 17.2A6.96 6.96 0 0 1 5 13a7 7 0 0 1 14 0c0 1.59-.53 3.05-1.42 4.23l1.37 1.37A8.96 8.96 0 0 0 21 13a9 9 0 0 0-9-9zm1 3h-2v6.28a2 2 0 1 0 2 0V7z"/></svg>',
  quality: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 5H3a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2zm-9 10H9.5v-2.5H7V15H4V9h3v2.5h2.5V9H12v6zm3 0h-2V9h2a2 2 0 0 1 2 2v2a2 2 0 0 1-2 2zm6 0h-2V9h2a2 2 0 0 1 2 2v2a2 2 0 0 1-2 2z"/></svg>',
  source: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 3H3a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h6v2H7v2h10v-2h-2v-2h6a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2zm0 14H3V5h18v12z"/></svg>',
  back: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11H7.83l5.59-5.59L12 4l-8 8 8 8 1.41-1.41L7.83 13H20v-2z"/></svg>',
};

// ---- DOM + player implementation -------------------------------------------

function createElement(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = text;
  return node;
}

function makeButton(icon, labelText, className) {
  const button = createElement('button', 'oyt-btn ' + (className || ''));
  button.type = 'button';
  button.setAttribute('aria-label', labelText);
  button.title = labelText;
  button.innerHTML = ICONS[icon] || '';
  return button;
}

function safeStorage() {
  try {
    return window.localStorage;
  } catch (error) {
    return null;
  }
}

function deepValue(object, path) {
  let current = object;
  for (let i = 0; i < path.length; i += 1) {
    if (current === null || current === undefined) return undefined;
    current = current[path[i]];
  }
  return current;
}

export default function mount(root, context) {
  if (!root) throw new Error('mount(el, ctx): missing mount element');
  const ctx = context || {};
  const storage = safeStorage();
  const preferences = loadPreferences(storage);
  const availability = { webrtc: isWebRTCAvailable(ctx) };
  const state = {
    source: preferences.source,
    transport: null,
    transportKind: null,
    pendingTransport: false,
    destroyed: false,
    generation: 0,
    playing: false,
    muted: preferences.muted,
    volume: preferences.volume,
    speed: preferences.speed,
    statsOpen: preferences.stats,
    statsDate: '',
    settingsOpen: false,
    settingsView: 'main',
    behindLive: false,
    userSeeked: false,
    qualityHls: preferences.hlsQuality,
    qualityWebrtc: preferences.webrtcVariant,
    serverStatus: { online: null, viewerCount: null },
    lastStats: null,
    controlsTimer: null,
    statsTimer: null,
    statusTimer: null,
    progressTimer: null,
    toastTimer: null,
    clickTimer: null,
    fullscreen: false,
    autoFallbackUsed: false,
  };

  root.classList.add('oyt-root');
  root.tabIndex = 0;
  root.setAttribute('data-ocp-root', '');
  root.setAttribute('data-transport', 'none');
  root.replaceChildren();

  const style = document.createElement('style');
  style.textContent = CSS;
  root.appendChild(style);

  const media = createElement('div', 'oyt-media');
  media.style.position = 'absolute';
  media.style.inset = '0';
  media.style.zIndex = '1';
  root.appendChild(media);

  const watermark = createElement('div', 'oyt-watermark');
  watermark.appendChild(createElement('span', 'oyt-dot'));
  watermark.appendChild(createElement('span', '', 'LIVE'));
  watermark.hidden = true;
  root.appendChild(watermark);

  const bufferOverlay = createElement('div', 'oyt-buffer');
  bufferOverlay.hidden = true;
  const spinner = createElement('div', 'oyt-spinner');
  bufferOverlay.appendChild(spinner);
  root.appendChild(bufferOverlay);

  const center = createElement('div', 'oyt-center');
  const bigPlay = makeButton('play', 'Play');
  bigPlay.classList.add('oyt-bigplay');
  center.appendChild(bigPlay);
  root.appendChild(center);

  const controls = createElement('div', 'oyt-controls');
  const progress = createElement('div', 'oyt-progress');
  const progressBuffer = createElement('div', 'oyt-progress-buffer');
  const progressPlayed = createElement('div', 'oyt-progress-played');
  const progressThumb = createElement('div', 'oyt-progress-thumb');
  progress.appendChild(progressBuffer);
  progress.appendChild(progressPlayed);
  progress.appendChild(progressThumb);
  controls.appendChild(progress);

  const bar = createElement('div', 'oyt-bar');
  const playButton = makeButton('play', 'Play (k)');
  const volumeWrap = createElement('div', 'oyt-volume');
  const muteButton = makeButton('volumeHigh', 'Mute (m)');
  const volumeSlider = document.createElement('input');
  volumeSlider.className = 'oyt-volume-slider';
  volumeSlider.type = 'range';
  volumeSlider.min = '0';
  volumeSlider.max = '1';
  volumeSlider.step = '0.01';
  volumeSlider.setAttribute('aria-label', 'Volume');
  volumeWrap.appendChild(muteButton);
  volumeWrap.appendChild(volumeSlider);

  // Single integrated live indicator: static "Live" with a red dot at the live
  // edge; behind live it becomes the red jump-to-live control (YouTube badge).
  const livePill = createElement('button', 'oyt-live-pill');
  livePill.type = 'button';
  livePill.appendChild(createElement('span', 'oyt-dot'));
  livePill.appendChild(createElement('span', 'oyt-live-text', 'Live'));
  livePill.disabled = true;

  const spacer = createElement('div', 'oyt-spacer');
  const settingsButton = makeButton('gear', 'Settings');
  const pipButton = makeButton('pip', 'Picture in picture');
  const fullscreenButton = makeButton('fullscreen', 'Fullscreen (f)');

  bar.appendChild(playButton);
  bar.appendChild(volumeWrap);
  bar.appendChild(livePill);
  bar.appendChild(spacer);
  bar.appendChild(settingsButton);
  bar.appendChild(pipButton);
  bar.appendChild(fullscreenButton);
  controls.appendChild(bar);
  root.appendChild(controls);

  const settingsPanel = createElement('div', 'oyt-settings');
  settingsPanel.hidden = true;
  settingsPanel.setAttribute('role', 'menu');
  root.appendChild(settingsPanel);

  const statsPanel = createElement('div', 'oyt-stats');
  statsPanel.hidden = true;
  const statsClose = createElement('button', 'oyt-stats-close', 'x');
  statsClose.type = 'button';
  statsClose.setAttribute('aria-label', 'Close stats');
  statsPanel.appendChild(statsClose);
  const statsRowsHost = createElement('div', 'oyt-sfn');
  statsPanel.appendChild(statsRowsHost);
  root.appendChild(statsPanel);
  statsClose.addEventListener('click', () => toggleStats(false));

  const toast = createElement('div', 'oyt-toast');
  toast.hidden = true;
  root.appendChild(toast);

  const errorOverlay = createElement('div', 'oyt-error');
  errorOverlay.hidden = true;
  const errorText = createElement('div', '', 'Playback error');
  const retryButton = createElement('button', '', 'Retry');
  retryButton.type = 'button';
  errorOverlay.appendChild(errorText);
  errorOverlay.appendChild(retryButton);
  root.appendChild(errorOverlay);

  function showError(message) {
    errorText.textContent = message || 'Playback error';
    errorOverlay.hidden = false;
    center.hidden = true;
    bufferOverlay.hidden = true;
  }

  function clearError() {
    errorOverlay.hidden = true;
  }

  function showToast(message) {
    toast.textContent = message;
    toast.hidden = false;
    if (state.toastTimer) window.clearTimeout(state.toastTimer);
    state.toastTimer = window.setTimeout(() => {
      toast.hidden = true;
      state.toastTimer = null;
    }, 2600);
  }

  function currentVideo() {
    const transport = state.transport;
    return transport && transport.videoElement ? transport.videoElement() : null;
  }

  function persist() {
    savePreferences(storage, {
      source: state.source,
      volume: state.volume,
      muted: state.muted,
      hlsQuality: state.qualityHls,
      webrtcVariant: state.qualityWebrtc,
      speed: state.speed,
      stats: state.statsOpen,
    });
  }

  function updateVolumeUi() {
    const video = currentVideo();
    const muted = state.muted || state.volume === 0;
    muteButton.innerHTML = muted ? ICONS.muted : state.volume < 0.5 ? ICONS.volumeLow : ICONS.volumeHigh;
    muteButton.title = muted ? 'Unmute (m)' : 'Mute (m)';
    muteButton.setAttribute('aria-label', muteButton.title);
    volumeSlider.value = String(state.volume);
    if (video) {
      video.muted = muted;
      video.volume = state.volume;
    }
  }

  function updatePlayUi() {
    const paused = !state.playing;
    playButton.innerHTML = paused ? ICONS.play : ICONS.pause;
    playButton.title = paused ? 'Play (k)' : 'Pause (k)';
    playButton.setAttribute('aria-label', playButton.title);
    bigPlay.hidden = !paused;
    center.hidden = !paused || errorOverlay.hidden === false;
  }

  function isTransportLive() {
    const transport = state.transport;
    if (!transport) return false;
    if (errorOverlay.hidden === false) return false;
    return transport.isLive ? transport.isLive() : true;
  }

  function updateLiveUi() {
    const transport = state.transport;
    const live = isTransportLive();
    livePill.hidden = !live;
    watermark.hidden = !live || state.statsOpen;
    const behind = live && transport && transport.secondsBehindLive ? transport.secondsBehindLive() : 0;
    const behindLive = live && typeof behind === 'number' && behind > LIVE_EDGE_TOLERANCE_SECONDS
      && (state.userSeeked || behind > LIVE_EDGE_DRIFT_SECONDS);
    state.behindLive = behindLive;
    livePill.classList.toggle('oyt-behind', behindLive);
    livePill.disabled = !behindLive;
    if (behindLive) {
      livePill.setAttribute('aria-label', 'Go to live');
      livePill.title = 'Go to live';
    } else {
      livePill.removeAttribute('aria-label');
      livePill.removeAttribute('title');
    }
    if (!transport) {
      progress.classList.add('oyt-progress-live');
      return;
    }
    if (transport.kind === SOURCE_WEBRTC || !transport.seekableWindow) {
      progress.classList.add('oyt-progress-live');
      progressBuffer.style.width = '100%';
      progressPlayed.style.width = '100%';
      progressThumb.style.display = 'none';
    } else {
      progress.classList.remove('oyt-progress-live');
      progressThumb.style.display = '';
    }
  }

  function updateProgressUi() {
    const transport = state.transport;
    if (!transport) {
      updateLiveUi();
      return;
    }
    if (transport.kind === SOURCE_WEBRTC || !transport.seekableWindow) {
      updateLiveUi();
      return;
    }
    const window = transport.seekableWindow();
    const current = transport.currentTime ? transport.currentTime() : 0;
    if (!window || window.end <= window.start) {
      updateLiveUi();
      return;
    }
    const span = Math.max(0.001, window.end - window.start);
    const played = clampNumber((current - window.start) / span, 0, 1);
    const buffered = transport.bufferEnd ? clampNumber((transport.bufferEnd() - window.start) / span, 0, 1) : played;
    progressPlayed.style.width = (played * 100).toFixed(2) + '%';
    progressBuffer.style.width = (Math.max(played, buffered) * 100).toFixed(2) + '%';
    progressThumb.style.left = (played * 100).toFixed(2) + '%';
    updateLiveUi();
  }
  function updateStatsUi() {
    if (!state.statsOpen) return;
    const transport = state.transport;
    const viewportWidth = Math.round(root.getBoundingClientRect().width);
    const viewportHeight = Math.round(root.getBoundingClientRect().height);
    const volumePct = Math.round((state.muted ? 0 : state.volume) * 100);
    const rows = getStatsRows(state.lastStats, state.serverStatus, {
      viewportWidth,
      viewportHeight,
      volumePct,
      normalizedPct: volumePct,
      date: state.statsDate,
    });
    statsRowsHost.replaceChildren();
    rows.forEach((row) => {
      const rowElement = createElement('div', 'oyt-sfn-row');
      rowElement.appendChild(createElement('div', 'oyt-sfn-label', row.label));
      if (row.bar) {
        const bar = createElement('div', 'oyt-sfn-bar');
        const fill = createElement('div', 'oyt-sfn-bar-fill');
        fill.style.width = Math.round(clampNumber(row.bar, 0, 1) * 100) + '%';
        bar.appendChild(fill);
        rowElement.appendChild(bar);
      }
      rowElement.appendChild(createElement('div', 'oyt-sfn-value', row.value));
      statsRowsHost.appendChild(rowElement);
    });
  }
  async function pollServerStatus() {
    try {
      const response = await fetch('/api/status', { headers: { Accept: 'application/json' } });
      if (!response.ok) return;
      state.serverStatus = parseServerStatus(await response.json());
      updateStatsUi();
    } catch (error) {
      // Server status is informational only.
    }
  }

  async function pollStats() {
    if (state.destroyed || !state.statsOpen) return;
    const transport = state.transport;
    if (!transport) return;
    try {
      const stats = await transport.getStats();
      state.lastStats = stats;
      updateStatsUi();
    } catch (error) {
      state.lastStats = null;
      updateStatsUi();
    }
  }

  function startStatsPolling() {
    stopStatsPolling();
    if (!state.statsOpen) return;
    void pollStats();
    void pollServerStatus();
    state.statsTimer = window.setInterval(() => void pollStats(), STATS_POLL_MS);
    state.statusTimer = window.setInterval(() => void pollServerStatus(), SERVER_STATUS_POLL_MS);
  }

  function stopStatsPolling() {
    if (state.statsTimer) window.clearInterval(state.statsTimer);
    if (state.statusTimer) window.clearInterval(state.statusTimer);
    state.statsTimer = null;
    state.statusTimer = null;
  }

  function toggleStats(open) {
    const next = open === undefined ? !state.statsOpen : Boolean(open);
    state.statsOpen = next;
    statsPanel.hidden = !next;
    root.classList.toggle('oyt-stats-open', next);
    if (next) {
      state.statsDate = new Date().toString();
      startStatsPolling();
    } else {
      stopStatsPolling();
    }
    updateLiveUi();
    persist();
    if (state.settingsOpen) renderSettings();
  }

  function closeSettings() {
    state.settingsOpen = false;
    state.settingsView = 'main';
    renderSettings();
  }

  function selectedQualityId() {
    return state.transportKind === SOURCE_WEBRTC ? String(state.qualityWebrtc) : String(state.qualityHls);
  }

  function qualityMenuOptions() {
    const options = [{ id: AUTO_QUALITY, label: 'Auto' }];
    const transport = state.transport;
    if (!transport) return options;
    if (state.transportKind === SOURCE_WEBRTC) {
      (ctx.variants || []).forEach((variant) => {
        options.push({ id: String(variant.index), label: variant.name || ('Quality ' + variant.index) });
      });
      return options;
    }
    const levels = transport.getLevels ? transport.getLevels() : [];
    if (levels.length && options[0].label === 'Auto') {
      options[0] = { id: AUTO_QUALITY, label: 'Auto (' + levelLabel(levels[levels.length - 1], levels.length - 1) + ')' };
    }
    levels.forEach((level, index) => {
      options.push({ id: String(index), label: levelLabel(level, index) });
    });
    return options;
  }

  function qualityDisplayValue() {
    const options = qualityMenuOptions();
    const selected = selectedQualityId();
    const match = options.filter((option) => option.id === selected)[0];
    return match ? match.label : 'Auto';
  }

  function speedDisplayValue() {
    return Number(state.speed) === 1 ? 'Normal' : String(state.speed) + 'x';
  }

  function sourceDisplayValue() {
    if (state.source === SOURCE_AUTO) return 'Auto';
    if (state.source === SOURCE_WEBRTC) return 'WebRTC';
    return 'HLS';
  }

  // One YouTube-style settings row: leading icon, label, right-aligned value,
  // chevron for submenus or a check mark for selected options.
  function menuRow(options) {
    const item = createElement('button', 'oyt-menu-item');
    item.type = 'button';
    if (options.role) item.setAttribute('role', options.role);
    if (options.disabled) {
      item.disabled = true;
      item.classList.add('oyt-menu-disabled');
    }
    const icon = createElement('span', 'oyt-menu-icon');
    icon.innerHTML = options.icon ? ICONS[options.icon] || '' : '';
    item.appendChild(icon);
    item.appendChild(createElement('span', 'oyt-menu-label', options.label));
    if (options.value !== undefined && options.value !== null) {
      item.appendChild(createElement('span', 'oyt-menu-value', String(options.value)));
    }
    if (options.chevron) {
      item.appendChild(createElement('span', 'oyt-menu-chevron', '›'));
    } else if (options.checked) {
      item.appendChild(createElement('span', 'oyt-menu-check', '\u2713'));
    }
    if (options.onClick && !options.disabled) item.addEventListener('click', options.onClick);
    return item;
  }

  function renderSubmenuHeader(title) {
    const header = createElement('div', 'oyt-settings-header');
    const back = createElement('button', 'oyt-settings-back');
    back.type = 'button';
    back.setAttribute('aria-label', 'Back');
    back.innerHTML = ICONS.back;
    back.addEventListener('click', () => {
      state.settingsView = 'main';
      renderSettings();
    });
    header.appendChild(back);
    header.appendChild(createElement('span', 'oyt-settings-title', title));
    settingsPanel.appendChild(header);
  }

  function renderSubmenu(view) {
    if (view === 'speed') {
      renderSubmenuHeader('Playback speed');
      SPEED_CHOICES.forEach((speed) => {
        settingsPanel.appendChild(menuRow({
          label: speed === 1 ? 'Normal' : String(speed) + 'x',
          checked: Number(state.speed) === speed,
          disabled: state.transportKind !== SOURCE_HLS,
          onClick: () => {
            applySpeed(speed);
            closeSettings();
          },
        }));
      });
      if (state.transportKind !== SOURCE_HLS) {
        settingsPanel.appendChild(menuRow({ label: 'Speed is available for HLS only', disabled: true }));
      }
      return;
    }
    if (view === 'quality') {
      renderSubmenuHeader('Quality');
      const options = qualityMenuOptions();
      const selected = selectedQualityId();
      options.forEach((option) => {
        settingsPanel.appendChild(menuRow({
          label: option.label,
          checked: selected === option.id,
          onClick: () => {
            applyQuality(option.id);
            closeSettings();
          },
        }));
      });
      return;
    }
    if (view === 'source') {
      renderSubmenuHeader('Playback source');
      SOURCE_CHOICES.forEach((choice) => {
        const label = choice === SOURCE_AUTO ? 'Auto' : choice === SOURCE_WEBRTC ? 'WebRTC' : 'HLS';
        const disabled = choice === SOURCE_WEBRTC && !availability.webrtc;
        settingsPanel.appendChild(menuRow({
          label,
          value: choice === SOURCE_WEBRTC && !availability.webrtc ? 'Unavailable' : undefined,
          checked: state.source === choice && !disabled,
          disabled,
          onClick: () => {
            chooseSource(choice);
            closeSettings();
          },
        }));
      });
      return;
    }
    state.settingsView = 'main';
    renderSettings();
  }

  function renderSettings() {
    if (!state.settingsOpen) {
      settingsPanel.hidden = true;
      return;
    }
    settingsPanel.hidden = false;
    settingsPanel.replaceChildren();
    if (state.settingsView !== 'main') {
      renderSubmenu(state.settingsView);
      return;
    }
    settingsPanel.appendChild(menuRow({
      icon: 'speed',
      label: 'Playback speed',
      value: speedDisplayValue(),
      chevron: true,
      onClick: () => {
        state.settingsView = 'speed';
        renderSettings();
      },
    }));
    settingsPanel.appendChild(menuRow({
      icon: 'quality',
      label: 'Quality',
      value: qualityDisplayValue(),
      chevron: true,
      onClick: () => {
        state.settingsView = 'quality';
        renderSettings();
      },
    }));
    settingsPanel.appendChild(menuRow({
      icon: 'source',
      label: 'Playback source',
      value: sourceDisplayValue(),
      chevron: true,
      onClick: () => {
        state.settingsView = 'source';
        renderSettings();
      },
    }));
    settingsPanel.appendChild(menuRow({
      icon: 'stats',
      label: 'Stats for nerds',
      value: state.statsOpen ? 'On' : 'Off',
      onClick: () => {
        toggleStats();
        closeSettings();
      },
    }));
  }
  function chooseSource(choice) {
    const normalized = normalizeSourceChoice(choice);
    if (normalized === SOURCE_WEBRTC && !availability.webrtc) {
      showToast('WebRTC is not available; staying on HLS');
      return;
    }
    state.source = normalized;
    persist();
    state.settingsOpen = false;
    renderSettings();
    void activateTransport(resolveTransport(normalized, availability), true);
  }

  function applyQuality(value) {
    if (state.transportKind === SOURCE_WEBRTC) {
      state.qualityWebrtc = normalizeQualityPreference(value, 0);
      persist();
      void activateTransport(SOURCE_WEBRTC, true);
    } else {
      state.qualityHls = normalizeQualityPreference(value, 0);
      persist();
      if (state.transport && state.transport.setLevel) state.transport.setLevel(state.qualityHls);
    }
    renderSettings();
  }

  function applySpeed(speed) {
    state.speed = clampNumber(speed, 0.5, 2);
    persist();
    if (state.transport && state.transport.setPlaybackRate) {
      state.transport.setPlaybackRate(state.speed);
    }
    renderSettings();
  }

  function togglePlay() {
    const transport = state.transport;
    if (!transport) {
      void activateTransport(resolveTransport(state.source, availability), true);
      return;
    }
    if (state.playing) {
      transport.pause();
    } else {
      void transport.play().catch(() => showToast('Playback was blocked by the browser; click play'));
    }
  }

  function toggleMute() {
    state.muted = !state.muted;
    if (!state.muted && state.volume === 0) state.volume = 0.5;
    updateVolumeUi();
    persist();
  }

  function changeVolume(delta) {
    state.volume = clampVolume(state.volume + delta);
    state.muted = state.volume === 0;
    updateVolumeUi();
    persist();
  }

  function seekBy(delta) {
    const transport = state.transport;
    if (!transport || !transport.seek || !transport.supportsSeek || !transport.supportsSeek()) return;
    const current = transport.currentTime ? transport.currentTime() : 0;
    const target = Math.max(0, current + delta);
    transport.seek(target);
    state.userSeeked = true;
  }

  function goLive() {
    state.userSeeked = false;
    if (state.transport && state.transport.seekToLive) state.transport.seekToLive();
    updateLiveUi();
  }

  function toggleFullscreen() {
    if (document.fullscreenElement === root) {
      void document.exitFullscreen().catch(() => {});
      return;
    }
    if (root.requestFullscreen) {
      void root.requestFullscreen().catch(() => showToast('Fullscreen was blocked by the browser'));
    }
  }

  function onFullscreenChange() {
    state.fullscreen = document.fullscreenElement === root;
    root.classList.toggle('oyt-fullscreen', state.fullscreen);
    fullscreenButton.innerHTML = state.fullscreen ? ICONS.fullscreenExit : ICONS.fullscreen;
    fullscreenButton.title = state.fullscreen ? 'Exit fullscreen (f)' : 'Fullscreen (f)';
    fullscreenButton.setAttribute('aria-label', fullscreenButton.title);
  }

  async function togglePictureInPicture() {
    const video = currentVideo();
    if (!video || !document.pictureInPictureEnabled) {
      showToast('Picture-in-picture is not supported');
      return;
    }
    try {
      if (document.pictureInPictureElement === video) {
        await document.exitPictureInPicture();
      } else {
        await video.requestPictureInPicture();
      }
    } catch (error) {
      showToast('Picture-in-picture was blocked by the browser');
    }
  }

  function showControlsTemporarily() {
    controls.classList.remove('oyt-hidden');
    if (state.controlsTimer) window.clearTimeout(state.controlsTimer);
    state.controlsTimer = window.setTimeout(() => {
      if (state.playing && !state.settingsOpen && !state.statsOpen) controls.classList.add('oyt-hidden');
      state.controlsTimer = null;
    }, CONTROLS_HIDE_MS);
  }

  function onKeyDown(event) {
    const target = event.target;
    if (target && target.tagName && ['INPUT', 'SELECT', 'TEXTAREA', 'BUTTON'].indexOf(target.tagName) !== -1 && event.key !== 'Escape') {
      return;
    }
    const key = event.key;
    if (key === ' ' || key === 'k' || key === 'K') {
      event.preventDefault();
      togglePlay();
    } else if (key === 'm' || key === 'M') {
      event.preventDefault();
      toggleMute();
    } else if (key === 'f' || key === 'F') {
      event.preventDefault();
      toggleFullscreen();
    } else if (key === 'ArrowLeft' || key === 'j' || key === 'J') {
      event.preventDefault();
      seekBy(-5);
    } else if (key === 'ArrowRight' || key === 'l' || key === 'L') {
      event.preventDefault();
      seekBy(5);
    } else if (key === 'ArrowUp') {
      event.preventDefault();
      changeVolume(0.05);
    } else if (key === 'ArrowDown') {
      event.preventDefault();
      changeVolume(-0.05);
    } else if (key === 'Escape') {
      if (state.settingsOpen) {
        state.settingsOpen = false;
        renderSettings();
      }
    }
    showControlsTemporarily();
  }

  function onPointerActivity() {
    showControlsTemporarily();
  }

  function onDocumentPointerDown(event) {
    if (!state.settingsOpen) return;
    if (settingsPanel.contains(event.target) || settingsButton.contains(event.target)) return;
    closeSettings();
  }

  function onRootClick(event) {
    if (event.target === root || event.target === media || event.target === center) {
      if (state.clickTimer) return;
      state.clickTimer = window.setTimeout(() => {
        state.clickTimer = null;
        togglePlay();
      }, 220);
    }
  }

  function onRootDoubleClick() {
    if (state.clickTimer) {
      window.clearTimeout(state.clickTimer);
      state.clickTimer = null;
    }
    toggleFullscreen();
  }

  function onProgressClick(event) {
    const transport = state.transport;
    if (!transport || !transport.seek || !transport.supportsSeek || !transport.supportsSeek()) return;
    const window = transport.seekableWindow ? transport.seekableWindow() : null;
    if (!window) return;
    const rect = progress.getBoundingClientRect();
    if (!rect.width) return;
    const ratio = clampNumber((event.clientX - rect.left) / rect.width, 0, 1);
    transport.seek(window.start + ratio * Math.max(0, window.end - window.start));
    state.userSeeked = true;
  }

  // ---- transport lifecycle --------------------------------------------------

  function callbacksFor(kind) {
    return {
      onPlaying: () => {
        state.playing = true;
        bufferOverlay.hidden = true;
        updatePlayUi();
        updateLiveUi();
        showControlsTemporarily();
      },
      onPause: () => {
        state.playing = false;
        updatePlayUi();
        controls.classList.remove('oyt-hidden');
      },
      onWaiting: () => {
        bufferOverlay.hidden = false;
      },
      onCanPlay: () => {
        bufferOverlay.hidden = true;
      },
      onEnded: () => {
        state.playing = false;
        updatePlayUi();
      },
      onLevelsChanged: () => {
        if (state.settingsOpen) renderSettings();
      },
      onError: (error) => {
        if (state.destroyed) return;
        if (kind === SOURCE_WEBRTC && state.source !== SOURCE_HLS && !state.autoFallbackUsed) {
          state.autoFallbackUsed = true;
          showToast('WebRTC failed; falling back to HLS');
          void activateTransport(SOURCE_HLS, false);
          return;
        }
        showError((error && error.message) || 'Playback error');
      },
    };
  }

  async function activateTransport(kind, userInitiated) {
    if (state.destroyed) return;
    const generation = (state.generation += 1);
    state.pendingTransport = true;
    clearError();
    bufferOverlay.hidden = false;
    bigPlay.hidden = true;
    center.hidden = true;
    stopStatsPolling();
    if (state.transport) {
      const previous = state.transport;
      state.transport = null;
      try {
        await previous.destroy();
      } catch (error) {
        // Teardown is best-effort.
      }
      if (generation !== state.generation) return;
    }
    media.replaceChildren();
    const callbacks = callbacksFor(kind);
    try {
      const transport = await createTransport(kind, ctx, media, callbacks, {
        qualityHls: state.qualityHls,
        qualityWebrtc: state.qualityWebrtc,
      });
      if (generation !== state.generation || state.destroyed) {
        try {
          await transport.destroy();
        } catch (error) {
          // Ignore stale teardown errors.
        }
        return;
      }
      state.transport = transport;
      state.transportKind = kind;
      state.playing = false;
      state.userSeeked = false;
      state.autoFallbackUsed = false;
      root.setAttribute('data-transport', kind);
      applyStoredVolume(transport);
      updatePlayUi();
      updateLiveUi();
      renderSettings();
      if (userInitiated) showToast(kind === SOURCE_WEBRTC ? 'Switching to WebRTC' : 'Switching to HLS');
      maybeAutoplay(transport);
      if (state.statsOpen) startStatsPolling();
      if (typeof ctx.reportPlayback === 'function') {
        try {
          ctx.reportPlayback({ event: 'transport', protocol: kind, timestamp: Date.now() });
        } catch (error) {
          // Reporting is best-effort.
        }
      }
    } catch (error) {
      bufferOverlay.hidden = true;
      bigPlay.hidden = false;
      center.hidden = false;
      if (kind === SOURCE_WEBRTC && state.source !== SOURCE_HLS && !state.autoFallbackUsed) {
        state.autoFallbackUsed = true;
        showToast('WebRTC is unavailable; falling back to HLS');
        state.pendingTransport = false;
        await activateTransport(SOURCE_HLS, false);
        return;
      }
      showError((error && error.message) || 'Unable to start playback');
    } finally {
      if (generation === state.generation) state.pendingTransport = false;
    }
  }

  function applyStoredVolume(transport) {
    const video = transport.videoElement ? transport.videoElement() : null;
    if (!video) return;
    video.volume = state.volume;
    video.muted = state.muted;
    updateVolumeUi();
  }

  function maybeAutoplay(transport) {
    if (ctx.autoplay === 'none') {
      updatePlayUi();
      return;
    }
    if (ctx.autoplay === 'muted') {
      state.muted = true;
      updateVolumeUi();
      persist();
    }
    const attempt = () => {
      transport.play().catch(() => {
        state.playing = false;
        state.muted = true;
        updateVolumeUi();
        persist();
        updatePlayUi();
        showToast('Autoplay was blocked; click play to start');
      });
    };
    if (transport.ready) {
      Promise.resolve(transport.ready).then(attempt).catch(() => {});
    } else {
      attempt();
    }
  }

  // ---- initial wiring -------------------------------------------------------

  root.addEventListener('mousemove', onPointerActivity);
  root.addEventListener('touchstart', onPointerActivity, { passive: true });
  root.addEventListener('pointerdown', () => {
    try {
      root.focus({ preventScroll: true });
    } catch (error) {
      // Focus is best-effort.
    }
  });
  root.addEventListener('keydown', onKeyDown);
  root.addEventListener('click', onRootClick);
  root.addEventListener('dblclick', onRootDoubleClick);
  document.addEventListener('fullscreenchange', onFullscreenChange);

  playButton.addEventListener('click', togglePlay);
  bigPlay.addEventListener('click', (event) => {
    event.stopPropagation();
    togglePlay();
  });
  center.addEventListener('click', (event) => {
    event.stopPropagation();
    togglePlay();
  });
  muteButton.addEventListener('click', toggleMute);
  volumeSlider.addEventListener('input', () => {
    state.volume = clampVolume(volumeSlider.value);
    state.muted = state.volume === 0;
    updateVolumeUi();
    persist();
  });
  volumeSlider.addEventListener('change', persist);
  settingsButton.addEventListener('click', (event) => {
    event.stopPropagation();
    state.settingsOpen = !state.settingsOpen;
    state.settingsView = 'main';
    renderSettings();
    showControlsTemporarily();
  });
  document.addEventListener('pointerdown', onDocumentPointerDown);
  pipButton.addEventListener('click', () => void togglePictureInPicture());
  fullscreenButton.addEventListener('click', toggleFullscreen);
  // The LIVE pill doubles as the jump-to-live control when behind the edge.
  livePill.addEventListener('click', (event) => {
    event.stopPropagation();
    if (state.behindLive) goLive();
  });
  progress.addEventListener('click', onProgressClick);
  retryButton.addEventListener('click', () => {
    clearError();
    void activateTransport(state.transportKind || resolveTransport(state.source, availability), true);
  });

  updateVolumeUi();
  updatePlayUi();
  statsPanel.hidden = !state.statsOpen;
  renderSettings();
  if (state.statsOpen) startStatsPolling();
  state.progressTimer = window.setInterval(updateProgressUi, 300);
  showControlsTemporarily();

  const initialKind = resolveTransport(state.source, availability);
  if (initialKind === SOURCE_HLS && state.source === SOURCE_WEBRTC && !availability.webrtc) {
    showToast('WebRTC is not available; using HLS');
  }
  void activateTransport(initialKind, false);

  return {
    destroy() {
      if (state.destroyed) return;
      state.destroyed = true;
      state.generation += 1;
      stopStatsPolling();
      if (state.progressTimer) window.clearInterval(state.progressTimer);
      if (state.controlsTimer) window.clearTimeout(state.controlsTimer);
      if (state.toastTimer) window.clearTimeout(state.toastTimer);
      if (state.clickTimer) window.clearTimeout(state.clickTimer);
      state.progressTimer = null;
      state.controlsTimer = null;
      state.toastTimer = null;
      state.clickTimer = null;
      root.removeEventListener('mousemove', onPointerActivity);
      root.removeEventListener('touchstart', onPointerActivity);
      root.removeEventListener('keydown', onKeyDown);
      root.removeEventListener('click', onRootClick);
      root.removeEventListener('dblclick', onRootDoubleClick);
      document.removeEventListener('fullscreenchange', onFullscreenChange);
      document.removeEventListener('pointerdown', onDocumentPointerDown);
      if (document.fullscreenElement === root) {
        document.exitFullscreen().catch(() => {});
      }
      const transport = state.transport;
      state.transport = null;
      if (transport) {
        try {
          const result = transport.destroy();
          if (result && typeof result.then === 'function') result.catch(() => {});
        } catch (error) {
          // Teardown is best-effort.
        }
      }
      root.replaceChildren();
      root.classList.remove('oyt-root', 'oyt-fullscreen');
      root.removeAttribute('data-ocp-root');
      root.removeAttribute('data-transport');
      root.removeAttribute('tabindex');
    },
    setVariant(index) {
      const variant = Number(index);
      if (!isFinite(variant) || variant < 0) return;
      state.qualityWebrtc = variant;
      persist();
      if (state.transportKind === SOURCE_WEBRTC) {
        void activateTransport(SOURCE_WEBRTC, true);
      } else if (state.transport && state.transport.setLevel) {
        state.transport.setLevel(variant);
      }
    },
  };
}

// ---- transports -------------------------------------------------------------

async function createTransport(kind, ctx, container, callbacks, qualities) {
  if (kind === SOURCE_WEBRTC) {
    return createWebRTCTransport(ctx, container, callbacks, qualities);
  }
  return createHLSTransport(ctx, container, callbacks, qualities);
}

function makePlainVideo(container) {
  const video = document.createElement('video');
  video.className = 'oyt-video';
  video.setAttribute('playsinline', '');
  video.setAttribute('webkit-playsinline', '');
  video.setAttribute('aria-label', 'Live stream');
  video.preload = 'auto';
  container.appendChild(video);
  return video;
}

function createWebRTCTransport(ctx, container, callbacks, qualities) {
  const video = makePlainVideo(container);
  video.muted = true;
  const pc = new RTCPeerConnection({ iceServers: (ctx.libs && ctx.libs.iceServers) || [] });
  let destroyed = false;
  let sessionUrl = '';
  let location = '';
  let connectionState = 'new';
  let permissionTimer = null;
  const samples = { video: null, audio: null };

  const setMedia = (stream) => {
    if (destroyed) return;
    video.srcObject = stream;
    const playPromise = video.play();
    if (playPromise && typeof playPromise.catch === 'function') playPromise.catch(() => {});
  };

  pc.ontrack = (event) => {
    const stream = event.streams && event.streams[0];
    if (stream) setMedia(stream);
  };
  const stateChange = () => {
    if (destroyed) return;
    connectionState = pc.connectionState || pc.iceConnectionState || 'unknown';
    if (connectionState === 'failed' || connectionState === 'closed') callbacks.onPause();
    if (connectionState === 'connected') {
      video.play().catch(() => {});
    }
  };
  pc.onconnectionstatechange = stateChange;
  pc.oniceconnectionstatechange = stateChange;

  const variant = qualities.qualityWebrtc;
  const whepUrl = pickWhepUrl(ctx.whepUrl, variant);

  async function negotiate() {
    pc.addTransceiver('video', { direction: 'recvonly' });
    pc.addTransceiver('audio', { direction: 'recvonly' });
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    let timeoutHandle = null;
    const timeout = new Promise((resolve, reject) => {
      timeoutHandle = window.setTimeout(() => reject(new Error('WHEP request timed out')), WHEP_REQUEST_TIMEOUT_MS);
    });
    const response = await Promise.race([
      fetch(whepUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/sdp' },
        body: pc.localDescription.sdp,
        credentials: 'same-origin',
      }),
      timeout,
    ]);
    if (timeoutHandle) window.clearTimeout(timeoutHandle);
    if (!response.ok) {
      let code = '';
      try {
        const payload = await response.clone().json();
        code = payload && payload.code ? ' (' + payload.code + ')' : '';
      } catch (error) {
        // No JSON body.
      }
      throw new Error('WebRTC signaling failed: HTTP ' + response.status + code);
    }
    location = response.headers.get('Location') || '';
    sessionUrl = location;
    const answer = await response.text();
    await pc.setRemoteDescription({ type: 'answer', sdp: answer });
  }

  const ready = negotiate()
    .then(() => {
      callbacks.onCanPlay();
    })
    .catch((error) => {
      callbacks.onError(error);
      throw error;
    });

  const transport = {
    kind: SOURCE_WEBRTC,
    mode: 'whep',
    ready,
    videoElement: () => video,
    play: () => {
      const promise = video.play();
      return promise && typeof promise.then === 'function' ? promise : Promise.resolve();
    },
    pause: () => video.pause(),
    isPaused: () => video.paused,
    currentTime: () => video.currentTime || 0,
    duration: () => video.duration,
    buffered: () => video.buffered,
    isLive: () => true,
    secondsBehindLive: () => 0,
    supportsSeek: () => false,
    getLevels: () => [],
    setLevel: () => {},
    getPlaybackRate: () => video.playbackRate,
    setPlaybackRate: () => {},
    setVolume: (value) => {
      video.volume = clampVolume(value);
    },
    getVolume: () => video.volume,
    setMuted: (value) => {
      video.muted = Boolean(value);
    },
    getMuted: () => video.muted,
    getStats: async () => {
      const stats = {
        protocol: 'WebRTC',
        transportDetail: 'WHEP',
        connectionState,
        width: video.videoWidth || null,
        height: video.videoHeight || null,
        fps: null,
        videoCodec: null,
        audioCodec: null,
        bitrateKbps: null,
        bufferAheadSeconds: bufferHealthAhead(video.buffered, video.currentTime),
        droppedFrames: null,
        totalFrames: null,
        bytesReceived: null,
        optimalWidth: null,
        optimalHeight: null,
        liveLatencySeconds: null,
        liveMode: 'WebRTC (WHEP)',
        streamId: sessionUrl ? sessionUrl.split('/').pop() : null,
        jitterMs: null,
        rttMs: null,
        packetsLost: null,
        details: 'session ' + (sessionUrl ? sessionUrl.split('/').pop() : 'none'),
      };
      try {
        const reports = await pc.getStats();
        let candidatePair = null;
        let videoInbound = null;
        let audioInbound = null;
        const codecs = {};
        reports.forEach((report) => {
          if (report.type === 'codec' && report.id) codecs[report.id] = report;
          if (report.type === 'candidate-pair' && (report.selected || report.nominated)) candidatePair = report;
          if (report.type === 'inbound-rtp' && report.kind === 'video') videoInbound = report;
          if (report.type === 'inbound-rtp' && report.kind === 'audio') audioInbound = report;
        });
        if (videoInbound) {
          const codec = videoInbound.codecId ? codecs[videoInbound.codecId] : null;
          stats.videoCodec = codec && codec.mimeType ? codec.mimeType : null;
          stats.width = videoInbound.frameWidth || stats.width;
          stats.height = videoInbound.frameHeight || stats.height;
          stats.fps = typeof videoInbound.framesPerSecond === 'number' ? videoInbound.framesPerSecond : null;
          stats.droppedFrames = typeof videoInbound.framesDropped === 'number' ? videoInbound.framesDropped : null;
          if (typeof videoInbound.framesDecoded === 'number') {
            stats.totalFrames = videoInbound.framesDecoded + (typeof videoInbound.framesDropped === 'number' ? videoInbound.framesDropped : 0);
          }
          stats.optimalWidth = stats.width;
          stats.optimalHeight = stats.height;
          stats.jitterMs = typeof videoInbound.jitter === 'number' ? videoInbound.jitter * 1000 : null;
          if (typeof videoInbound.packetsLost === 'number') {
            stats.packetsLost = videoInbound.packetsLost;
          }
          const now = Date.now();
          const bytes = Number(videoInbound.bytesReceived) + (audioInbound ? Number(audioInbound.bytesReceived) : 0);
          const kbps = bitrateKbpsFromSamples(samples.video, { bytes, time: now });
          if (kbps !== null) stats.bitrateKbps = kbps;
          stats.bytesReceived = bytes;
          samples.video = { bytes, time: now };
        }
        if (audioInbound) {
          const codec = audioInbound.codecId ? codecs[audioInbound.codecId] : null;
          stats.audioCodec = codec && codec.mimeType ? codec.mimeType : null;
          if (typeof audioInbound.jitter === 'number') stats.jitterMs = audioInbound.jitter * 1000;
        }
        if (candidatePair && typeof candidatePair.currentRoundTripTime === 'number') {
          stats.rttMs = candidatePair.currentRoundTripTime * 1000;
        }
        stats.connectionState = connectionState;
        stats.details = 'WHEP session ' + (sessionUrl ? sessionUrl.split('/').pop() : 'none') + ' / ICE ' + (pc.iceConnectionState || 'unknown');
      } catch (error) {
        // Partial stats are still useful.
      }
      return stats;
    },
    destroy: async () => {
      destroyed = true;
      if (permissionTimer) window.clearTimeout(permissionTimer);
      if (location) {
        try {
          fetch(location, { method: 'DELETE', keepalive: true, credentials: 'same-origin' });
        } catch (error) {
          // Best-effort teardown.
        }
      }
      try {
        video.pause();
      } catch (error) {
        // Ignore.
      }
      video.srcObject = null;
      video.removeAttribute('src');
      try {
        pc.close();
      } catch (error) {
        // Ignore.
      }
      if (video.parentNode) video.parentNode.removeChild(video);
    },
  };
  video.addEventListener('playing', () => callbacks.onPlaying());
  video.addEventListener('pause', () => callbacks.onPause());
  video.addEventListener('waiting', () => callbacks.onWaiting());
  video.addEventListener('canplay', () => callbacks.onCanPlay());
  video.addEventListener('ended', () => callbacks.onEnded());
  video.addEventListener('error', () => {
    if (!destroyed) callbacks.onError(new Error('WebRTC media error'));
  });
  return transport;
}

function createHLSTransport(ctx, container, callbacks, qualities) {
  const hlsConstructor = ctx.libs && ctx.libs.hls;
  const videojsConstructor = ctx.libs && ctx.libs.videojs;
  if (typeof hlsConstructor === 'function' && hlsConstructor.isSupported && hlsConstructor.isSupported()) {
    return createHlsJsTransport(ctx, container, callbacks, qualities, hlsConstructor);
  }
  if (typeof videojsConstructor === 'function') {
    return createVideoJsTransport(ctx, container, callbacks, qualities, videojsConstructor);
  }
  return createNativeHlsTransport(ctx, container, callbacks);
}

function createVideoJsTransport(ctx, container, callbacks, qualities, videojs) {
  const video = makePlainVideo(container);
  const player = videojs(video, {
    controls: false,
    autoplay: false,
    preload: 'auto',
    muted: false,
    fluid: false,
    fill: true,
    responsive: true,
    playsinline: true,
    sources: [{ src: ctx.hlsUrl, type: 'application/x-mpegURL' }],
    html5: { vhs: { overrideNative: true } },
  });
  let destroyed = false;
  const ready = new Promise((resolve) => {
    player.ready(() => resolve());
  });

  const mediaElement = () => {
    try {
      const tech = player.tech(true);
      if (tech && tech.el) return tech.el();
    } catch (error) {
      // Fall through to the original element.
    }
    return video;
  };

  player.on('playing', () => callbacks.onPlaying());
  player.on('pause', () => callbacks.onPause());
  player.on('waiting', () => callbacks.onWaiting());
  player.on('canplay', () => callbacks.onCanPlay());
  player.on('ended', () => callbacks.onEnded());
  player.on('loadedmetadata', () => callbacks.onLevelsChanged());
  player.on('error', () => {
    if (!destroyed) callbacks.onError(new Error('HLS playback error'));
  });
  player.on('timeupdate', () => {});

  function levels() {
    const list = [];
    try {
      const qualityLevels = player.qualityLevels();
      for (let i = 0; i < qualityLevels.length; i += 1) {
        const level = qualityLevels[i];
        list.push({ height: level.height, bitrate: level.bitrate, width: level.width, enabled: level.enabled });
      }
    } catch (error) {
      // Levels may not be populated yet.
    }
    return list;
  }

  function currentLevelIndex(levelList) {
    let enabledIndex = -1;
    let enabledCount = 0;
    for (let i = 0; i < levelList.length; i += 1) {
      if (levelList[i].enabled) {
        enabledCount += 1;
        enabledIndex = i;
      }
    }
    if (enabledCount === 1) return enabledIndex;
    return AUTO_QUALITY;
  }

  const transport = {
    kind: SOURCE_HLS,
    mode: 'videojs',
    ready,
    videoElement: mediaElement,
    play: () => {
      const promise = player.play();
      return promise && typeof promise.then === 'function' ? promise : Promise.resolve();
    },
    pause: () => player.pause(),
    isPaused: () => player.paused(),
    currentTime: () => player.currentTime() || 0,
    duration: () => player.duration(),
    buffered: () => {
      const tech = mediaElement();
      return tech ? tech.buffered : null;
    },
    seekableWindow: () => {
      const tech = mediaElement();
      if (!tech || !tech.seekable || tech.seekable.length === 0) return null;
      try {
        return { start: tech.seekable.start(0), end: tech.seekable.end(tech.seekable.length - 1) };
      } catch (error) {
        return null;
      }
    },
    bufferEnd: () => {
      const window = transport.seekableWindow();
      const tech = mediaElement();
      if (!tech || !tech.buffered || tech.buffered.length === 0) return window ? window.start : 0;
      try {
        return tech.buffered.end(tech.buffered.length - 1);
      } catch (error) {
        return window ? window.start : 0;
      }
    },
    isLive: () => {
      const duration = player.duration();
      return !isFinite(duration) || duration === Infinity || duration === 0;
    },
    secondsBehindLive: () => {
      const window = transport.seekableWindow();
      if (!window) return 0;
      return Math.max(0, window.end - (player.currentTime() || 0));
    },
    supportsSeek: () => true,
    seek: (time) => {
      player.currentTime(time);
    },
    seekToLive: () => {
      const window = transport.seekableWindow();
      if (window) player.currentTime(Math.max(window.start, window.end - 0.5));
    },
    getLevels: () => levels(),
    currentLevel: () => currentLevelIndex(levels()),
    setLevel: (value) => {
      const levelList = levels();
      const target = value === AUTO_QUALITY ? AUTO_QUALITY : Number(value);
      try {
        const qualityLevels = player.qualityLevels();
        for (let i = 0; i < qualityLevels.length; i += 1) {
          qualityLevels[i].enabled = target === AUTO_QUALITY ? true : i === target;
        }
      } catch (error) {
        // Selection is best-effort.
      }
      return levelList.length;
    },
    getPlaybackRate: () => player.playbackRate(),
    setPlaybackRate: (rate) => player.playbackRate(rate),
    setVolume: (value) => player.volume(clampVolume(value)),
    getVolume: () => player.volume(),
    setMuted: (value) => player.muted(Boolean(value)),
    getMuted: () => player.muted(),
    getStats: async () => {
      const tech = mediaElement();
      const vhs = deepValue(player, ['tech_', 'vhs']) || deepValue(player, ['tech', 'vhs']);
      const master = deepValue(vhs, ['playlists', 'master']);
      const levelList = levels();
      const current = currentLevelIndex(levelList);
      const level = current === AUTO_QUALITY ? levelList[0] : levelList[current];
      const bandwidth = deepValue(vhs, ['bandwidth']) || deepValue(master, ['bandwidth']) || deepValue(vhs, ['masterPlaylistController_', 'bandwidth']);
      const playbackQuality = tech && tech.getVideoPlaybackQuality ? tech.getVideoPlaybackQuality() : null;
      let codecs = '';
      try {
        const playlists = deepValue(master, ['playlists']) || [];
        const first = playlists[0] || {};
        codecs = deepValue(first, ['attributes', 'CODECS']) || deepValue(first, ['attributes', 'codecs']) || '';
      } catch (error) {
        codecs = '';
      }
      const videoCodec = codecs ? codecs.split(',')[0].trim() : null;
      const audioCodec = codecs && codecs.indexOf(',') !== -1 ? codecs.split(',')[1].trim() : null;
      const behind = transport.secondsBehindLive();
      const stats = {
        protocol: 'HLS',
        transportDetail: 'video.js VHS',
        connectionState: 'stable',
        streamId: 'owncast-hls',
        width: (tech && tech.videoWidth) || null,
        height: (tech && tech.videoHeight) || null,
        optimalWidth: (level && level.width) || (tech && tech.videoWidth) || null,
        optimalHeight: (level && level.height) || (tech && tech.videoHeight) || null,
        fps: null,
        videoCodec,
        audioCodec,
        bitrateKbps: level && level.bitrate ? level.bitrate / 1000 : bandwidth ? bandwidth / 1000 : null,
        bufferAheadSeconds: tech ? bufferHealthAhead(tech.buffered, tech.currentTime) : null,
        droppedFrames: playbackQuality && typeof playbackQuality.droppedVideoFrames === 'number' ? playbackQuality.droppedVideoFrames : null,
        totalFrames: playbackQuality && typeof playbackQuality.totalVideoFrames === 'number' ? playbackQuality.totalVideoFrames : null,
        bytesReceived: null,
        liveLatencySeconds: typeof behind === 'number' ? Math.max(0, behind) : null,
        liveMode: 'HLS (VHS), level ' + (current === AUTO_QUALITY ? 'auto' : String(current)),
        jitterMs: null,
        rttMs: null,
        packetsLost: null,
        details: 'level ' + (current === AUTO_QUALITY ? 'auto' : String(current)) + (bandwidth ? ' / bandwidth ' + Math.round(bandwidth / 1000) + ' kbps' : ''),
      };
      return stats;
    },
    destroy: async () => {
      destroyed = true;
      try {
        player.dispose();
      } catch (error) {
        // Ignore double dispose.
      }
    },
  };
  return transport;
}

function createHlsJsTransport(ctx, container, callbacks, qualities, Hls) {
  const video = makePlainVideo(container);
  const hls = new Hls({ lowLatencyMode: Boolean(ctx.lowLatency), enableWorker: true });
  let destroyed = false;
  const levels = [];
  hls.loadSource(ctx.hlsUrl);
  hls.attachMedia(video);
  if (Hls.Events) {
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      (hls.levels || []).forEach((level) => levels.push(level));
      callbacks.onLevelsChanged();
      callbacks.onCanPlay();
    });
    hls.on(Hls.Events.LEVEL_SWITCHED, () => callbacks.onLevelsChanged());
    hls.on(Hls.Events.ERROR, (event, data) => {
      if (data && data.fatal) callbacks.onError(new Error('HLS error: ' + data.details));
    });
  } else {
    hls.on('hlsManifestParsed', () => callbacks.onCanPlay());
  }
  video.addEventListener('playing', () => callbacks.onPlaying());
  video.addEventListener('pause', () => callbacks.onPause());
  video.addEventListener('waiting', () => callbacks.onWaiting());
  video.addEventListener('canplay', () => callbacks.onCanPlay());
  video.addEventListener('ended', () => callbacks.onEnded());

  function levelList() {
    return (hls.levels || []).map((level) => ({
      height: level.height,
      width: level.width,
      bitrate: level.bitrate,
      codecs: level.videoCodec || level.audioCodec,
    }));
  }

  const transport = {
    kind: SOURCE_HLS,
    mode: 'hlsjs',
    videoElement: () => video,
    play: () => video.play(),
    pause: () => video.pause(),
    isPaused: () => video.paused,
    currentTime: () => video.currentTime,
    duration: () => video.duration,
    buffered: () => video.buffered,
    seekableWindow: () => {
      if (!video.seekable || video.seekable.length === 0) return null;
      try {
        return { start: video.seekable.start(0), end: video.seekable.end(video.seekable.length - 1) };
      } catch (error) {
        return null;
      }
    },
    bufferEnd: () => {
      if (!video.buffered || video.buffered.length === 0) return 0;
      return video.buffered.end(video.buffered.length - 1);
    },
    isLive: () => true,
    secondsBehindLive: () => {
      const window = transport.seekableWindow();
      if (!window) return 0;
      return Math.max(0, window.end - video.currentTime);
    },
    supportsSeek: () => true,
    seek: (time) => {
      video.currentTime = time;
    },
    seekToLive: () => {
      const window = transport.seekableWindow();
      if (window) video.currentTime = Math.max(window.start, window.end - 0.5);
    },
    getLevels: () => levelList(),
    currentLevel: () => (hls.currentLevel < 0 ? AUTO_QUALITY : hls.currentLevel),
    setLevel: (value) => {
      hls.currentLevel = value === AUTO_QUALITY ? -1 : Number(value);
    },
    getPlaybackRate: () => video.playbackRate,
    setPlaybackRate: (rate) => {
      video.playbackRate = rate;
    },
    setVolume: (value) => {
      video.volume = clampVolume(value);
    },
    getVolume: () => video.volume,
    setMuted: (value) => {
      video.muted = Boolean(value);
    },
    getMuted: () => video.muted,
    getStats: async () => {
      const level = hls.currentLevel >= 0 && hls.levels ? hls.levels[hls.currentLevel] : null;
      const playbackQuality = video.getVideoPlaybackQuality ? video.getVideoPlaybackQuality() : null;
      const behind = transport.secondsBehindLive();
      return {
        protocol: 'HLS',
        transportDetail: 'hls.js',
        connectionState: 'stable',
        streamId: 'owncast-hls',
        width: video.videoWidth || null,
        height: video.videoHeight || null,
        optimalWidth: level ? level.width || null : null,
        optimalHeight: level ? level.height || null : null,
        fps: null,
        videoCodec: level ? level.videoCodec || null : null,
        audioCodec: level ? level.audioCodec || null : null,
        bitrateKbps: level && level.bitrate ? level.bitrate / 1000 : hls.bandwidthEstimate ? hls.bandwidthEstimate / 1000 : null,
        bufferAheadSeconds: bufferHealthAhead(video.buffered, video.currentTime),
        droppedFrames: playbackQuality && typeof playbackQuality.droppedVideoFrames === 'number' ? playbackQuality.droppedVideoFrames : null,
        totalFrames: playbackQuality && typeof playbackQuality.totalVideoFrames === 'number' ? playbackQuality.totalVideoFrames : null,
        bytesReceived: null,
        liveLatencySeconds: typeof behind === 'number' ? Math.max(0, behind) : null,
        liveMode: 'HLS (hls.js), level ' + (hls.currentLevel < 0 ? 'auto' : String(hls.currentLevel)),
        jitterMs: null,
        rttMs: null,
        packetsLost: null,
        details: 'level ' + (hls.currentLevel < 0 ? 'auto' : String(hls.currentLevel)) + ' / bandwidth ' + Math.round((hls.bandwidthEstimate || 0) / 1000) + ' kbps',
      };
    },
    destroy: async () => {
      destroyed = true;
      try {
        hls.destroy();
      } catch (error) {
        // Ignore double destroy.
      }
      video.removeAttribute('src');
      if (video.parentNode) video.parentNode.removeChild(video);
    },
  };
  return transport;
}

function createNativeHlsTransport(ctx, container, callbacks) {
  const video = makePlainVideo(container);
  video.src = ctx.hlsUrl;
  let destroyed = false;
  video.addEventListener('playing', () => callbacks.onPlaying());
  video.addEventListener('pause', () => callbacks.onPause());
  video.addEventListener('waiting', () => callbacks.onWaiting());
  video.addEventListener('canplay', () => callbacks.onCanPlay());
  video.addEventListener('ended', () => callbacks.onEnded());
  video.addEventListener('error', () => {
    if (!destroyed) callbacks.onError(new Error('HLS is not supported by this browser and no HLS library was provided by the host'));
  });
  const transport = {
    kind: SOURCE_HLS,
    mode: 'native',
    videoElement: () => video,
    play: () => video.play(),
    pause: () => video.pause(),
    isPaused: () => video.paused,
    currentTime: () => video.currentTime,
    duration: () => video.duration,
    buffered: () => video.buffered,
    seekableWindow: () => {
      if (!video.seekable || video.seekable.length === 0) return null;
      try {
        return { start: video.seekable.start(0), end: video.seekable.end(video.seekable.length - 1) };
      } catch (error) {
        return null;
      }
    },
    bufferEnd: () => {
      if (!video.buffered || video.buffered.length === 0) return 0;
      return video.buffered.end(video.buffered.length - 1);
    },
    isLive: () => true,
    secondsBehindLive: () => 0,
    supportsSeek: () => true,
    seek: (time) => {
      video.currentTime = time;
    },
    seekToLive: () => {},
    getLevels: () => [],
    setLevel: () => {},
    getPlaybackRate: () => video.playbackRate,
    setPlaybackRate: (rate) => {
      video.playbackRate = rate;
    },
    setVolume: (value) => {
      video.volume = clampVolume(value);
    },
    getVolume: () => video.volume,
    setMuted: (value) => {
      video.muted = Boolean(value);
    },
    getMuted: () => video.muted,
    getStats: async () => ({
      protocol: 'HLS',
      transportDetail: 'native HTML video',
      connectionState: 'stable',
      streamId: 'owncast-hls',
      width: video.videoWidth || null,
      height: video.videoHeight || null,
      optimalWidth: video.videoWidth || null,
      optimalHeight: video.videoHeight || null,
      fps: null,
      videoCodec: null,
      audioCodec: null,
      bitrateKbps: null,
      bufferAheadSeconds: bufferHealthAhead(video.buffered, video.currentTime),
      droppedFrames: null,
      totalFrames: null,
      bytesReceived: null,
      liveLatencySeconds: typeof transport.secondsBehindLive === 'function' ? transport.secondsBehindLive() : null,
      liveMode: 'HLS (native)',
      jitterMs: null,
      rttMs: null,
      packetsLost: null,
      details: 'native playback',
    }),
    destroy: async () => {
      destroyed = true;
      video.pause();
      video.removeAttribute('src');
      video.load();
      if (video.parentNode) video.parentNode.removeChild(video);
    },
  };
  return transport;
}

