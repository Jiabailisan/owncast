// Unit tests for the YouTube Live-style custom player (task-24/32).
//
// The example player is served as a single ES module, so its pure helpers are
// exported from the real file and tested here without a browser. This keeps the
// logic that decides transports/persistence/stats formatting covered while the
// Playwright suite (tools/browser/youtube-player-e2e.mjs) covers the UI,
// video scaling and live-edge behavior.

import {
  AUTO_QUALITY,
  DEFAULT_PREFERENCES,
  SOURCE_AUTO,
  SOURCE_HLS,
  SOURCE_WEBRTC,
  bitrateKbpsFromSamples,
  bufferHealthAhead,
  clampVolume,
  formatKbps,
  formatStatValue,
  formatTime,
  getStatsRows,
  levelLabel,
  loadPreferences,
  nextSourceChoice,
  normalizeQualityPreference,
  normalizeSourceChoice,
  parseServerStatus,
  pickWhepUrl,
  qualityLabel,
  resolveTransport,
  savePreferences,
} from '../../../docs/examples/custom-player/youtube-live-player.js';

type MemoryStorage = {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
};

function memoryStorage(initial: Record<string, string> = {}): MemoryStorage {
  const data: Record<string, string> = { ...initial };
  return {
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = String(value);
    },
  };
}

function fakeTimeRanges(ranges: Array<[number, number]>) {
  return {
    length: ranges.length,
    start: (index: number) => ranges[index][0],
    end: (index: number) => ranges[index][1],
  } as unknown as TimeRanges;
}

function rowsToMap(rows: Array<{ label: string; value: string; bar?: number }>) {
  return Object.fromEntries(rows.map((row) => [row.label, row]));
}

describe('transport selection', () => {
  it('normalizes unknown choices to auto', () => {
    expect(normalizeSourceChoice('auto')).toBe(SOURCE_AUTO);
    expect(normalizeSourceChoice('webrtc')).toBe(SOURCE_WEBRTC);
    expect(normalizeSourceChoice('hls')).toBe(SOURCE_HLS);
    expect(normalizeSourceChoice('bogus')).toBe(SOURCE_AUTO);
    expect(normalizeSourceChoice(undefined)).toBe(SOURCE_AUTO);
  });

  it('resolves auto to WebRTC only when it is available', () => {
    expect(resolveTransport(SOURCE_AUTO, { webrtc: true })).toBe(SOURCE_WEBRTC);
    expect(resolveTransport(SOURCE_AUTO, { webrtc: false })).toBe(SOURCE_HLS);
  });

  it('falls back to HLS when WebRTC is requested but unavailable', () => {
    expect(resolveTransport(SOURCE_WEBRTC, { webrtc: true })).toBe(SOURCE_WEBRTC);
    expect(resolveTransport(SOURCE_WEBRTC, { webrtc: false })).toBe(SOURCE_HLS);
    expect(resolveTransport(SOURCE_HLS, { webrtc: true })).toBe(SOURCE_HLS);
  });

  it('cycles source choices auto -> webrtc -> hls', () => {
    expect(nextSourceChoice(SOURCE_AUTO)).toBe(SOURCE_WEBRTC);
    expect(nextSourceChoice(SOURCE_WEBRTC)).toBe(SOURCE_HLS);
    expect(nextSourceChoice(SOURCE_HLS)).toBe(SOURCE_AUTO);
  });
});

describe('preferences persistence', () => {
  it('returns defaults for a fresh storage', () => {
    expect(loadPreferences(memoryStorage())).toEqual(DEFAULT_PREFERENCES);
  });

  it('round-trips every preference', () => {
    const storage = memoryStorage();
    const preferences = {
      ...DEFAULT_PREFERENCES,
      source: SOURCE_HLS,
      volume: 0.35,
      muted: true,
      hlsQuality: 2,
      webrtcVariant: 1,
      speed: 1.5,
      stats: true,
    };
    savePreferences(storage, preferences);
    expect(loadPreferences(storage)).toEqual(preferences);
  });

  it('clamps corrupt values instead of throwing', () => {
    const storage = memoryStorage({
      'owncast-youtube-player:source': 'nope',
      'owncast-youtube-player:volume': '9',
      'owncast-youtube-player:speed': '-4',
      'owncast-youtube-player:hls-quality': 'bad',
    });
    const preferences = loadPreferences(storage);
    expect(preferences.source).toBe(SOURCE_AUTO);
    expect(preferences.volume).toBe(1);
    expect(preferences.speed).toBe(0.5);
    expect(preferences.hlsQuality).toBe(AUTO_QUALITY);
  });

  it('clamps the volume helper', () => {
    expect(clampVolume(2)).toBe(1);
    expect(clampVolume(-1)).toBe(0);
    expect(clampVolume('0.4')).toBeCloseTo(0.4);
  });
});

