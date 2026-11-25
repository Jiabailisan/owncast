import { render } from '@testing-library/react';

// video.js is an imperative widget. These tests pin the lifecycle contract
// added with the SRT/WebRTC work: dispose on unmount and recreate when the
// media source changes, so protocol switches never leak players or audio.

jest.mock('video.js', () => {
  const players: any[] = [];
  const videojs: any = jest.fn(() => {
    const player = {
      autoplay: jest.fn(),
      src: jest.fn(),
      on: jest.fn(),
      off: jest.fn(),
      dispose: jest.fn(),
      tech: jest.fn(() => undefined),
    };
    players.push(player);
    return player;
  });
  videojs.addLanguage = jest.fn();
  videojs.__players = players;
  return { __esModule: true, default: videojs };
});

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import videojs from 'video.js';
import { VideoJS } from '../components/video/VideoJS/VideoJS';

const mockedVideojs = videojs as unknown as { __players: any[] };
const createdPlayers = () => mockedVideojs.__players;

const optionsFor = (src: string) => ({
  autoplay: false,
  sources: [{ src, type: 'application/x-mpegURL' }],
});

describe('VideoJS lifecycle', () => {
  beforeEach(() => {
    createdPlayers().length = 0;
    (videojs as unknown as jest.Mock).mockClear();
  });

  test('disposes the player on unmount', () => {
    const { unmount } = render(
      <VideoJS options={optionsFor('/hls/stream.m3u8')} onReady={jest.fn()} />,
    );
    expect(createdPlayers()).toHaveLength(1);
    unmount();
    expect(createdPlayers()[0].dispose).toHaveBeenCalledTimes(1);
  });

  test('recreates the player when the media source changes', () => {
    const { rerender, unmount } = render(
      <VideoJS options={optionsFor('/hls/stream.m3u8')} onReady={jest.fn()} />,
    );
    expect(createdPlayers()).toHaveLength(1);

    rerender(<VideoJS options={optionsFor('/hls/other.m3u8')} onReady={jest.fn()} />);

    expect(createdPlayers()).toHaveLength(2);
    expect(createdPlayers()[0].dispose).toHaveBeenCalledTimes(1);
    expect(createdPlayers()[1].dispose).not.toHaveBeenCalled();

    unmount();
    expect(createdPlayers()[1].dispose).toHaveBeenCalledTimes(1);
  });

  test('does not recreate the player when only re-rendered with a new options object', () => {
    const { rerender } = render(
      <VideoJS options={optionsFor('/hls/stream.m3u8')} onReady={jest.fn()} />,
    );
    rerender(<VideoJS options={optionsFor('/hls/stream.m3u8')} onReady={jest.fn()} />);
    expect(createdPlayers()).toHaveLength(1);
  });

  test('cachebuster resolves relative m3u8 URIs against window.location.href', () => {
    let requestHook: ((o: { uri: string }) => { uri: string }) | undefined;
    (videojs as unknown as jest.Mock).mockImplementationOnce(() => {
      const player = {
        autoplay: jest.fn(),
        src: jest.fn(),
        on: jest.fn((event: string, cb: () => void) => {
          if (event === 'xhr-hooks-ready') {
            cb();
          }
        }),
        off: jest.fn(),
        dispose: jest.fn(),
        tech: jest.fn(() => ({
          vhs: {
            xhr: {
              onRequest: (hook: (o: { uri: string }) => { uri: string }) => {
                requestHook = hook;
              },
            },
          },
        })),
      };
      createdPlayers().push(player);
      return player;
    });

    render(<VideoJS options={optionsFor('/hls/stream.m3u8')} onReady={jest.fn()} />);
    expect(requestHook).toBeDefined();
    const out = requestHook!({ uri: '0/stream.m3u8' });
    expect(out.uri).toMatch(/^http:\/\/localhost\/0\/stream\.m3u8\?cachebust=[0-9a-f]+$/);
  });
});
