import { Meta, StoryObj } from '@storybook/nextjs';
import { MetricValue } from './MetricValue';

const meta = {
  title: 'owncast/Admin/Live Status/MetricValue',
  component: MetricValue,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof MetricValue>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Available: Story = { args: { value: '2,450 kbps' } };
export const Unavailable: Story = {
  args: { value: null, tooltip: 'The source does not report this value.' },
};
