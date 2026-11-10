import { fireEvent, render, screen } from '@testing-library/react';

import { PlayerControls } from '../components/video/PlayerControls/PlayerControls';
import { makeEmptyStats } from '../components/video/PlayerControls/types';
import {
  buildStatsRows,
  pushSparkline,
  sparklinePoints,
  formatStatsBitrate,
  hlsConnectionSpeedKbps,
  hlsLiveMode,
  hlsMeasuredFps,
  hlsPlaylistFps,
  hlsResolution,
  hlsVideoBitrateKbps,
} from '../components/video/PlayerControls/stats';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

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

const makeProps = (overrides: Record<string, any> = {}) => ({
  playing: true,
  muted: false,
  volume: 0.8,
  transport: 'auto' as const,
  activeTransport: 'hls' as const,
  webrtcAvailable: true,
  qualities: [
    { id: '0', name: '720p' },
    { id: '1', name: '360p' },
  ],
  selectedQuality: 'auto',
  latencyEnabled: false,
  latencySupported: true,
  liveEdge: true,
  stats: makeEmptyStats(false),
  sparklines: { video: [1000, 1200], audio: [128, 130] },
  statsOpen: false,
  callbacks: makeCallbacks(),
  ...overrides,
});

describe('stats presentation helpers', () => {
  test('formats bitrates and N/A honestly', () => {
    expect(formatStatsBitrate(1200)).toBe('1.20 Mbps');
    expect(formatStatsBitrate(128)).toBe('128 kbps');
    expect(formatStatsBitrate(null)).toBe('—');
    expect(formatStatsBitrate(0)).toBe('—');
  });

  test('keeps a rolling sparkline window and builds SVG points', () => {
    let series: number[] = [];
    for (let i = 0; i < 65; i += 1) series = pushSparkline(series, i);
    expect(series.length).toBe(60);
    expect(series[series.length - 1]).toBe(64);
    expect(sparklinePoints([], 200, 30)).toBe('');
    expect(sparklinePoints([10, 20], 200, 30)).toContain(',');
  });

  test('builds the provided player row set', () => {
    const stats = {
      ...makeEmptyStats(false),
      collecting: false,
      frames: '1440x900 / 2 dropped of 1000',
      volume: '80% / 100%',
    };
    const rows = buildStatsRows(stats);
    expect(rows.map(row => row.label)).toEqual([
      'Stream / Video ID',
      'Viewport / Frames',
      'Current / Optimal Res',
      'Volume / Normalized',
      'Codecs',
      'Quality / SDP',
      'Video Bitrate',
      'Audio Bitrate',
      'Connection Speed',
      'RTT / Jitter',
      'Packet Loss (video)',
      'Buffer Health',
      'Live Latency',
      'Live Mode',
      'Date',
    ]);
  });
});

