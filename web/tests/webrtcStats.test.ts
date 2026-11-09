// Unit tests for the WebRTC Stats-for-nerds collector and client stats beacon
// (round-7: ICE transport, validated FPS, consistent packet-loss payload).

import {
  chooseEncodedWebRTCVariant,
  collectVideoInboundTotals,
  collectWebRTCStatsPanel,
  formatWebRTCResolution,
  packetLossPercent,
  packetLossPctFor,
  plausibleFrameRate,
  selectedIceTransport,
  startWebRTCStatsBeacon,
  summarizeWebRTCStats,
  webRTCLiveMode,
} from '../components/video/webrtc/webrtc-stats';

function report(entries: Record<string, unknown>[]): RTCStatsReport {
  return new Map(entries.map(entry => [String(entry.id), entry])) as unknown as RTCStatsReport;
}

function videoStats(overrides: Record<string, unknown> = {}) {
  return {
    id: 'inbound-video',
    type: 'inbound-rtp',
    kind: 'video',
    frameWidth: 2560,
    frameHeight: 1440,
    framesPerSecond: 60,
    framesDecoded: 600,
    packetsLost: 126,
    packetsReceived: 8750,
    jitter: 0.005,
    bytesReceived: 8_000_000,
    timestamp: 10_000,
    ...overrides,
  };
}

function iceEntries(protocol: string) {
  return [
    {
      id: 'pair',
      type: 'candidate-pair',
      nominated: true,
      state: 'succeeded',
      localCandidateId: 'local',
      remoteCandidateId: 'remote',
      currentRoundTripTime: 0.003,
      bytesReceived: 1000,
    },
    // Chrome reports local/remote candidates with these type names.
    { id: 'local', type: 'local-candidate', protocol, candidateType: 'host' },
    { id: 'remote', type: 'remote-candidate', protocol, candidateType: 'host' },
  ];
}

describe('WebRTC ICE transport detection', () => {
  test('uses the selected/nominated candidate pair protocol', () => {
    expect(selectedIceTransport(report([videoStats(), ...iceEntries('udp')]))).toBe('udp');
    expect(selectedIceTransport(report([videoStats(), ...iceEntries('tcp')]))).toBe('tcp');
    expect(selectedIceTransport(report([videoStats()]))).toBeNull();
  });

  test('formats the Live Mode label as WebRTC-U / WebRTC-T / WebRTC', () => {
    expect(webRTCLiveMode('udp')).toBe('WebRTC-U');
    expect(webRTCLiveMode('tcp')).toBe('WebRTC-T');
    expect(webRTCLiveMode(null)).toBe('WebRTC');
  });

  test('falls back to a succeeded pair when none is nominated', () => {
    const entries = [
      {
        id: 'pair',
        type: 'candidate-pair',
        state: 'succeeded',
        localCandidateId: 'local',
        remoteCandidateId: 'remote',
        bytesReceived: 100,
      },
      { id: 'local', type: 'candidate', protocol: 'tcp' },
      { id: 'remote', type: 'candidate', protocol: 'tcp' },
    ];
    expect(selectedIceTransport(report(entries))).toBe('tcp');
  });
});

