import { Meta, StoryObj } from '@storybook/nextjs';
import { VideoHintAlert } from './VideoHintAlert';

const meta = {
  title: 'owncast/Admin/Video hints',
  component: VideoHintAlert,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof VideoHintAlert>;

export default meta;
type Story = StoryObj<typeof meta>;

// The pre-existing recommendation hint (message only).
export const Recommendation: Story = {
  args: {
    message:
      'You have only set one video quality variant. If your server has the computing resources, consider adding another, lower-quality variant, so more people can view your content!',
  },
};

// The WebRTC/copy hint (message + description): both lines share the same
// 14px/22px typography after the unification.
export const OutputMapping: Story = {
  args: {
    message:
      'When WebRTC delivery is enabled globally and no output variant has an explicit mapping, every HLS variant is exposed over WebRTC. As soon as any variant has an explicit mapping, only the enabled variants are delivered.',
    description:
      'Copy requires H.264 video without B-frames. SRT sources are inspected automatically; RTMP sources fall back to transcoding.',
  },
};

// Both hints stacked as they appear above the variants table.
export const BothHints: Story = {
  args: {
    message:
      'You have only set one video quality variant. If your server has the computing resources, consider adding another, lower-quality variant, so more people can view your content!',
    description:
      'Copy requires H.264 video without B-frames. SRT sources are inspected automatically; RTMP sources fall back to transcoding.',
  },
  render: () => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <VideoHintAlert {...Recommendation.args} />
      <VideoHintAlert {...OutputMapping.args} />
    </div>
  ),
};
