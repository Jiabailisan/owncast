import { Meta, StoryObj } from '@storybook/nextjs';
import { FfmpegPanel } from './FfmpegPanel';
import { ffmpegPanelResponse } from '../../../stories/admin-pages/liveStreamingFixtures';

const meta = {
  title: 'owncast/Admin/Live Status/FfmpegPanel',
  component: FfmpegPanel,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof FfmpegPanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SharedModel: Story = { args: { panel: ffmpegPanelResponse as any } };

export const IsolatedModel: Story = {
  args: {
    panel: {
      processModel: 'isolated',
      processes: [
        ...(ffmpegPanelResponse as any).processes,
        {
          ...(ffmpegPanelResponse as any).processes[0],
          id: 'webrtc',
          purpose: ['webrtc'],
          pid: 4243,
        },
      ],
    } as any,
  },
};

export const Empty: Story = { args: { panel: { processModel: 'shared', processes: [] } } };
