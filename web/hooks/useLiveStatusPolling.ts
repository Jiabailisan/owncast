import { useCallback, useEffect, useRef, useState } from 'react';

import LiveStatusService from '../services/live-status-service';
import { FfmpegPanel, LiveStatus } from '../interfaces/live-status.model';

export const DEFAULT_LIVE_STATUS_POLL_MS = 2000;
const MAX_BACKOFF_MS = 15000;

export interface UseLiveStatusPollingOptions {
  enabled?: boolean;
  intervalMs?: number;
}

export interface LiveStatusPollingState {
  status: LiveStatus | null;
  ffmpeg: FfmpegPanel | null;
  error: string | null;
  lastUpdated: number | null;
  loading: boolean;
  refresh: () => void;
}

// Visibility-aware polling for the admin Live Stream Status section. Polls the
// status snapshot and the FFmpeg process panel together, pauses while the tab
// is hidden, backs off exponentially on errors and keeps the last good
// snapshot on screen (the UI shows a stale banner). Expected transient
// failures are logged with console.warn only: the browser test suite fails a
// page that logs a console.error.
export function useLiveStatusPolling(
  options: UseLiveStatusPollingOptions = {},
): LiveStatusPollingState {
  const { enabled = true, intervalMs = DEFAULT_LIVE_STATUS_POLL_MS } = options;

  const [status, setStatus] = useState<LiveStatus | null>(null);
  const [ffmpeg, setFfmpeg] = useState<FfmpegPanel | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<number | null>(null);
  const [loading, setLoading] = useState<boolean>(enabled);

  const mountedRef = useRef(false);
  const hiddenRef = useRef(false);
  const failuresRef = useRef(0);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const runRef = useRef<() => Promise<void>>(async () => {});
  const scheduleRef = useRef<(delay: number) => void>(() => {});

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const schedule = useCallback(
    (delay: number) => {
      clearTimer();
      if (!mountedRef.current || !enabled || hiddenRef.current) return;
      timerRef.current = setTimeout(() => {
        void runRef.current();
      }, delay);
    },
    [clearTimer, enabled],
  );

  const run = useCallback(async () => {
    if (!mountedRef.current || !enabled) return;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const [nextStatus, nextFfmpeg] = await Promise.all([
        LiveStatusService.getStatus({ signal: controller.signal }),
        LiveStatusService.getFfmpegPanel({ signal: controller.signal }),
      ]);
      if (!mountedRef.current || controller.signal.aborted) return;
      failuresRef.current = 0;
      setStatus(nextStatus);
      setFfmpeg(nextFfmpeg);
      setError(null);
      setLastUpdated(Date.now());
    } catch (pollError) {
      if (!mountedRef.current || controller.signal.aborted) return;
      failuresRef.current += 1;
      if (failuresRef.current === 1) {
        console.warn('Live status polling failed:', (pollError as Error)?.message);
      }
      setError((pollError as Error)?.message || 'Unable to load live stream status');
    } finally {
      if (!mountedRef.current) return;
      setLoading(false);
      const delay =
        failuresRef.current > 0
          ? Math.min(intervalMs * 2 ** failuresRef.current, MAX_BACKOFF_MS)
          : intervalMs;
      schedule(delay);
    }
  }, [enabled, intervalMs, schedule]);

  // Keep the timer callbacks pointing at the latest closures without touching
  // refs during render.
  useEffect(() => {
    runRef.current = run;
  }, [run]);
  useEffect(() => {
    scheduleRef.current = schedule;
  }, [schedule]);

  // Fetch on enable and clean up timer/requests when disabled or unmounted.
  useEffect(() => {
    mountedRef.current = true;
    if (!enabled) {
      setLoading(false);
      return () => {
        mountedRef.current = false;
        clearTimer();
        abortRef.current?.abort();
      };
    }

    setLoading(true);
    hiddenRef.current = document.visibilityState === 'hidden';
    void run();

    return () => {
      mountedRef.current = false;
      clearTimer();
      abortRef.current?.abort();
    };
  }, [enabled, run, clearTimer]);

  // Pause polling while the tab is hidden; refresh immediately on return so
  // the operator never sees a stale snapshot after switching back.
  useEffect(() => {
    if (!enabled || typeof document === 'undefined') return undefined;
    const handleVisibility = () => {
      const hidden = document.visibilityState === 'hidden';
      hiddenRef.current = hidden;
      if (hidden) {
        clearTimer();
      } else {
        void run();
      }
    };
    document.addEventListener('visibilitychange', handleVisibility);
    return () => document.removeEventListener('visibilitychange', handleVisibility);
  }, [enabled, run, clearTimer]);

  const refresh = useCallback(() => {
    clearTimer();
    void run();
  }, [clearTimer, run]);

  return { status, ffmpeg, error, lastUpdated, loading, refresh };
}

export default useLiveStatusPolling;
