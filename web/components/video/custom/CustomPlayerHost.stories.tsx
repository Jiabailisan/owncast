import { Meta, StoryObj } from '@storybook/nextjs';

import { CustomPlayerHost } from './CustomPlayerHost';
import { AutoplaySetting } from '../../../utils/autoplay';

// Storybook cannot load /custom-player/player.js (that is served by the Go
// backend only when configured), so the story injects a compatible module.
const fakeLoader = async () => ({
  default: (el: HTMLElement) => {
    el.replaceChildren();
    const frame = document.createElement('div');
    frame.style.cssText =
      'display:flex;align-items:center;justify-content:center;width:100%;height:100%;background:#20242c;color:#fff;font-size:20px;';
    frame.textContent = 'Custom player module mounted';
    el.appendChild(frame);
    return { destroy: () => frame.remove() };
  },
});

const meta = {
  title: 'owncast/Player/Custom Player Host',
  component: CustomPlayerHost,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof CustomPlayerHost>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Mounted: Story = {
  args: {
    playerUrl: '/custom-player/player.js',
    hlsUrl: '/hls/stream.m3u8',
    whepUrl: '/webrtc/whep',
    variants: [{ index: 0, name: '720p', width: 1280, height: 720 }],
    online: true,
    autoplay: AutoplaySetting.Off,
    title: 'Custom player module',
    loader: fakeLoader,
    onFallback: () => {},
  },
};

export const LoadFailure: Story = {
  args: {
    playerUrl: '/custom-player/player.js',
    hlsUrl: '/hls/stream.m3u8',
    online: true,
    autoplay: AutoplaySetting.Off,
    loader: async () => {
      throw new Error('Failed to fetch dynamically imported module');
    },
    onFallback: () => {},
  },
};
