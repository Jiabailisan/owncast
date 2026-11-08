import { Meta, StoryObj } from '@storybook/nextjs';
import { WebRTCPlayer } from './WebRTCPlayer';
import { WHEPSessionHandle, WHEPError } from '../../../services/whep-client';

// A canvas-backed MediaStream lets the story show a real <video> playing
// without a backend; production never uses this factory.
function makeFakeSession(): WHEPSessionHandle {
  const canvas = document.createElement('canvas');
  canvas.width = 640;
  canvas.height = 360;
  const context = canvas.getContext('2d');
  if (context) {
    context.fillStyle = '#20242c';
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.fillStyle = '#fff';
    context.font = '24px sans-serif';
    context.fillText('WebRTC story stream', 40, 190);
  }
  const stream = canvas.captureStream ? canvas.captureStream(5) : new MediaStream();
  const fakePc = { getStats: async () => new Map() } as unknown as RTCPeerConnection;
  return {
    pc: fakePc,
    stream,
    sessionUrl: '/webrtc/whep/session/story',
    variantIndex: 0,
    iceServersFromLink: [],
    close: () => {},
    deleteSession: async () => {},
  };
}

const meta = {
  title: 'owncast/Player/WebRTC',
  component: WebRTCPlayer,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof WebRTCPlayer>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Connecting: Story = {
  args: {
    whepUrl: '/webrtc/whep',
    online: true,
    title: 'Connecting example',
    sessionFactory: () => new Promise<WHEPSessionHandle>(() => {}),
  },
};

export const Playing: Story = {
  args: {
    whepUrl: '/webrtc/whep',
    online: true,
    title: 'WebRTC playback example',
    variants: [
      { index: 0, name: '720p', width: 1280, height: 720 },
      { index: 1, name: '360p', width: 640, height: 360 },
    ],
    sessionFactory: async () => makeFakeSession(),
    enableStats: false,
  },
};

export const CapacityFallback: Story = {
  args: {
    whepUrl: '/webrtc/whep',
    online: true,
    title: 'Capacity rejection example',
    sessionFactory: async () => {
      throw new WHEPError('capacity', 'WebRTC capacity is full');
    },
  },
};

export const AutoplayMuted: Story = {
  args: {
    whepUrl: '/webrtc/whep',
    online: true,
    title: 'Autoplay muted example',
    autoplay: 'always',
    sessionFactory: async () => makeFakeSession(),
    enableStats: false,
  },
};
