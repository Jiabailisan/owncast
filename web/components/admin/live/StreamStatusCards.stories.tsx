import { Meta, StoryObj } from '@storybook/nextjs';
import { StreamStatusCards } from './StreamStatusCards';
import { liveStatus } from '../../../stories/admin-pages/liveStreamingFixtures';

const meta = {
  title: 'owncast/Admin/Live Status/StreamStatusCards',
  component: StreamStatusCards,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof StreamStatusCards>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SrtLive: Story = { args: { status: liveStatus as any } };

export const RtmpWithTcpUnmeasurable: Story = {
  args: {
    status: {
      ...(liveStatus as any),
      inputProtocol: 'rtmp',
      ingest: { ...(liveStatus as any).ingest, packetLossPct: null, rttMs: null },
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