describe('PlayerControls overlay', () => {
  test('renders the control bar with transport pill, live state and settings', () => {
    const callbacks = makeCallbacks();
    render(<PlayerControls {...makeProps({ callbacks })} />);
    expect(screen.getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Playback transport' })).toHaveTextContent('Auto');
    expect(screen.getByRole('button', { name: 'Live' })).toHaveTextContent('LIVE');
    expect(screen.getByRole('button', { name: 'Settings' })).toBeInTheDocument();
    // The volume slider appears when the volume group is hovered/focused.
    const volumeGroup = screen.getByRole('button', { name: 'Mute' }).parentElement as Element;
    fireEvent.mouseEnter(volumeGroup);
    expect(screen.getByLabelText('Volume')).toBeInTheDocument();
    // The volume pill is one continuous control: no caret arrow, a custom
    // div-based slider (not a native input) lives inside the same pill.
    expect(volumeGroup.querySelector('.anticon-down')).toBeNull();
    expect(volumeGroup.querySelector('input[type="range"]')).toBeNull();
    const slider = screen.getByLabelText('Volume');
    expect(slider).toHaveAttribute('role', 'slider');
    expect(slider).toHaveAttribute('aria-valuenow', '80');
    expect(volumeGroup.contains(slider)).toBe(true);

    fireEvent.keyDown(slider, { key: 'ArrowRight' });
    expect(callbacks.onVolumeChange).toHaveBeenCalledWith(0.8500000000000001);
    fireEvent.keyDown(slider, { key: 'Home' });
    expect(callbacks.onVolumeChange).toHaveBeenCalledWith(0);
  });

  test('routes play/pause, mute and live-edge actions to the callbacks', () => {
    const callbacks = makeCallbacks();
    render(
      <PlayerControls
        {...makeProps({ playing: false, liveEdge: false, behindLiveSeconds: 12, callbacks })}
      />,
    );
    const playButtons = screen.getAllByRole('button', { name: 'Play' });
    fireEvent.click(playButtons[playButtons.length - 1]);
    fireEvent.click(screen.getByRole('button', { name: 'Mute' }));
    fireEvent.click(screen.getByRole('button', { name: 'Seek to live' }));
    expect(callbacks.onTogglePlay).toHaveBeenCalled();
    expect(callbacks.onToggleMute).toHaveBeenCalled();
    expect(callbacks.onGoLive).toHaveBeenCalled();
  });

  test('PiP uses the provided inline SVG icon', () => {
    Object.defineProperty(document, 'pictureInPictureEnabled', {
      configurable: true,
      value: true,
    });
    render(<PlayerControls {...makeProps()} />);
    const pip = screen.getByRole('button', { name: 'Picture in picture' });
    const path = pip.querySelector('svg path');
    expect(path).toHaveAttribute('d', 'M4 5h16v14H4V5zm2 2v10h12V7H6zm6 4h6v5h-6v-5z');
    delete (document as unknown as { pictureInPictureEnabled?: boolean }).pictureInPictureEnabled;
  });

  test('opens the transport menu and disables WebRTC when unavailable', () => {
    const callbacks = makeCallbacks();
    render(<PlayerControls {...makeProps({ webrtcAvailable: false, callbacks })} />);
    fireEvent.click(screen.getByRole('button', { name: 'Playback transport' }));
    expect(screen.getByRole('button', { name: 'HLS' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'WebRTC' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'HLS' }));
    expect(callbacks.onTransportChange).toHaveBeenCalledWith('hls');
  });

  test('opens the settings menu and toggles quality/latency/stats', () => {
    const callbacks = makeCallbacks();
    render(<PlayerControls {...makeProps({ callbacks })} />);
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
    expect(screen.getByText('QUALITY')).toBeInTheDocument();
    expect(screen.getByText('Stats for nerds')).toBeInTheDocument();
    expect(screen.getByText('Minimize latency')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '720p' }));
    expect(callbacks.onQualityChange).toHaveBeenCalledWith('0');
    fireEvent.click(screen.getByText('Minimize latency'));
    expect(callbacks.onToggleLatency).toHaveBeenCalled();
    fireEvent.click(screen.getByText('Stats for nerds'));
    expect(callbacks.onToggleStats).toHaveBeenCalled();
  });

  test('renders the stats panel rows and sparklines when open', () => {
    const stats = {
      ...makeEmptyStats(false),
      collecting: false,
      streamId: 'hls/stream.m3u8',
      videoBitrateKbps: 1200,
      audioBitrateKbps: 128,
      packetsLost: 2,
      packetsReceived: 998,
      bufferSeconds: 3.2,
      liveLatencySeconds: 4.5,
    };
    render(<PlayerControls {...makeProps({ statsOpen: true, stats })} />);
    expect(screen.getByRole('dialog', { name: 'Stats for nerds' })).toBeInTheDocument();
    expect(screen.getByText('hls/stream.m3u8')).toBeInTheDocument();
    expect(screen.getAllByText('1.20 Mbps').length).toBeGreaterThan(0);
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
    expect(screen.getAllByLabelText(/bitrate chart/).length).toBe(2);
    fireEvent.click(screen.getByRole('button', { name: 'Close stats' }));
    expect(makeProps().callbacks.onToggleStats).toBeDefined();
  });

  test('shows Collecting… before the first stats sample', () => {
    render(<PlayerControls {...makeProps({ statsOpen: true })} />);
    expect(screen.getByText('Collecting...')).toBeInTheDocument();
  });
});