describe('WebRTC frame-rate validation', () => {
  test('clamps plausible values and rejects absurd or non-positive ones', () => {
    expect(plausibleFrameRate(60)).toBe(60);
    expect(plausibleFrameRate(59.94)).toBe(59.9);
    expect(plausibleFrameRate(571)).toBeNull();
    expect(plausibleFrameRate(0)).toBeNull();
    expect(plausibleFrameRate(-1)).toBeNull();
    expect(plausibleFrameRate(Number.NaN)).toBeNull();
  });

  test('never forwards a 571 FPS delta from the beacon summary', () => {
    const previous = { framesDecoded: 600, timestampSeconds: 10 };
    // 571 FPS over a 0.1s window: rejected because the window is too short and
    // the value is implausible.
    const shortWindow = summarizeWebRTCStats(
      report([
        videoStats({ framesPerSecond: undefined, framesDecoded: 657, timestamp: 10_100 }),
        ...iceEntries('udp'),
      ]),
      previous,
    );
    expect(shortWindow.summary.fps).toBeUndefined();

    // 60 frames over one second is accepted.
    const saneWindow = summarizeWebRTCStats(
      report([
        videoStats({ framesPerSecond: undefined, framesDecoded: 660, timestamp: 11_000 }),
        ...iceEntries('udp'),
      ]),
      previous,
    );
    expect(saneWindow.summary.fps).toBe(60);
  });

  test('prefers measured decoded deltas over receiver-reported fps', () => {
    const result = summarizeWebRTCStats(
      report([
        videoStats({ framesPerSecond: 30, framesDecoded: 600, timestamp: 10_000 }),
        ...iceEntries('udp'),
      ]),
      { framesDecoded: 540, timestampSeconds: 9 },
    );
    expect(result.summary.fps).toBe(60);
  });

  test('falls back to the receiver-reported fps without a previous sample', () => {
    const result = summarizeWebRTCStats(
      report([videoStats({ framesPerSecond: 59.9 }), ...iceEntries('udp')]),
    );
    expect(result.summary.fps).toBe(59.9);
  });
});

describe('WebRTC packet loss and beacon payload', () => {
  test('uses one loss formula for beacon and panel', () => {
    expect(packetLossPercent(126, 8750)).toBe(1.42);
    expect(packetLossPercent(0, 0)).toBe(0);
    expect(packetLossPercent(null, 100)).toBeNull();
    expect(packetLossPercent(-5, 100)).toBe(0);
  });

  test('summary sends decoded size, transport, received packets and loss pct', () => {
    const { summary } = summarizeWebRTCStats(report([videoStats(), ...iceEntries('udp')]));
    expect(summary).toMatchObject({
      decodedWidth: 2560,
      decodedHeight: 1440,
      fps: 60,
      packetsLost: 126,
      packetsReceived: 8750,
      packetLossPct: 1.42,
      jitterMs: 5,
      roundTripTimeMs: 3,
      transport: 'udp',
    });
  });

  test('panel sample carries transport, sanitized counters and fps', () => {
    const { sample } = collectWebRTCStatsPanel(report([videoStats(), ...iceEntries('tcp')]));
    expect(sample.transport).toBe('tcp');
    expect(sample.packetsLost).toBe(126);
    expect(sample.packetsReceived).toBe(8750);
    expect(sample.framesPerSecond).toBe(60);
  });

  test('panel derives fps from a >=1s window when framesPerSecond is absent', () => {
    jest.spyOn(Date, 'now').mockReturnValue(1000);
    const first = collectWebRTCStatsPanel(
      report([
        videoStats({ framesPerSecond: undefined, framesDecoded: 600 }),
        ...iceEntries('udp'),
      ]),
    );
    jest.spyOn(Date, 'now').mockReturnValue(3000);
    const second = collectWebRTCStatsPanel(
      report([
        videoStats({ framesPerSecond: undefined, framesDecoded: 720 }),
        ...iceEntries('udp'),
      ]),
      first.bytes,
    );
    jest.restoreAllMocks();
    expect(first.sample.framesPerSecond).toBeNull();
    expect(second.sample.framesPerSecond).toBe(60);
  });

  test('beacon posts the additive fields with a validated fps', async () => {
    const fetchMock = jest.fn(async () => new Response(null, { status: 204 }));
    const beacon = startWebRTCStatsBeacon({
      pc: {
        getStats: async () => report([videoStats(), ...iceEntries('udp')]),
      } as unknown as RTCPeerConnection,
      sessionId: 'abc',
      fetchImpl: fetchMock as unknown as typeof fetch,
      intervalMs: 1_000_000,
    });
    await beacon.sampleOnce();
    beacon.stop();

    const call = fetchMock.mock.calls[0] as unknown as [string, { body: string }];
    const body = JSON.parse(call[1].body);
    expect(body).toMatchObject({
      sessionId: 'abc',
      decodedWidth: 2560,
      decodedHeight: 1440,
      fps: 60,
      packetsLost: 126,
      packetsReceived: 8750,
      packetLossPct: 1.42,
      transport: 'udp',
    });
    expect(body.fps).toBeLessThanOrEqual(240);
  });

  test('beacon never posts an implausible fps', async () => {
    const fetchMock = jest.fn(async () => new Response(null, { status: 204 }));
    const beacon = startWebRTCStatsBeacon({
      pc: {
        getStats: async () => report([videoStats({ framesPerSecond: 571 }), ...iceEntries('udp')]),
      } as unknown as RTCPeerConnection,
      sessionId: 'abc',
      fetchImpl: fetchMock as unknown as typeof fetch,
      intervalMs: 1_000_000,
    });
    await beacon.sampleOnce();
    beacon.stop();
    const call = fetchMock.mock.calls[0] as unknown as [string, { body: string }];
    expect(JSON.parse(call[1].body).fps).toBeUndefined();
  });
});

