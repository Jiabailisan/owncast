import React from 'react';
import { Meta, StoryObj } from '@storybook/nextjs';
import { Provider, createStore } from 'jotai';

import { PlayerContainer } from './PlayerContainer';
import { clientConfigStateAtom } from '../stores/ClientConfigStore';
import { makeEmptyClientConfig } from '../../interfaces/client-config.model';
import { WHEPSessionHandle } from '../../services/whep-client';
import { WHEPError } from '../../services/whep-client';

const DEMO_HLS = 'https://watch.owncast.online/hls/stream.m3u8';

function withStore(config: ReturnType<typeof makeEmptyClientConfig>, children: React.ReactNode) {
  const store = createStore();
  store.set(clientConfigStateAtom, config);
  return <Provider store={store}>{children}</Provider>;
}

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
    context.fillText('WebRTC container story', 40, 190);
  }
  return {
    pc: { getStats: async () => new Map() } as unknown as RTCPeerConnection,
    stream: canvas.captureStream ? canvas.captureStream(5) : new MediaStream(),
    sessionUrl: '/webrtc/whep/session/story',
    variantIndex: 0,
    iceServersFromLink: [],
    close: () => {},
    deleteSession: async () => {},
  };
}

const webRTCEnabledConfig = {
  ...makeEmptyClientConfig(),
  webrtc: {
    enabled: true,
    whepUrl: '/webrtc/whep',
    variants: [{ index: 0, name: '720p', width: 1280, height: 720 }],
    iceServers: [],
    fallbackToHls: true,
  },
};

const meta = {
  title: 'owncast/Player/PlayerContainer',
  component: PlayerContainer,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof PlayerContainer>;

export default meta;
type Story = StoryObj<typeof meta>;

export const HLSOnly: Story = {
  render: args => withStore(makeEmptyClientConfig(), <PlayerContainer {...args} />),
  args: {
    source: DEMO_HLS,
    online: false,
    title: 'HLS only',
  },
};

export const WebRTCPlaying: Story = {
  render: args => withStore(webRTCEnabledConfig, <PlayerContainer {...args} />),
  args: {
    source: DEMO_HLS,
    online: true,
    title: 'WebRTC playback',
    webRTCSessionFactory: async () => makeFakeSession(),
  },
};

export const FallbackToHLS: Story = {
  render: args => withStore(webRTCEnabledConfig, <PlayerContainer {...args} />),
  args: {
    source: DEMO_HLS,
    online: true,
    title: 'WebRTC fallback',
    webRTCSessionFactory: async () => {
      throw new WHEPError('capacity', 'WebRTC capacity is full');
    },
  },
};
