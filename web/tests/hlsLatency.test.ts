import {
  hlsForwardBufferSeconds,
  hlsLiveLatencySeconds,
  hlsPlaylistSegments,
} from '../components/video/PlayerControls/stats';

describe('HLS live latency from program-date-time', () => {
  const base = Date.UTC(2026, 10, 10, 13, 47, 50); // arbitrary wall clock

  const segments = [
    { start: 100, end: 101, duration: 1, dateTimeObject: new Date(base) },
    { start: 101, end: 102, duration: 1, dateTimeObject: null },
    { start: 102, end: 103, duration: 1, dateTimeObject: null },
  ];

  test('measures the real delay against the wall clock', () => {
    // Playhead sits 0.5 s into the first segment, 4 s after it was packaged.
    const latency = hlsLiveLatencySeconds({
      nowMs: base + 4000 + 500,
      currentTime: 100.5,
      segments,
      liveEdgeLatencySeconds: 0.8,
    });
    expect(latency).toBeCloseTo(4, 5);
  });

  test('anchors on the closest preceding stamped segment for later segments', () => {
    const latency = hlsLiveLatencySeconds({
      nowMs: base + 3000 + 2500,
      currentTime: 102.5,
      segments,
      liveEdgeLatencySeconds: null,
    });
    expect(latency).toBeCloseTo(3, 5);
  });

  test('falls back to the seekable-edge distance without stamps', () => {
    expect(
      hlsLiveLatencySeconds({
        nowMs: base,
        currentTime: 10,
        segments: [{ start: 10, end: 11, duration: 1 }],
        liveEdgeLatencySeconds: 0.8,
      }),
    ).toBeCloseTo(0.8, 5);
  });

  test('rejects implausible values instead of reporting nonsense', () => {
    expect(
      hlsLiveLatencySeconds({
        nowMs: base - 60_000,
        currentTime: 100,
        segments,
        liveEdgeLatencySeconds: null,
      }),
    ).toBeNull();
  });

  test('reads segments out of the VHS playlist controller', () => {
    const vhs = { playlistController_: { media: () => ({ segments }) } };
    expect(hlsPlaylistSegments(vhs)).toHaveLength(3);
    expect(hlsPlaylistSegments({})).toBeNull();
    expect(hlsPlaylistSegments(null)).toBeNull();
  });
});

describe('HLS forward buffer', () => {
  test('counts only the range containing the playhead', () => {
    expect(hlsForwardBufferSeconds([{ start: 0, end: 5 }], 3)).toBeCloseTo(2, 5);
    expect(
      hlsForwardBufferSeconds(
        [
          { start: 0, end: 2 },
          { start: 10, end: 14 },
        ],
        10.5,
      ),
    ).toBeCloseTo(3.5, 5);
  });

  test('reports zero when the playhead is outside every range', () => {
    expect(hlsForwardBufferSeconds([{ start: 0, end: 2 }], 8)).toBe(0);
    expect(hlsForwardBufferSeconds([], 8)).toBeNull();
    expect(hlsForwardBufferSeconds(null, 8)).toBeNull();
  });
});
