import { render, screen, waitFor } from '@testing-library/react';

import { LiveViewerTable } from '../components/admin/live/LiveViewerTable';
import LiveStatusService from '../services/live-status-service';
import { useMediaQuery } from '../hooks/useMediaQuery';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

jest.mock('../services/live-status-service', () => ({
  __esModule: true,
  default: { getViewers: jest.fn() },
}));

jest.mock('../hooks/useMediaQuery', () => ({
  __esModule: true,
  useMediaQuery: jest.fn(() => false),
  MOBILE_BREAKPOINT_QUERY: '(max-width: 768px)',
}));

const mockedService = LiveStatusService as unknown as { getViewers: jest.Mock };
const mockedUseMediaQuery = useMediaQuery as unknown as jest.Mock;

const viewersResponse = {
  viewers: [
    {
      clientID: 'client-1',
      ipAddress: '203.0.113.7',
      country: 'US',
      region: 'CA',
      locationApproximate: true,
      protocol: 'webrtc',
      iceTransport: 'udp',
      bitrateKbps: 2450,
      resolution: '1280x720',
      fps: 30,
      packetLossPct: 0.4,
      jitterMs: 4,
      connectedAt: '2026-11-09T03:10:00Z',
      durationSeconds: 300,
      source: 'client',
    },
    {
      clientID: 'client-tcp',
      ipAddress: '203.0.113.8',
      protocol: 'webrtc',
      iceTransport: 'tcp',
      bitrateKbps: null,
      resolution: '640x360',
      fps: 24,
      packetLossPct: null,
      jitterMs: null,
      connectedAt: '2026-11-09T03:12:00Z',
      durationSeconds: 120,
      source: 'server',
    },
    {
      clientID: 'client-2',
      ipAddress: '198.51.100.20',
      protocol: 'hls',
      bitrateKbps: null,
      resolution: '1920x1080',
      fps: null,
      packetLossPct: null,
      jitterMs: null,
      connectedAt: '2026-11-09T03:00:00Z',
      durationSeconds: 900,
      source: 'server',
    },
  ],
  total: 3,
  page: 1,
  pageSize: 25,
};

describe('LiveViewerTable', () => {
  beforeEach(() => {
    mockedUseMediaQuery.mockReturnValue(false);
    mockedService.getViewers.mockReset().mockResolvedValue(viewersResponse);
  });

  test('renders the server-paged viewer table with protocol, metrics and source', async () => {
    render(<LiveViewerTable enabled />);
    await waitFor(() => expect(screen.getByText('203.0.113.7')).toBeInTheDocument());
    expect(mockedService.getViewers).toHaveBeenCalledTimes(1);
    expect(screen.getByText('WebRTC-U')).toBeInTheDocument();
    expect(screen.getByText('WebRTC-T')).toBeInTheDocument();
    expect(screen.getByText('2450 kbps')).toBeInTheDocument();
    expect(screen.getAllByText('Player reported').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Server measured').length).toBeGreaterThan(0);
    // The server-measured HLS viewer has unknown bitrate/FPS: N/A, never an estimate.
    expect(screen.getAllByText('N/A').length).toBeGreaterThan(0);
  });

  test('does not fetch or render rows while disabled', () => {
    render(<LiveViewerTable enabled={false} />);
    expect(mockedService.getViewers).not.toHaveBeenCalled();
    expect(screen.queryByText('203.0.113.7')).not.toBeInTheDocument();
  });

  test('renders the mobile card list under the breakpoint', async () => {
    mockedUseMediaQuery.mockReturnValue(true);
    render(<LiveViewerTable enabled />);
    await waitFor(() => expect(screen.getByText('203.0.113.7')).toBeInTheDocument());
    // The card list does not render an antd table.
    expect(document.querySelector('.ant-table')).toBeNull();
    // Mobile cards keep the same transport tags and source labels.
    expect(screen.getByText('WebRTC-U')).toBeInTheDocument();
    expect(screen.getAllByText('Player reported').length).toBeGreaterThan(0);
  });

  test('shows an error with retry when the request fails', async () => {
    mockedService.getViewers.mockRejectedValue(new Error('server unavailable'));
    const warnSpy = jest.spyOn(console, 'warn').mockImplementation(() => {});
    render(<LiveViewerTable enabled />);
    await waitFor(() => expect(screen.getByText('server unavailable')).toBeInTheDocument());
    expect(warnSpy).toHaveBeenCalledWith('Live viewers request failed:', 'server unavailable');
    expect(screen.getByText('Retry')).toBeInTheDocument();
    warnSpy.mockRestore();
  });
});
