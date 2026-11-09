import { Meta, StoryObj } from '@storybook/nextjs';
import { PlayerControls } from './PlayerControls';
import { makeEmptyStats } from './types';

const callbacks = {
  onTogglePlay: () => {},
  onToggleMute: () => {},
  onVolumeChange: () => {},
  onGoLive: () => {},
  onTransportChange: () => {},
  onQualityChange: () => {},
  onToggleLatency: () => {},
  onToggleStats: () => {},
  onTogglePip: () => {},
  onToggleFullscreen: () => {},
};

const stats = {
  ...makeEmptyStats(false),
  collecting: false,
  streamId: 'hls/stream.m3u8',
  viewport: '1440x900 / 2 dropped of 15324',
  frames: '1440x900 / 2 dropped of 15324',
  resolution: '1280x720',
  optimalResolution: '1280x720',
  volume: '80% / 100%',
  normalizedVolume: '100%',
  codecs: 'H.264 / AAC (HLS)',
  videoBitrateKbps: 2450,
  audioBitrateKbps: 128,
  connectionSpeedKbps: 2600,
  rttMs: 12,
  jitterMs: 1.2,
  packetsLost: 3,
  packetsReceived: 9987,
  bufferSeconds: 3.4,
  liveLatencySeconds: 4.2,
  liveMode: 'HLS (Auto)',
  date: '11/10/2026, 12:00:00 AM',
};

const baseArgs = {
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
  behindLiveSeconds: null,
  stats,
  sparklines: {
    video: [1800, 2100, 1900, 2400, 2450],
    audio: [128, 128, 127, 128, 128],
  },
  statsOpen: false,
  callbacks,
};

const PlayerFrame = ({ children }: { children: React.ReactNode }) => (
  <div
    style={{
      position: 'relative',
      width: '100%',
      maxWidth: 1120,
      aspectRatio: '16 / 9',
      background: 'linear-gradient(135deg, #1d4ed8 0%, #0f766e 35%, #7c3aed 65%, #be123c 100%)',
    }}
  >
    {children}
  </div>
);

const meta = {
  title: 'owncast/Player/Player controls',
  component: PlayerControls,
  parameters: { layout: 'padded' },
  decorators: [
    (Story: React.FC) => (
      <PlayerFrame>
        <Story />
      </PlayerFrame>
    ),
  ],
} satisfies Meta<typeof PlayerControls>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = { args: baseArgs };

export const Paused: Story = {
  args: { ...baseArgs, playing: false },
};

export const BehindLive: Story = {
  args: { ...baseArgs, liveEdge: false, behindLiveSeconds: 12 },
};

export const SettingsOpen: Story = {
  args: { ...baseArgs, initialMenu: 'settings' },
};

export const TransportMenuOpen: Story = {
  args: { ...baseArgs, initialMenu: 'transport' },
};

export const StatsOpen: Story = {
  args: { ...baseArgs, statsOpen: true },
};

export const WebRTCActive: Story = {
  args: {
    ...baseArgs,
    transport: 'webrtc',
    activeTransport: 'webrtc',
    showQuality: false,
    note: 'WebRTC (auto)',
    stats: { ...stats, isWebRTC: true, codecs: 'H.264 / OPUS (WebRTC)', liveMode: 'WebRTC (auto)' },
  },
};

export const Mobile: Story = {
  args: baseArgs,
  parameters: { viewport: { defaultViewport: 'mobile1' } },
};

export const CollectingStats: Story = {
  args: { ...baseArgs, statsOpen: true, stats: makeEmptyStats(false) },
};
