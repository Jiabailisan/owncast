import { render, waitFor } from '@testing-library/react';

import { CustomPlayerHost } from '../components/video/custom/CustomPlayerHost';
import { AutoplaySetting } from '../utils/autoplay';

describe('CustomPlayerHost', () => {
  let warnSpy: jest.SpyInstance;

  beforeEach(() => {
    warnSpy = jest.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    warnSpy.mockRestore();
  });

  test('mounts the module and passes the stable context', async () => {
    const contextCapture: any = {};
    const destroy = jest.fn();
    const loader = async () => ({
      default: (el: HTMLElement, context: any) => {
        contextCapture.value = context;
        const node = document.createElement('div');
        node.textContent = 'custom player';
        el.appendChild(node);
        return { destroy };
      },
    });

    const { unmount, getByText } = render(
      <CustomPlayerHost
        playerUrl="/custom-player/player.js"
        hlsUrl="/hls/stream.m3u8"
        whepUrl="/webrtc/whep"
        variants={[{ index: 0, name: '720p' }]}
        online
        autoplay={AutoplaySetting.Always}
        title="Custom player"
        loader={loader as any}
        onFallback={jest.fn()}
      />,
    );

    await waitFor(() => expect(getByText('custom player')).toBeInTheDocument());
    expect(contextCapture.value).toMatchObject({
      hlsUrl: '/hls/stream.m3u8',
      whepUrl: '/webrtc/whep',
      isOnline: true,
      autoplay: 'unmuted-when-allowed',
      variants: [{ index: 0, name: '720p' }],
    });
    expect(typeof contextCapture.value.reportPlayback).toBe('function');

    unmount();
    expect(destroy).toHaveBeenCalledTimes(1);
  });

  test('reports a load failure and asks the container to fall back', async () => {
    const onFallback = jest.fn();
    const errorReporter = jest.fn();
    render(
      <CustomPlayerHost
        playerUrl="/custom-player/player.js"
        hlsUrl="/hls/stream.m3u8"
        online
        loader={async () => {
          throw new Error('404 module missing');
        }}
        errorReporter={errorReporter}
        onFallback={onFallback}
      />,
    );

    await waitFor(() => expect(onFallback).toHaveBeenCalledTimes(1));
    expect(onFallback.mock.calls[0][0].message).toContain('404 module missing');
    expect(errorReporter).toHaveBeenCalledWith(expect.stringContaining('404 module missing'));
  });

  test('rejects an instance without destroy() and falls back', async () => {
    const onFallback = jest.fn();
    render(
      <CustomPlayerHost
        playerUrl="/custom-player/player.js"
        hlsUrl="/hls/stream.m3u8"
        online
        loader={async () => ({ default: () => ({}) })}
        errorReporter={jest.fn()}
        onFallback={onFallback}
      />,
    );

    await waitFor(() => expect(onFallback).toHaveBeenCalledTimes(1));
    expect(onFallback.mock.calls[0][0].message).toContain('destroy()');
  });

  test('does not mount while the stream is offline', async () => {
    const loader = jest.fn();
    const onFallback = jest.fn();
    render(
      <CustomPlayerHost
        playerUrl="/custom-player/player.js"
        hlsUrl="/hls/stream.m3u8"
        online={false}
        loader={loader as any}
        onFallback={onFallback}
      />,
    );
    await new Promise(resolve => setTimeout(resolve, 20));
    expect(loader).not.toHaveBeenCalled();
    expect(onFallback).not.toHaveBeenCalled();
  });
});
