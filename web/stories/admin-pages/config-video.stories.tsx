import { Meta, StoryObj } from '@storybook/nextjs';
import { http, HttpResponse } from 'msw';

import ConfigVideo from '../../pages/admin/config-video';

// Render smoke for the admin Config Video page: a story that throws fails the
// Storybook build, catching page-level breakage on every PR. The output process
// model card loads through msw so the page renders its real controls.
const meta = {
  title: 'owncast/Admin/Pages/Config Video',
  component: ConfigVideo,
  parameters: {
    msw: {
      handlers: [
        http.get('*/api/admin/config/output', () =>
          HttpResponse.json({
            config: { processModel: 'shared' },
            defaults: { processModel: 'shared' },
            pendingRestart: { pending: false, fields: [] },
          }),
        ),
      ],
    },
  },
} satisfies Meta<typeof ConfigVideo>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
