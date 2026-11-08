import { Meta, StoryObj } from '@storybook/nextjs';
import { ProtocolBadge } from './ProtocolBadge';

const meta = {
  title: 'owncast/Admin/Live Status/ProtocolBadge',
  component: ProtocolBadge,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof ProtocolBadge>;

export default meta;
type Story = StoryObj<typeof meta>;

export const RTMP: Story = { args: { protocol: 'rtmp' } };
export const SRT: Story = { args: { protocol: 'srt' } };
export const Offline: Story = { args: { protocol: '' } };
