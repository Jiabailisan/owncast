import { Meta, StoryObj } from '@storybook/nextjs';

import { CustomPlayerConfig } from './CustomPlayerConfig';
import { ServerStatusContext } from '../../../../utils/server-status-context';
import { AppearanceAdminReport } from '../../../../interfaces/appearance.model';

const baseReport: AppearanceAdminReport = {
  activeFrontend: 'default',
  customPlayerPath: '',
  customFrontendDir: '',
  customPlayerValid: false,
  customFrontendValid: false,
};

const makeContext = (report: AppearanceAdminReport) =>
  ({
    serverConfig: { appearance: report },
    setFieldInConfigState: () => {},
  }) as any;

const meta = {
  title: 'owncast/Admin/Appearance/Custom Player & Frontend',
  component: CustomPlayerConfig,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof CustomPlayerConfig>;

export default meta;
type Story = StoryObj<typeof meta>;

const renderWithReport = (report: AppearanceAdminReport) => () => (
  <ServerStatusContext.Provider value={makeContext(report)}>
    <CustomPlayerConfig />
  </ServerStatusContext.Provider>
);

export const DefaultViewer: Story = { render: renderWithReport(baseReport) };

export const CustomPlayerActive: Story = {
  render: renderWithReport({
    ...baseReport,
    activeFrontend: 'custom-player',
    customPlayerPath: '/opt/owncast/player.js',
    customPlayerValid: true,
  }),
};

export const CustomFrontendActive: Story = {
  render: renderWithReport({
    ...baseReport,
    activeFrontend: 'custom-frontend',
    customFrontendDir: '/opt/owncast/frontend',
    customFrontendValid: true,
  }),
};

export const InvalidPlayerPath: Story = {
  render: renderWithReport({
    ...baseReport,
    customPlayerPath: '/does/not/exist.js',
    customPlayerError: 'custom player path does not exist or is not readable',
  }),
};

export const LoadErrorReported: Story = {
  render: renderWithReport({
    ...baseReport,
    activeFrontend: 'custom-player',
    customPlayerPath: '/opt/owncast/broken.js',
    customPlayerValid: true,
    customPlayerLoadError: {
      message: 'Failed to fetch dynamically imported module',
      at: '2026-11-09T04:00:00Z',
    },
  }),
};
