import { act, renderHook } from '@testing-library/react';

import { useLiveStatusPolling } from '../hooks/useLiveStatusPolling';
import LiveStatusService from '../services/live-status-service';
import { makeEmptyLiveStatus } from '../interfaces/live-status.model';

jest.mock('../services/live-status-service', () => ({
  __esModule: true,
  default: {
    getStatus: jest.fn(),
    getFfmpegPanel: jest.fn(),
    getViewers: jest.fn(),
  },
  buildLiveViewersUrl: jest.fn(),
}));

const mockedService = LiveStatusService as unknown as {
  getStatus: jest.Mock;
  getFfmpegPanel: jest.Mock;
};

const emptyStatus = makeEmptyLiveStatus();
const emptyPanel = { processModel: 'shared', processes: [] };

const setVisibility = (value: 'visible' | 'hidden') => {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => value,
  });
  document.dispatchEvent(new Event('visibilitychange'));
};

describe('useLiveStatusPolling', () => {
  let warnSpy: jest.SpyInstance;

  beforeEach(() => {
    jest.useFakeTimers();
    mockedService.getStatus.mockReset().mockResolvedValue(emptyStatus);
    mockedService.getFfmpegPanel.mockReset().mockResolvedValue(emptyPanel);
    warnSpy = jest.spyOn(console, 'warn').mockImplementation(() => {});
    setVisibility('visible');
  });

  afterEach(() => {
    jest.useRealTimers();
    warnSpy.mockRestore();
  });

  test('fetches status and ffmpeg on mount and polls at the configured interval', async () => {
    const { result } = renderHook(() => useLiveStatusPolling({ intervalMs: 2000 }));

    await act(async () => {
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(1);
    expect(mockedService.getFfmpegPanel).toHaveBeenCalledTimes(1);

    await act(async () => {
      jest.advanceTimersByTime(2000);
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(2);
    expect(result.current.status?.state).toBe('offline');
    expect(result.current.error).toBeNull();
  });

  test('pauses while the tab is hidden and refreshes immediately when visible', async () => {
    renderHook(() => useLiveStatusPolling({ intervalMs: 2000 }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(1);

    act(() => setVisibility('hidden'));
    await act(async () => {
      jest.advanceTimersByTime(10000);
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(1);

    await act(async () => {
      setVisibility('visible');
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(2);
  });

  test('keeps the last snapshot, warns once and backs off on errors', async () => {
    mockedService.getStatus
      .mockResolvedValueOnce(emptyStatus)
      .mockRejectedValueOnce(new Error('boom'))
      .mockRejectedValueOnce(new Error('boom again'));

    const { result } = renderHook(() => useLiveStatusPolling({ intervalMs: 2000 }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.status).not.toBeNull();

    await act(async () => {
      jest.advanceTimersByTime(2000);
      await Promise.resolve();
    });
    expect(result.current.error).toBe('boom');
    expect(result.current.status).not.toBeNull();
    expect(warnSpy).toHaveBeenCalledTimes(1);
    expect(warnSpy.mock.calls[0][0]).toContain('Live status polling failed');

    // Backoff: the next attempt is scheduled at 4s, not 2s.
    await act(async () => {
      jest.advanceTimersByTime(2000);
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(2);
    await act(async () => {
      jest.advanceTimersByTime(2000);
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(3);
  });

  test('stops polling on unmount', async () => {
    const { unmount } = renderHook(() => useLiveStatusPolling({ intervalMs: 2000 }));
    await act(async () => {
      await Promise.resolve();
    });
    unmount();
    await act(async () => {
      jest.advanceTimersByTime(20000);
      await Promise.resolve();
    });
    expect(mockedService.getStatus).toHaveBeenCalledTimes(1);
  });
});