describe('HLS transport metering helpers', () => {
  test('video bitrate prefers decoded bytes, then segment bytes/seconds, then playlist bandwidth', () => {
    expect(
      hlsVideoBitrateKbps({ mediaBytesTransferred: 8_000_000, mediaSecondsLoaded: 60 }),
    ).toBeCloseTo(1066.67, 1);
    expect(
      hlsVideoBitrateKbps({
        mediaBytesTransferred: 8_000_000,
        mediaSecondsLoaded: 60,
        decodedBitsPerSecond: 1_090_000,
      }),
    ).toBeCloseTo(1090, 3);
    expect(hlsVideoBitrateKbps({ representationBandwidthBps: 1_200_000 })).toBeCloseTo(1200, 3);
    expect(hlsVideoBitrateKbps({})).toBeNull();
  });

  test('connection speed uses segment transfer duration and never drops below the stream bitrate', () => {
    expect(
      hlsConnectionSpeedKbps(
        { mediaBytesTransferred: 8_000_000, mediaTransferDurationMs: 7_400, audioBitrateKbps: 131 },
        1090,
      ),
    ).toBeCloseTo(8648.65, 0);
    expect(hlsConnectionSpeedKbps({ systemBandwidthBps: 12_000_000 }, 1090)).toBeCloseTo(12000, 0);
    expect(hlsConnectionSpeedKbps({}, 1090)).toBeCloseTo(1090, 0);
    expect(hlsConnectionSpeedKbps({}, null)).toBeNull();
  });

  test('fps comes from decoded frame deltas with a playlist FRAME-RATE fallback', () => {
    expect(hlsMeasuredFps(24, 1)).toBe(24);
    expect(hlsMeasuredFps(47, 2)).toBe(24);
    expect(hlsMeasuredFps(0, 1)).toBeNull();
    expect(hlsMeasuredFps(null, 1)).toBeNull();
    expect(hlsPlaylistFps({ 'FRAME-RATE': '24.000' })).toBe(24);
    expect(hlsPlaylistFps(null, '1.2Mbps@24fps')).toBe(24);
    expect(hlsPlaylistFps({}, 'Auto')).toBeNull();
  });

  test('resolution/optimal row and live mode use the reference formatting', () => {
    expect(hlsResolution(1280, 720, 24)).toBe('1280x720@24fps');
    expect(hlsResolution(1280, 720, null)).toBe('1280x720');
    expect(hlsResolution(0, 0, 24)).toBe('—');
    expect(hlsLiveMode('hls')).toBe('HLS (hls)');
    expect(hlsLiveMode(null)).toBe('HLS (hls)');
    expect(hlsLiveMode('transcode')).toBe('HLS (transcode)');
    const rows = buildStatsRows({
      ...makeEmptyStats(false),
      resolution: '1280x720@24fps',
      optimalResolution: '1280x720@24fps',
      audioBitrateKbps: 131,
      videoBitrateKbps: 1090,
      connectionSpeedKbps: 8710,
      liveMode: 'HLS (hls)',
    });
    const values = Object.fromEntries(rows.map(row => [row.label, row.value]));
    expect(values['Current / Optimal Res']).toBe('1280x720@24fps / 1280x720@24fps');
    expect(values['Audio Bitrate']).toBe('131 kbps (ingest)');
    expect(values['Video Bitrate']).toBe('1.09 Mbps');
    expect(values['Connection Speed']).toBe('8.71 Mbps');
    expect(values['Live Mode']).toBe('HLS (hls)');
    expect(values['RTT / Jitter']).toBe('—');
    expect(values['Packet Loss (video)']).toBe('—');
  });
});
