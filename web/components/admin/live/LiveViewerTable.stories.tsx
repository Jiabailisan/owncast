import { Meta, StoryObj } from '@storybook/nextjs';
import { http, HttpResponse } from 'msw';

import { LiveViewerTable } from './LiveViewerTable';
import { viewersResponse } from '../../../stories/admin-pages/liveStreamingFixtures';

const meta = {
  title: 'owncast/Admin/Live Status/LiveViewerTable',
  component: LiveViewerTable,
  parameters: {
    layout: 'padded',
    msw: {
      handlers: [http.get('*/api/admin/live/viewers*', () => HttpResponse.json(viewersResponse))],
    },
  },
} satisfies Meta<typeof LiveViewerTable>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = { args: { enabled: true } };

export const NoViewers: Story = {
  args: { enabled: true },
  parameters: {
    msw: {
      handlers: [
        http.get('*/api/admin/live/viewers*', () =>
          HttpResponse.json({ viewers: [], total: 0, page: 1, pageSize: 25 }),
        ),
      ],
    },
  },
};

export const LoadError: Story = {
  args: { enabled: true },
  parameters: {
    msw: {
      handlers: [
        http.get('*/api/admin/live/viewers*', () =>
          HttpResponse.json({ message: 'server unavailable' }, { status: 500 }),
        ),
      ],
    },
  },
};
