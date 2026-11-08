import { Meta, StoryObj } from '@storybook/nextjs';
import { LiveStateBadge } from './LiveStateBadge';

const meta = {
  title: 'owncast/Admin/Live Status/LiveStateBadge',
  component: LiveStateBadge,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof LiveStateBadge>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Live: Story = { args: { state: 'live' } };
export const Offline: Story = { args: { state: 'offline' } };
export const Waiting: Story = { args: { state: 'waiting' } };
export const Degraded: Story = { args: { state: 'degraded' } };
export const ErrorState: Story = { args: { state: 'error' } };
