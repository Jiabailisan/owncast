import PlaybackMetrics from '../components/video/metrics/playback';

type Listener = () => void;

const makePlayer = () => {
  const listeners = new Map<string, Listener>();
  let paused = false;
  return {
    buffered: () => [],
    currentTime: () => 0,
    ended: () => false,
    error: () => null,
    off: jest.fn(),
    on: jest.fn((event: string, listener: Listener) => listeners.set(event, listener)),
    paused: () => paused,
    playbackRate: () => 1,
    seeking: () => false,
    setPaused: (value: boolean) => {
      paused = value;
    },
    tech: () => undefined,
    trigger: (event: string) => listeners.get(event)?.(),
  };
};

describe('PlaybackMetrics', () => {
  beforeEach(() => {
    global.fetch = jest.fn(() => Promise.resolve({})) as jest.Mock;
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  test('reports ended when a started player is torn down', async () => {
    jest.useFakeTimers();
    const player = makePlayer();
    const videojs = { Vhs: {}, xhr: jest.fn() };
    const metrics = new PlaybackMetrics(player, videojs);

    player.trigger('playing');
    await Promise.resolve();
    metrics.stop();
    await Promise.resolve();

    const reports = (global.fetch as jest.Mock).mock.calls.map(([, init]) => JSON.parse(init.body));
    expect(reports.map(report => report.sta)).toEqual(['p', 'e']);
  });

  test('reports a pause after playback starts', async () => {
    jest.useFakeTimers();
    const player = makePlayer();
    const metrics = new PlaybackMetrics(player, { Vhs: {}, xhr: jest.fn() });
    expect(metrics).toBeDefined();

    player.trigger('playing');
    player.trigger('pause');
    await Promise.resolve();

    const reports = (global.fetch as jest.Mock).mock.calls.map(([, init]) => JSON.parse(init.body));
    expect(reports.map(report => report.sta)).toEqual(['p', 'a']);
  });

  test('keeps a paused session fresh beyond the server stale timeout', async () => {
    jest.useFakeTimers();
    const player = makePlayer();
    const metrics = new PlaybackMetrics(player, { Vhs: {}, xhr: jest.fn() });
    expect(metrics).toBeDefined();

    player.trigger('playing');
    player.setPaused(true);
    player.trigger('pause');
    jest.advanceTimersByTime(60_000);
    await Promise.resolve();

    const reports = (global.fetch as jest.Mock).mock.calls.map(([, init]) => JSON.parse(init.body));
    expect(reports.map(report => report.sta)).toEqual(['p', 'a', 'a', 'a', 'a', 'a', 'a', 'a']);
  });

  test('stop restores videojs.Vhs.xhr and removes only its own player listeners', () => {
    const player = makePlayer();
    const originalXhr = jest.fn();
    const videojs = { Vhs: { xhr: originalXhr }, xhr: jest.fn() };
    const metrics = new PlaybackMetrics(player, videojs);

    expect(videojs.Vhs.xhr).not.toBe(originalXhr);
    metrics.stop();

    expect(videojs.Vhs.xhr).toBe(originalXhr);
    // Must detach specific listeners rather than calling bare player.off().
    expect(player.off).toHaveBeenCalled();
    player.off.mock.calls.forEach((args: unknown[]) => {
      expect(args.length).toBe(2);
    });
  });
});
