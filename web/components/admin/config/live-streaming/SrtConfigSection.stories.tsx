import { Meta, StoryObj } from '@storybook/nextjs';
import { http, HttpResponse } from 'msw';

import { SrtConfigSection } from './SrtConfigSection';
import {
  srtConfigResponse,
  settingsMetadata,
  webrtcConfigResponse,
} from '../../../../stories/admin-pages/liveStreamingFixtures';

const settingsHandlers = [
  http.get('*/api/admin/config/settings-metadata', () => HttpResponse.json(settingsMetadata)),
  http.get('*/api/admin/config/srt', () => HttpResponse.json(srtConfigResponse)),
  http.get('*/api/admin/config/webrtc', () => HttpResponse.json(webrtcConfigResponse)),
];

const meta = {
  title: 'owncast/Admin/Live Streaming/SrtConfigSection',
  component: SrtConfigSection,
  parameters: {
    layout: 'padded',
    msw: { handlers: settingsHandlers },
  },
} satisfies Meta<typeof SrtConfigSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = { args: { metadata: settingsMetadata as any } };