describe('WebRTC resolution formatting', () => {
  test('appends @fps when a plausible rate is known', () => {
    expect(formatWebRTCResolution(2560, 1440, 60)).toBe('2560x1440@60fps');
    expect(formatWebRTCResolution(2560, 1440, null)).toBe('2560x1440');
    expect(formatWebRTCResolution(0, 0, 60)).toBe('N/A');
  });
});

describe('WebRTC beacon bitrate', () => {
  test('posts a between-sample inbound video bitrate', async () => {
    const first = report([
      videoStats({ framesPerSecond: 60, bytesReceived: 1_000_000, timestamp: 10_000 }),
      ...iceEntries('udp'),
    ]);
    const second = report([
      videoStats({ framesPerSecond: 60, bytesReceived: 6_000_000, timestamp: 11_000 }),
      ...iceEntries('udp'),
    ]);
    let call = 0;
    const fetchMock = jest.fn(async () => new Response(null, { status: 204 }));
    const beacon = startWebRTCStatsBeacon({
      pc: {} as RTCPeerConnection,
      sessionId: 'abc',
      fetchImpl: fetchMock as unknown as typeof fetch,
      getStatsImpl: async () => (call++ === 0 ? first : second),
      intervalMs: 1_000_000,
    });
    await beacon.sampleOnce();
    await beacon.sampleOnce();
    beacon.stop();
    const bodies = fetchMock.mock.calls.map(entry =>
      JSON.parse((entry as unknown as [string, { body: string }])[1].body),
    );
    expect(bodies[0].videoBitrateKbps).toBeUndefined();
    expect(bodies[1].videoBitrateKbps).toBeCloseTo(40000, 0);
  });
});

describe('auto WebRTC quality fallback', () => {
  test('packetLossPctFor computes the loss of one sample window', () => {
    expect(
      packetLossPctFor(
        { packetsLost: 0, packetsReceived: 100 },
        { packetsLost: 10, packetsReceived: 190 },
      ),
    ).toBe(10);
    expect(
      packetLossPctFor(
        { packetsLost: 5, packetsReceived: 100 },
        { packetsLost: 5, packetsReceived: 100 },
      ),
    ).toBeNull();
  });

  test('chooseEncodedWebRTCVariant skips passthrough/source rows', () => {
    expect(
      chooseEncodedWebRTCVariant([
        { index: 0, name: 'Passthrough' },
        { index: 1, name: '6Mbps@60fps' },
      ])?.index,
    ).toBe(1);
    expect(chooseEncodedWebRTCVariant([{ index: 0, name: 'Source copy' }])).toBeNull();
  });

  test('collectVideoInboundTotals aggregates inbound video counters', async () => {
    const inbound = report([
      { id: 'video', type: 'inbound-rtp', kind: 'video', packetsLost: 4, packetsReceived: 20 },
      { id: 'audio', type: 'inbound-rtp', kind: 'audio', packetsLost: 99, packetsReceived: 99 },
    ]);
    const totals = await collectVideoInboundTotals({} as RTCPeerConnection, async () => inbound);
    expect(totals).toEqual({ packetsLost: 4, packetsReceived: 20 });
  });
});
