import { Meta, StoryObj } from '@storybook/nextjs';

import { VariantOutputSettings } from './VariantOutputSettings';

const meta = {
  title: 'owncast/Admin/Variant output settings',
  component: VariantOutputSettings,
  parameters: { layout: 'padded' },
  args: {
    onChange: () => {},
  },
} satisfies Meta<typeof VariantOutputSettings>;

export default meta;
type Story = StoryObj<typeof meta>;

export const LegacyVariant: Story = { args: { value: undefined } };

export const WebRTCTranscode: Story = {
  args: {
    value: {
      webrtc: { enabled: true, mode: 'transcode' },
    },
  },
};

export const WebRTCCopyWithFallback: Story = {
  args: {
    value: {
      hls: { enabled: true },
      webrtc: { enabled: true, mode: 'copy', copyFallback: 'transcode' },
    },
  },
};

export const WebRTCDisabled: Story = {
  args: {
    value: {
      webrtc: { enabled: false, mode: 'transcode' },
    },
  },
};
