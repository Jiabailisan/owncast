import { Meta, StoryObj } from '@storybook/nextjs';
import { StreamStatusCards } from './StreamStatusCards';
import { liveStatus, liveStatusDetailed } from '../../../stories/admin-pages/liveStreamingFixtures';

const meta = {
  title: 'owncast/Admin/Live Status/StreamStatusCards',
  component: StreamStatusCards,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof StreamStatusCards>;

export default meta;
type Story = StoryObj<typeof meta>;

// SRT: AAC profile/sample-rate/channels details and the dual GeoIP databases.
export const SrtLive: Story = { args: { status: liveStatusDetailed as any } };

// RTMP omits the additive audio-detail fields, so the audio card shows the codec
// exactly as before (no sample rate/channel suffix).
export const RtmpWithoutAudioDetails: Story = {
  args: {
    status: {
      ...(liveStatus as any),
      inputProtocol: 'rtmp',
      source: {
        width: 1920,
        height: 1080,
        fps: 30,
        videoCodec: 'h264',
        videoProfile: 'high',
        audioCodec: 'aac',
        videoBitrateKbps: 2500,
        audioBitrateKbps: 128,
        hasBFrames: false,
      },
      ingest: { ...(liveStatus as any).ingest, packetLossPct: null, rttMs: null },
    },
  },
};

// GeoIP dual-database card on its own (primary MaxMind city + secondary fill-in).
export const GeoIpDualSources: Story = {
  args: {
    status: {
      ...(liveStatus as any),
      geoip: (liveStatusDetailed as any).geoip,
    },
  },
};

export const Offline: Story = {
  args: {
    status: {
      ...(liveStatus as any),
      state: 'offline',
      inputProtocol: '',
      startTime: null,
      uptimeSeconds: 0,
      source: {
        width: null,
        height: null,
        fps: null,
        videoCodec: '',
        videoProfile: '',
        audioCodec: '',
        videoBitrateKbps: null,
        audioBitrateKbps: null,
      },
      ingest: {
        bitrateKbps: null,
        packetLossPct: null,
        rttMs: null,
        retransmittedPackets: null,
        droppedPackets: null,
        detail: {},
      },
      viewers: { total: 0, webrtc: 0, hls: 0 },
      ffmpeg: { processModel: '', processes: [] },
      warnings: [{ code: 'copy_fallback', message: 'WebRTC copy falling back to transcode' }],
    },
  },
};
