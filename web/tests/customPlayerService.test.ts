import {
  buildCustomPlayerContext,
  defaultCustomPlayerLoader,
  loadCustomPlayerModule,
  mapAutoplayForCustomPlayer,
  mapVariantsForCustomPlayer,
  reportCustomPlayerError,
  reportCustomPlayerPlaybackEvent,
  CUSTOM_PLAYER_MODULE_URL,
  CMCD_COLLECTOR_URL,
} from '../services/custom-player-service';
import { AutoplaySetting } from '../utils/autoplay';
import { CUSTOM_PLAYER_ERROR } from '../utils/apis';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

describe('custom player service', () => {
  test('maps the autoplay setting onto the stable context contract', () => {
    expect(mapAutoplayForCustomPlayer(AutoplaySetting.Off)).toBe('none');
    expect(mapAutoplayForCustomPlayer(AutoplaySetting.Always)).toBe('unmuted-when-allowed');
    expect(mapAutoplayForCustomPlayer(AutoplaySetting.SoundOnly)).toBe('unmuted-when-allowed');
    expect(mapAutoplayForCustomPlayer(AutoplaySetting.Always, true)).toBe('muted');
  });

  test('maps variants to the contract shape', () => {
    expect(
      mapVariantsForCustomPlayer([
        { index: 0, name: '720p', width: 1280, height: 720, fps: 30 },
        { index: 1, name: 'source' },
      ]),
    ).toEqual([
      { index: 0, name: '720p', width: 1280, height: 720 },
      { index: 1, name: 'source' },
    ]);
  });

  test('builds the context with HLS, WebRTC and playback reporting', () => {
    const reportPlayback = jest.fn();
    const context = buildCustomPlayerContext({
      hlsUrl: '/hls/stream.m3u8',
      whepUrl: '/webrtc/whep',
      variants: [{ index: 0, name: '720p', width: 1280, height: 720 }],
      isOnline: true,
      autoplay: AutoplaySetting.Always,
      lowLatency: true,
      reportPlayback,
    });
    expect(context).toEqual({
      hlsUrl: '/hls/stream.m3u8',
      whepUrl: '/webrtc/whep',
      variants: [{ index: 0, name: '720p', width: 1280, height: 720 }],
      isOnline: true,
      autoplay: 'unmuted-when-allowed',
      lowLatency: true,
      reportPlayback,
    });
  });

  test('passes host-provided libraries through to the module context', () => {
    const videojs = jest.fn();
    const context = buildCustomPlayerContext({
      hlsUrl: '/hls/stream.m3u8',
      isOnline: true,
      libs: { videojs },
    });
    expect(context.libs).toEqual({ videojs });
  });

  test('omits whepUrl when WebRTC is unavailable and reads lowLatency from storage', () => {
    localStorage.setItem('latencyCompensatorEnabled', 'true');
    const context = buildCustomPlayerContext({
      hlsUrl: '/hls/stream.m3u8',
      isOnline: false,
    });
    expect(context.whepUrl).toBeUndefined();
    expect(context.lowLatency).toBe(true);
    expect(context.autoplay).toBe('none');
    localStorage.removeItem('latencyCompensatorEnabled');
  });

  test('loads the default export mount function', async () => {
    const mount = jest.fn();
    const loader = jest.fn().mockResolvedValue({ default: mount });
    await expect(loadCustomPlayerModule('/custom-player/player.js', loader)).resolves.toBe(mount);
    expect(loader).toHaveBeenCalledWith('/custom-player/player.js');
    expect(CUSTOM_PLAYER_MODULE_URL).toBe('/custom-player/player.js');
  });

  test('accepts a module namespace whose default is the mount function directly', async () => {
    const mount = jest.fn();
    await expect(loadCustomPlayerModule(undefined, async () => mount)).resolves.toBe(mount);
  });

  test('rejects a module without a default mount function', async () => {
    await expect(
      loadCustomPlayerModule(undefined, async () => ({ default: 'nope' })),
    ).rejects.toThrow(/default-export a mount/);
  });

  test('rejects when the loader fails', async () => {
    await expect(
      loadCustomPlayerModule(undefined, async () => {
        throw new Error('404');
      }),
    ).rejects.toThrow(/Unable to load the custom player module/);
  });

  test('reports module failures to the public error sink, best-effort', async () => {
    const fetchImpl = jest.fn().mockResolvedValue({ ok: true });
    await reportCustomPlayerError('boom', { fetchImpl: fetchImpl as unknown as typeof fetch });
    expect(fetchImpl).toHaveBeenCalledWith(
      CUSTOM_PLAYER_ERROR,
      expect.objectContaining({ method: 'POST' }),
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.message).toBe('boom');

    // A failing sink never throws into playback.
    await expect(
      reportCustomPlayerError('boom', {
        fetchImpl: jest.fn().mockRejectedValue(new Error('offline')) as unknown as typeof fetch,
      }),
    ).resolves.toBeUndefined();
  });

  test('forwards playback events to the CMCD collector, best-effort', async () => {
    const fetchImpl = jest.fn().mockResolvedValue({ ok: true });
    await reportCustomPlayerPlaybackEvent(
      { event: 'play', position: 12 },
      { fetchImpl: fetchImpl as unknown as typeof fetch },
    );
    expect(fetchImpl).toHaveBeenCalledWith(
      CMCD_COLLECTOR_URL,
      expect.objectContaining({ method: 'POST' }),
    );
    expect(JSON.parse(fetchImpl.mock.calls[0][1].body)).toEqual({ event: 'play', position: 12 });
  });

  test('default loader is a function so bundlers can be bypassed at runtime', () => {
    expect(typeof defaultCustomPlayerLoader).toBe('function');
  });
});
