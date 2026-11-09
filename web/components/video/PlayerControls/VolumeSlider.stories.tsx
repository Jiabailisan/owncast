import { Meta, StoryObj } from '@storybook/nextjs';
import { VolumeSlider } from './VolumeSlider';

const meta = {
  title: 'owncast/Player/Player controls/Volume slider',
  component: VolumeSlider,
  parameters: { layout: 'centered' },
  decorators: [
    (Story: React.FC) => (
      <div style={{ padding: 24, background: '#181818', borderRadius: 12 }}>
        <Story />
      </div>
    ),
  ],
} satisfies Meta<typeof VolumeSlider>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Zero: Story = { args: { value: 0, onChange: () => {} } };
export const Half: Story = { args: { value: 0.5, onChange: () => {} } };
export const Full: Story = { args: { value: 1, onChange: () => {} } };
