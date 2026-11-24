import { Meta, StoryObj } from '@storybook/nextjs';
import { WebRTCOutputTable } from './WebRTCOutputTable';
import { liveStatusDetailed, webrtcLegs } from '../../../stories/admin-pages/liveStreamingFixtures';

const webrtc = (liveStatusDetailed as any).webrtc;

const meta = {
  title: 'owncast/Admin/Live Status/WebRTCOutputTable',
  component: WebRTCOutputTable,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof WebRTCOutputTable>;

export default meta;
type Story = StoryObj<typeof meta>;

// One transcode leg, one inherited copy leg and one explicit copy leg that fell
// back to a transcode because the source uses B-frames.
export const MixedLegs: Story = { args: { webrtc } };

// An "inherit" leg resolved against an HLS variant that is itself transcoded.
export const InheritedFromTranscodeHls: Story = {
  args: {
    webrtc: {
      ...webrtc,
      legs: [
        {
          ...webrtcLegs[1],
          requestedMode: 'inherit',
          inheritedFrom: 'hls-transcode',
          mode: 'transcode',
          videoBitrateKbps: 2500,
          gopFrames: 30,
          framerate: 30,
          h264Profile: 'high',
        },
      ],
    },
  },
};

// A withdrawn rendition: WebRTC delivery is not enabled for the variant, so it
// renders as Withdrawn with no encoder settings.
export const Withdrawn: Story = {
  args: {
    webrtc: {
      ...webrtc,
      legs: [
        {
          ...webrtcLegs[1],
          requestedMode: '',
          inheritedFrom: undefined,
          mode: 'disabled',
          outputBitrateKbps: null,
        },
      ],
    },
  },
};

// No legs at all: the component renders nothing rather than an empty table.
export const NoLegs: Story = { args: { webrtc: { ...webrtc, legs: [] } } };
