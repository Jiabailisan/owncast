import { Meta, StoryObj } from '@storybook/nextjs';
import { http, HttpResponse } from 'msw';

import { LiveStreamStatusSection } from './LiveStreamStatusSection';
import {
  ffmpegPanelResponse,
  liveStatus,
  viewersResponse,
} from '../../../../stories/admin-pages/liveStreamingFixtures';

const liveHandlers = [
  http.get('*/api/admin/live/status', () => HttpResponse.json(liveStatus)),
  http.get('*/api/admin/live/viewers*', () => HttpResponse.json(viewersResponse)),
  http.get('*/api/admin/live/ffmpeg', () => HttpResponse.json(ffmpegPanelResponse)),
];

const meta = {
  title: 'owncast/Admin/Live Streaming/LiveStreamStatusSection',
  component: LiveStreamStatusSection,
  parameters: {
    layout: 'padded',
    msw: { handlers: liveHandlers },
  },
} satisfies Meta<typeof LiveStreamStatusSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
