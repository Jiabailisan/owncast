import { fireEvent, render, screen, waitFor } from '@testing-library/react';

import { CustomPlayerConfig } from '../components/admin/config/general/CustomPlayerConfig';
import { ServerStatusContext } from '../utils/server-status-context';
import AppearanceService from '../services/appearance-service';
import { AppearanceAdminReport } from '../interfaces/appearance.model';

const text = {
  'Admin.Appearance.customPlayerTitle': 'Custom player and frontend',
  'Admin.Appearance.activeFrontendLabel': 'Active',
  'Admin.Appearance.activeFrontendDefault': 'Default viewer',
  'Admin.Appearance.activeFrontendCustomPlayer': 'Custom player',
  'Admin.Appearance.activeFrontendCustomFrontend': 'Custom frontend',
  'Admin.Appearance.ignoredCustomPlayer':
    'Ignored while a custom frontend directory is active.',
  'Admin.Appearance.customFrontendOverridesCss':
    'Custom CSS and Custom JavaScript are not served while a custom frontend is active.',
  'Admin.Appearance.customPlayerLoadErrorTitle': 'The custom player failed to load for some viewers',
  'Admin.Appearance.precedenceTitle': 'How these settings interact',
  'Admin.Appearance.saveButton': 'Save',
  'Admin.Appearance.savedMessage': 'Saved',
};

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({
    t: (key: string, vars?: Record<string, string>) => {
      if (key === 'Admin.Appearance.customPlayerLoadErrorDescription') {
        return 'Last reported ' + String(vars?.time) + ': ' + String(vars?.message);
      }
      return (text as Record<string, string>)[key] ?? key;
    },
  }),
}));

jest.mock('../services/appearance-service', () => ({
  __esModule: true,
  default: {
    getReport: jest.fn(),
    saveCustomPlayerPath: jest.fn(),
    saveCustomFrontendDir: jest.fn(),
  },
}));

const mockedService = AppearanceService as unknown as {
  getReport: jest.Mock;
  saveCustomPlayerPath: jest.Mock;
  saveCustomFrontendDir: jest.Mock;
};

const report: AppearanceAdminReport = {
  activeFrontend: 'default',
  customPlayerPath: '',
  customFrontendDir: '',
  customPlayerValid: false,
  customFrontendValid: false,
};

const renderWithReport = (overrides: Partial<AppearanceAdminReport> = {}) => {
  const value = {
    serverConfig: { appearance: { ...report, ...overrides } },
    setFieldInConfigState: jest.fn(),
  } as any;
  return render(
    <ServerStatusContext.Provider value={value}>
      <CustomPlayerConfig />
    </ServerStatusContext.Provider>,
  );
};

describe('CustomPlayerConfig (Appearance)', () => {
  beforeEach(() => {
    mockedService.getReport.mockReset().mockResolvedValue(report);
    mockedService.saveCustomPlayerPath.mockReset().mockResolvedValue(undefined);
    mockedService.saveCustomFrontendDir.mockReset().mockResolvedValue(undefined);
  });

  test('shows the active frontend badge and the precedence explanation', () => {
    renderWithReport();
    expect(screen.getByTestId('active-frontend-badge')).toHaveTextContent(/Active/i);
    expect(screen.getByTestId('active-frontend-badge')).toHaveTextContent(/Default viewer/i);
    expect(screen.getByText('How these settings interact')).toBeInTheDocument();
    expect(screen.getByTestId('custom-player-path-input')).not.toBeDisabled();
    expect(screen.getByTestId('custom-frontend-dir-input')).not.toBeDisabled();
  });

  test('explains that the custom player is ignored when a custom frontend is active', () => {
    renderWithReport({
      activeFrontend: 'custom-frontend',
      customFrontendDir: '/opt/owncast/frontend',
      customFrontendValid: true,
    });
    expect(screen.getByTestId('active-frontend-badge')).toHaveTextContent(/Custom frontend/i);
    expect(screen.getByTestId('custom-player-path-input')).toBeDisabled();
    expect(
      screen.getByText(/Ignored while a custom frontend directory is active/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Custom CSS and Custom JavaScript are not served/i),
    ).toBeInTheDocument();
  });

  test('renders path validation errors from the server report', () => {
    renderWithReport({
      customPlayerPath: '/does/not/exist.js',
      customPlayerError: 'custom player path does not exist or is not readable',
    });
    expect(
      screen.getByText('custom player path does not exist or is not readable'),
    ).toBeInTheDocument();
  });

  test('renders the last reported viewer-side load failure', () => {
    renderWithReport({
      activeFrontend: 'custom-player',
      customPlayerPath: '/opt/player.js',
      customPlayerValid: true,
      customPlayerLoadError: {
        message: 'Failed to fetch dynamically imported module',
        at: '2026-11-09T04:00:00Z',
      },
    });
    expect(
      screen.getByText(/The custom player failed to load for some viewers/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Failed to fetch dynamically imported module/),
    ).toBeInTheDocument();
  });

  test('saves the trimmed custom player path and refreshes the report', async () => {
    mockedService.getReport.mockResolvedValue({
      ...report,
      activeFrontend: 'custom-player',
      customPlayerPath: '/opt/player.js',
      customPlayerValid: true,
    });
    renderWithReport();
    fireEvent.change(screen.getByTestId('custom-player-path-input'), {
      target: { value: '  /opt/player.js  ' },
    });
    fireEvent.click(screen.getAllByText('Save')[0]);

    await waitFor(() =>
      expect(mockedService.saveCustomPlayerPath).toHaveBeenCalledWith('/opt/player.js'),
    );
    await waitFor(() => expect(mockedService.getReport).toHaveBeenCalled());
    await waitFor(() => expect(screen.getAllByText('Saved').length).toBeGreaterThan(0));
  });

  test('shows the server message when saving fails', async () => {
    mockedService.saveCustomFrontendDir.mockRejectedValue(
      new Error('custom frontend directory must contain index.html'),
    );
    renderWithReport();
    fireEvent.change(screen.getByTestId('custom-frontend-dir-input'), {
      target: { value: '/opt/empty' },
    });
    fireEvent.click(screen.getAllByText('Save')[1]);

    await waitFor(() =>
      expect(
        screen.getByText('custom frontend directory must contain index.html'),
      ).toBeInTheDocument(),
    );
  });
});