describe('formatting helpers', () => {
  it('formats durations', () => {
    expect(formatTime(0)).toBe('0:00');
    expect(formatTime(65)).toBe('1:05');
    expect(formatTime(3725)).toBe('1:02:05');
    expect(formatTime(-1)).toBe('0:00');
    expect(formatTime(Number.NaN)).toBe('0:00');
  });

  it('formats bitrates and stat values with N/A fallbacks', () => {
    expect(formatKbps(1234.4)).toBe('1234 kbps');
    expect(formatKbps(null)).toBe('N/A');
    expect(formatKbps(Number.POSITIVE_INFINITY)).toBe('N/A');
    expect(formatStatValue(12)).toBe('12');
    expect(formatStatValue(null)).toBe('N/A');
    expect(formatStatValue(Number.NaN)).toBe('N/A');
  });

  it('labels levels and qualities', () => {
    expect(levelLabel({ height: 720 }, 0)).toBe('720p');
    expect(levelLabel(null, 3)).toBe('Level 3');
    expect(levelLabel(null)).toBe('Auto');
    expect(qualityLabel(AUTO_QUALITY, [])).toBe('Auto');
    expect(qualityLabel('1', [{ height: 1080 }, { height: 480 }])).toBe('480p');
  });

  it('normalizes quality preferences to auto for invalid values', () => {
    expect(normalizeQualityPreference('auto', 3)).toBe(AUTO_QUALITY);
    expect(normalizeQualityPreference('1', 3)).toBe(1);
    expect(normalizeQualityPreference('7', 3)).toBe(AUTO_QUALITY);
    expect(normalizeQualityPreference('-1', 3)).toBe(AUTO_QUALITY);
    expect(normalizeQualityPreference('bad', 3)).toBe(AUTO_QUALITY);
  });
});

describe('WHEP URL and stats math', () => {
  it('appends the variant query only for explicit variants', () => {
    expect(pickWhepUrl('/webrtc/whep', AUTO_QUALITY)).toBe('/webrtc/whep');
    expect(pickWhepUrl('/webrtc/whep', 2)).toBe('/webrtc/whep?variant=2');
    expect(pickWhepUrl('/webrtc/whep?x=1', 0)).toBe('/webrtc/whep?x=1&variant=0');
    expect(pickWhepUrl('', 1)).toBe('');
  });

  it('computes buffer health ahead of the playhead', () => {
    expect(bufferHealthAhead(fakeTimeRanges([[10, 20]]), 12)).toBeCloseTo(8);
    expect(bufferHealthAhead(fakeTimeRanges([[10, 20]]), 20.5)).toBeCloseTo(0);
    expect(bufferHealthAhead(fakeTimeRanges([]), 1)).toBeNull();
    expect(bufferHealthAhead(null, 1)).toBeNull();
  });

  it('computes bitrate from monotonic byte samples', () => {
    const kbps = bitrateKbpsFromSamples({ bytes: 1000, time: 0 }, { bytes: 251000, time: 1000 });
    expect(kbps).toBeCloseTo(2000);
    expect(bitrateKbpsFromSamples({ bytes: 2000, time: 1000 }, { bytes: 1000, time: 2000 })).toBeNull();
    expect(bitrateKbpsFromSamples(null, { bytes: 1, time: 1 })).toBeNull();
  });

  it('parses the public server status defensively', () => {
    expect(parseServerStatus({ online: true, viewerCount: 4, inputProtocol: 'srt' }))
      .toEqual({ online: true, viewerCount: 4, inputProtocol: 'srt' });
    expect(parseServerStatus(null)).toEqual({ online: null, viewerCount: null, inputProtocol: null });
    expect(parseServerStatus({ online: 'yes' })).toEqual({ online: null, viewerCount: null, inputProtocol: null });
  });
});

