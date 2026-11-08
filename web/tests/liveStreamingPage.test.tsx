import { fireEvent, render, screen, waitFor } from '@testing-library/react';

import ConfigLiveStreaming from '../pages/admin/config/live-streaming';
import AdminSettingsService from '../services/admin-settings-service';
import LiveStatusService from '../services/live-status-service';
import { makeEmptyLiveStatus } from '../interfaces/live-status.model';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

jest.mock('next/router', () => ({
  useRouter: () => ({
    query: {},
    pathname: '/admin/config/live-streaming',
    asPath: '/admin/config/live-streaming',
    push: jest.fn(),
    replace: jest.fn(),
  }),
}));

jest.mock('../services/admin-settings-service', () => ({
  __esModule: true,
  default: {
    getSettingsMetadata: jest.fn(),
    getSRTConfig: jest.fn(),
    getWebRTCConfig: jest.fn(),
    saveSRTConfig: jest.fn(),
    saveWebRTCConfig: jest.fn(),
    runPreflight: jest.fn(),
    testWebRTCDomain: jest.fn(),
  },
}));

jest.mock('../services/live-status-service', () => ({
  __esModule: true,
  default: { getStatus: jest.fn(), getFfmpegPanel: jest.fn(), getViewers: jest.fn() },
  buildLiveViewersUrl: jest.fn(),
}));

const settingsService = AdminSettingsService as unknown as {
  getSettingsMetadata: jest.Mock;
  getSRTConfig: jest.Mock;
  getWebRTCConfig: jest.Mock;
};
const liveStatusService = LiveStatusService as unknown as {
  getStatus: jest.Mock;
  getFfmpegPanel: jest.Mock;
};

const metadata = {
  groups: [
    { id: 'srt.listener', label: 'Listener' },
    { id: 'webrtc.general', label: 'General' },
  ],
  srt: {
    fields: [
      {
        key: 'enabled',
        group: 'srt.listener',
        type: 'bool',
        default: false,
        description: 'Enable the SRT listener.',
        secret: false,
        applyMode: 'restart-service',
        readOnly: false,
      },
    ],
  },
  webrtc: {
    fields: [
      {
        key: 'enabled',
        group: 'webrtc.general',
        type: 'bool',
        default: false,
        description: 'Enable WebRTC delivery.',
        secret: false,
        applyMode: 'restart-service',
        readOnly: false,
      },
    ],
  },
};

const srtResponse = {
  config: { enabled: false },
  defaults: { enabled: false },
  pendingRestart: { pending: false, fields: [] },
  listener: {
    enabled: false,
    state: 'stopped',
    listening: false,
    port: 0,
    boundAddress: '',
    lastError: '',
    pendingRestart: false,
    activeSession: null,
  },
};

const webrtcResponse = {
  config: { enabled: false },
  defaults: { enabled: false },
  pendingRestart: { pending: false, fields: [] },
  service: {
    enabled: false,
    listeningAddress: '',
    activeSessions: 0,
    capacity: { used: 0, limit: 100 },
    warnings: [],
  },
};

describe('ConfigLiveStreaming page', () => {
  beforeEach(() => {
    settingsService.getSettingsMetadata.mockReset().mockResolvedValue(metadata);
    settingsService.getSRTConfig.mockReset().mockResolvedValue(srtResponse);
    settingsService.getWebRTCConfig.mockReset().mockResolvedValue(webrtcResponse);
    liveStatusService.getStatus.mockReset().mockResolvedValue(makeEmptyLiveStatus());
    liveStatusService.getFfmpegPanel
      .mockReset()
      .mockResolvedValue({ processModel: 'shared', processes: [] });
  });

  test('renders the page title and the three configuration tabs', async () => {
    render(<ConfigLiveStreaming />);
    expect(screen.getByText('Live Streaming')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('SRT Configuration')).toBeInTheDocument());
    expect(screen.getByText('WebRTC Configuration')).toBeInTheDocument();
    expect(screen.getByText('Live Stream Status')).toBeInTheDocument();
  });

  test('loads the SRT form from metadata and the SRT config endpoint', async () => {
    render(<ConfigLiveStreaming />);
    await waitFor(() => expect(screen.getByText('Enabled')).toBeInTheDocument());
    expect(settingsService.getSettingsMetadata).toHaveBeenCalledTimes(1);
    expect(settingsService.getSRTConfig).toHaveBeenCalledTimes(1);
  });

  test('switching to the WebRTC tab loads its config', async () => {
    render(<ConfigLiveStreaming />);
    fireEvent.click(await screen.findByText('WebRTC Configuration'));
    await waitFor(() => expect(settingsService.getWebRTCConfig).toHaveBeenCalled());
  });

  test('shows a retryable error when the settings metadata is unavailable', async () => {
    settingsService.getSettingsMetadata.mockRejectedValue(new Error('metadata unavailable'));
    const warnSpy = jest.spyOn(console, 'warn').mockImplementation(() => {});
    render(<ConfigLiveStreaming />);
    await waitFor(() => expect(screen.getByText('metadata unavailable')).toBeInTheDocument());
    expect(screen.getByText('Retry')).toBeInTheDocument();
    expect(warnSpy).toHaveBeenCalled();
    warnSpy.mockRestore();
  });
});
