import { Meta, StoryObj } from '@storybook/nextjs';
import { OutboundStreamsTable } from './OutboundStreamsTable';
import { liveStatusDetailed } from '../../../stories/admin-pages/liveStreamingFixtures';

const streams = (liveStatusDetailed as any).outbound.streams;

const meta = {
  title: 'owncast/Admin/Live Status/OutboundStreamsTable',
  component: OutboundStreamsTable,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof OutboundStreamsTable>;

export default meta;
type Story = StoryObj<typeof meta>;

export const TwoVariants: Story = { args: { streams } };

// A passthrough variant has no planned bitrate: the table renders N/A, never 0.
export const PassThroughWithUnknownBitrates: Story = {
  args: {
    streams: streams.filter((stream: any) => stream.videoEncoder === 'copy'),
  },
};

export const Empty: Story = { args: { streams: [] } };
