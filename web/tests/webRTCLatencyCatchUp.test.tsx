import { act, fireEvent, render, screen } from '@testing-library/react';

import { WebRTCPlayer } from '../components/video/webrtc/WebRTCPlayer';
import * as webrtcStats from '../components/video/webrtc/webrtc-stats';
import { LATENCY_STORAGE_KEY } from '../components/video/PlayerControls/preferences';
import { WHEPSessionHandle } from '../services/whep-client';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

jest.mock('react-hotkeys-hook', () => ({
  useHotkeys: jest.fn(),
}));

const fakeStream = { id: 'stream' } as unknown as MediaStream;

const makeSession = (getStats: jest.Mock): WHEPSessionHandle => ({
  pc: { getStats } as unknown as RTCPeerConnection,
  stream: fakeStream,
  sessionUrl: '/webrtc/whep/session/abc',
  variantIndex: 0,
  iceServersFromLink: [],
  close: jest.fn(),
  deleteSession: jest.fn(async () => {}),
});

const deferred = () => {
  let resolve!: (value: unknown) => void;
  const promise = new Promise(resolveFn => {
    resolve = resolveFn;
  });
  return { promise, resolve };
};

const bytesSample = (timestamp: number) => ({
  timestamp,
  videoBytes: 0,
  audioBytes: 0,
  framesDecoded: 0,
  jitterBufferDelay: 0,
  jitterBufferEmitted: 0,
  processingDelay: 0,
});

const videoEl = () => document.querySelector('video') as HTMLVideoElement;

