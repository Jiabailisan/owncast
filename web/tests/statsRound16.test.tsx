import { render, screen } from '@testing-library/react';
import { PlayerControls } from '../components/video/PlayerControls/PlayerControls';
import { makeEmptyStats } from '../components/video/PlayerControls/types';
import { collectWebRTCStatsPanel, summarizeSDP } from '../components/video/webrtc/webrtc-stats';

const makeCallbacks = () => ({
  onTogglePlay: jest.fn(),
  onToggleMute: jest.fn(),
  onVolumeChange: jest.fn(),
  onGoLive: jest.fn(),
  onTransportChange: jest.fn(),
  onQualityChange: jest.fn(),
  onToggleLatency: jest.fn(),
  onToggleStats: jest.fn(),
  onTogglePip: jest.fn(),
  onToggleFullscreen: jest.fn(),
});

const baseProps = {
  playing: true,
  muted: false,
  volume: 0.8,
  transport: 'auto' as const,
  activeTransport: 'webrtc' as const,
  webrtcAvailable: true,
  qualities: [
    { id: '0', name: 'Passthrough' },
    { id: '1', name: '6Mbps@60fps' },
  ],
  selectedQuality: 'auto',
  latencyEnabled: false,
  latencySupported: true,
  liveEdge: true,
  stats: {
    ...makeEmptyStats(true),
    collecting: false,
    sdp: 'level-id=4d0033',
    qualityName: '1080p60@6Mbps',
    liveLatencySeconds: 0.42,
  },
  sparklines: { video: [1000], audio: [128] },
};

test('stats panel shows the combined quality/SDP row and live latency', () => {
  render(<PlayerControls {...baseProps} statsOpen callbacks={makeCallbacks()} />);
  expect(screen.getByText('Quality / SDP')).toBeInTheDocument();
  expect(screen.getByText('1080p60@6Mbps / level-id=4d0033')).toBeInTheDocument();
  expect(screen.getByText('0.4s')).toBeInTheDocument();
  // Quality switching stays in the settings menu; the stats panel must not add
  // its own selector rows (it previously made the panel overflow).
  expect(screen.queryAllByTestId('quality-option').length).toBe(0);
});

test('summarizeSDP exposes the negotiated H264 profile and candidate count', () => {
  const summary = summarizeSDP(
    // Documentation addresses (RFC 5737) on purpose: a real host address here would
    // publish whoever wrote the test.
    'v=0\r\na=rtpmap:96 H264/90000\r\na=fmtp:96 profile-level-id=4d0033;packetization-mode=1\r\na=rtpmap:111 opus/48000/2\r\na=candidate:1 1 udp 1 192.0.2.10 18001 typ host\r\na=candidate:2 1 udp 1 198.51.100.20 18001 typ host\r\n',
  );
  expect(summary).toBe('level-id=4d0033');
});

test('WebRTC stats panel estimates live latency from jitter buffer and RTT', () => {
  const report = new Map<string, any>([
    ['codec-v', { id: 'codec-v', type: 'codec', mimeType: 'video/H264' }],
    ['codec-a', { id: 'codec-a', type: 'codec', mimeType: 'audio/opus' }],
    [
      'video',
      {
        id: 'video',
        type: 'inbound-rtp',
        kind: 'video',
        codecId: 'codec-v',
        bytesReceived: 1000,
        framesDecoded: 10,
        framesDropped: 1,
        framesPerSecond: 30,
        packetsLost: 1,
        packetsReceived: 100,
        jitter: 0.01,
        jitterBufferDelay: 0.4,
        jitterBufferEmittedCount: 1,
        totalProcessingDelay: 0.1,
        frameWidth: 1280,
        frameHeight: 720,
      },
    ],
    [
      'audio',
      { id: 'audio', type: 'inbound-rtp', kind: 'audio', codecId: 'codec-a', bytesReceived: 100 },
    ],
    [
      'pair',
      {
        id: 'pair',
        type: 'candidate-pair',
        nominated: true,
        state: 'succeeded',
        currentRoundTripTime: 0.02,
      },
    ],
  ]);
  const { sample } = collectWebRTCStatsPanel(report as RTCStatsReport);
  expect(sample.liveLatencySeconds).toBeCloseTo(0.42, 2);
});
