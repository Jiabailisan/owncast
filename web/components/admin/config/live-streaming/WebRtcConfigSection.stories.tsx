import { Meta, StoryObj } from '@storybook/nextjs';
import { http, HttpResponse } from 'msw';

import { WebRtcConfigSection } from './WebRtcConfigSection';
import {
  preflightResponse,
  srtConfigResponse,
  settingsMetadata,
  webrtcConfigResponse,
} from '../../../../stories/admin-pages/liveStreamingFixtures';

const settingsHandlers = [
  http.get('*/api/admin/config/settings-metadata', () => HttpResponse.json(settingsMetadata)),
  http.get('*/api/admin/config/srt', () => HttpResponse.json(srtConfigResponse)),
  http.get('*/api/admin/config/webrtc', () => HttpResponse.json(webrtcConfigResponse)),
  http.post('*/api/admin/config/preflight', () => HttpResponse.json(preflightResponse)),
  http.post('*/api/admin/config/webrtc/test-domain', () =>
    HttpResponse.json({
      domain: 'live.example.com',
      resolvedIPs: ['203.0.113.5'],
      advertisedIPs: ['203.0.113.5'],
      strictDomain: false,
      error: '',
    }),
  ),
];

const meta = {
  title: 'owncast/Admin/Live Streaming/WebRtcConfigSection',
  component: WebRtcConfigSection,
  parameters: {
    layout: 'padded',
    msw: { handlers: settingsHandlers },
  },
} satisfies Meta<typeof WebRtcConfigSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = { args: { metadata: settingsMetadata as any } };