describe('WebRTC low-latency catch-up loop', () => {
  let collectSpy: jest.SpyInstance;

  beforeAll(() => {
    Object.defineProperty(HTMLMediaElement.prototype, 'srcObject', {
      configurable: true,
      writable: true,
      value: null,
    });
    Object.defineProperty(HTMLMediaElement.prototype, 'paused', {
      configurable: true,
      get(this: HTMLMediaElement & { __paused?: boolean }) {
        return this.__paused ?? false;
      },
    });
    HTMLMediaElement.prototype.play = jest.fn(function play(this: HTMLMediaElement) {
      (this as HTMLMediaElement & { __paused?: boolean }).__paused = false;
      return Promise.resolve();
    }) as unknown as () => Promise<void>;
    HTMLMediaElement.prototype.pause = jest.fn(function pause(this: HTMLMediaElement) {
      (this as HTMLMediaElement & { __paused?: boolean }).__paused = true;
    }) as unknown as () => void;
  });

  beforeEach(() => {
    jest.useFakeTimers();
    localStorage.clear();
    localStorage.setItem(LATENCY_STORAGE_KEY, 'true');
    collectSpy = jest.spyOn(webrtcStats, 'collectWebRTCStatsPanel');
  });

  afterEach(() => {
    collectSpy.mockRestore();
    jest.useRealTimers();
    localStorage.clear();
  });

  // Drains the microtasks of the connection handshake without advancing timers, so the catch-up
  // interval is in place before a test starts ticking it.
  const flush = async () => {
    await act(async () => {
      for (let i = 0; i < 20; i += 1) {
        await Promise.resolve(); // eslint-disable-line no-await-in-loop
      }
    });
  };

  const tick = async (ms = 1000) => {
    await act(async () => {
      jest.advanceTimersByTime(ms);
    });
  };

  const mount = async (getStats: jest.Mock) => {
    const onReady = jest.fn();
    const rendered = render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        onReady={onReady}
        enableStats={false}
        sessionFactory={async () => makeSession(getStats)}
      />,
    );
    await flush();
    expect(onReady).toHaveBeenCalled();

    return rendered;
  };

  test('feeds each sample the previous one so the delta is measured between adjacent samples', async () => {
    const previousSeen: unknown[] = [];
    const bytesReturned: unknown[] = [];
    collectSpy.mockImplementation(((_report: unknown, previous?: unknown) => {
      previousSeen.push(previous);
      const bytes = bytesSample(previousSeen.length);
      bytesReturned.push(bytes);
      return { sample: { liveLatencySeconds: 0.2 }, bytes };
    }) as never);

    await mount(jest.fn(async () => new Map()));
    await tick();
    await tick();

    // The first sample has no baseline; every later sample is compared against the one before it,
    // which is what makes the interval jitter-buffer figure meaningful. Without the baseline the
    // collector falls back to the cumulative session average.
    expect(previousSeen[0]).toBeNull();
    expect(previousSeen[1]).toBe(bytesReturned[0]);
  });

  test('keeps the 1.0x / 1.1x thresholds while using adjacent samples', async () => {
    let latency = 5;
    collectSpy.mockImplementation((() => ({
      sample: { liveLatencySeconds: latency },
      bytes: bytesSample(1),
    })) as never);

    await mount(jest.fn(async () => new Map()));

    await tick();
    expect(videoEl().playbackRate).toBe(1);

    await tick();
    // Two consecutive late samples speed playback up to catch the buffer up.
    expect(videoEl().playbackRate).toBe(1.1);

    latency = 0.2;
    await tick();
    expect(videoEl().playbackRate).toBe(1);
  });

  test('discards samples that resolve after the player was torn down', async () => {
    const pending = deferred();
    // A sample that never settles keeps the catch-up delivery in flight for the whole test.
    const getStats = jest.fn(() => pending.promise);
    collectSpy.mockImplementation((() => ({
      sample: { liveLatencySeconds: 5 },
      bytes: bytesSample(1),
    })) as never);

    const { unmount } = await mount(getStats);

    await tick();
    // Nothing has been applied yet: the sample is still in flight.
    expect(collectSpy).not.toHaveBeenCalled();

    unmount();

    // The sample was still in flight. Resolving it after teardown used to keep running the dead
    // loop: it re-read the statistics and kept counting late samples for a player nobody watches.
    await act(async () => {
      pending.resolve(new Map());
      await Promise.resolve();
    });

    expect(collectSpy).not.toHaveBeenCalled();
  });

  test('discards in-flight samples when low latency is switched off', async () => {
    const pending = deferred();
    const getStats = jest.fn().mockReturnValue(pending.promise);
    collectSpy.mockImplementation((() => ({
      sample: { liveLatencySeconds: 5 },
      bytes: bytesSample(1),
    })) as never);

    await mount(getStats);

    await tick();
    expect(collectSpy).not.toHaveBeenCalled();

    // Turning low latency off is reachable only through the settings menu.
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
    fireEvent.click(screen.getByRole('button', { name: /minimize latency/i }));
    await act(async () => {
      pending.resolve(new Map());
      await Promise.resolve();
    });

    expect(collectSpy).not.toHaveBeenCalled();
    expect(videoEl().playbackRate).toBe(1);
  });

  test('starts a fresh sample window after a stream switch', async () => {
    const previousSeen: unknown[] = [];
    collectSpy.mockImplementation(((_report: unknown, previous?: unknown) => {
      previousSeen.push(previous);
      return { sample: { liveLatencySeconds: 0.2 }, bytes: bytesSample(previousSeen.length) };
    }) as never);

    const getStats = jest.fn(async () => new Map());
    const onReady = jest.fn();
    const { rerender } = render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        onReady={onReady}
        enableStats={false}
        sessionFactory={async () => makeSession(getStats)}
      />,
    );
    await flush();
    await tick();
    await tick();
    expect(previousSeen[1]).toBeDefined();

    rerender(
      <WebRTCPlayer
        whepUrl="/webrtc/whep?switch=1"
        online
        title="Test stream"
        onReady={onReady}
        enableStats={false}
        sessionFactory={async () => makeSession(getStats)}
      />,
    );
    await flush();
    const before = previousSeen.length;
    await tick();

    // The new session's first sample must not be compared against the previous session's last one.
    expect(previousSeen[before]).toBeNull();
  });
});