describe('stats for nerds rows (YouTube layout)', () => {
  const webrtcStats = {
    protocol: 'WebRTC',
    width: 1280,
    height: 720,
    fps: 30,
    videoCodec: 'video/H264',
    audioCodec: 'audio/opus',
    bitrateKbps: 2400,
    bufferAheadSeconds: 0.4,
    droppedFrames: 2,
    totalFrames: 640,
    bytesReceived: 1024 * 1024,
    optimalWidth: 1280,
    optimalHeight: 720,
    liveLatencySeconds: null,
    liveMode: 'WebRTC (WHEP), ICE connected',
    streamId: 'abc123',
  };
  const ui = { viewportWidth: 1112, viewportHeight: 626, volumePct: 100, normalizedPct: 100, date: 'Mon Nov 09 2026' };

  it('renders the exact YouTube labels in order', () => {
    const labels = getStatsRows(webrtcStats, { online: true, viewerCount: 3, inputProtocol: 'rtmp' }, ui)
      .map((row) => row.label);
    expect(labels).toEqual([
      'Video ID / sCPN',
      'Viewport / Frames',
      'Current / Optimal Res',
      'Volume / Normalized',
      'Codecs',
      'Connection Speed',
      'Network Activity',
      'Buffer Health',
      'Live Latency',
      'Live Mode',
      'Server / Ingest',
      'Mystery Text',
      'Date',
    ]);
  });

  it('formats WebRTC values and includes the bar rows', () => {
    const rows = rowsToMap(getStatsRows(webrtcStats, { online: true, viewerCount: 3, inputProtocol: 'rtmp' }, ui));
    expect(rows['Video ID / sCPN'].value).toBe('abc123');
    expect(rows['Viewport / Frames'].value).toBe('1112x626 / 2 dropped of 640');
    expect(rows['Current / Optimal Res'].value).toBe('1280x720@30 / 1280x720');
    expect(rows['Volume / Normalized'].value).toBe('100%/100%');
    expect(rows['Codecs'].value).toBe('video/H264 / audio/opus');
    expect(rows['Connection Speed'].value).toBe('2400 Kbps');
    expect(rows['Network Activity'].value).toBe('1024.00 KB');
    expect(rows['Live Mode'].value).toContain('WebRTC');
    expect(rows['Server / Ingest'].value).toContain('RTMP');
    expect(rows['Date'].value).toBe('Mon Nov 09 2026');
    expect(rows['Connection Speed'].bar).toBeGreaterThan(0);
    expect(rows['Network Activity'].bar).toBeGreaterThan(0);
  });

  it('formats HLS values with buffer/latency bars', () => {
    const rows = rowsToMap(getStatsRows(
      { width: 1280, height: 720, fps: 30, bitrateKbps: 2500, bufferAheadSeconds: 6.5, liveLatencySeconds: 2.5, liveMode: 'HLS (VHS)' },
      { online: true, viewerCount: 1, inputProtocol: 'srt' },
      ui,
    ));
    expect(rows['Buffer Health'].value).toBe('6.50 s');
    expect(rows['Live Latency'].value).toBe('2.50s');
    expect(rows['Buffer Health'].bar).toBeGreaterThan(0);
    expect(rows['Server / Ingest'].value).toContain('SRT');
  });

  it('uses N/A placeholders for missing values', () => {
    const rows = rowsToMap(getStatsRows({}, { online: null, viewerCount: null }, {}));
    expect(rows['Video ID / sCPN'].value).toBe('N/A');
    expect(rows['Viewport / Frames'].value).toBe('N/A / N/A');
    expect(rows['Codecs'].value).toBe('N/A');
    expect(rows['Connection Speed'].value).toBe('N/A');
    expect(rows['Live Latency'].value).toBe('N/A');
    expect(rows['Date'].value).not.toBe('N/A');
  });
});
