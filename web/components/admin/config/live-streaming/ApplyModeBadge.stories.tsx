import { Meta, StoryObj } from '@storybook/nextjs';
import { ApplyModeBadge } from './ApplyModeBadge';

const meta = {
  title: 'owncast/Admin/Live Streaming/ApplyModeBadge',
  component: ApplyModeBadge,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof ApplyModeBadge>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AppliesLive: Story = { args: { applyMode: 'live' } };
export const RestartService: Story = { args: { applyMode: 'restart-service' } };
export const RestartStream: Story = { args: { applyMode: 'restart-stream' } };
export const RestartServer: Story = { args: { applyMode: 'restart-server' } };
export const PendingRestart: Story = {
  args: { applyMode: 'restart-service', pending: true },
};
