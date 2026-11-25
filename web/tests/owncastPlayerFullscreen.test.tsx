import { act, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { Provider, createStore } from 'jotai';
import { OwncastPlayer } from '../components/video/OwncastPlayer/OwncastPlayer';
import { VideoSettingsServiceContext } from '../services/video-settings-service';

const mockPlayer = {
  el: jest.fn(),
  on: jest.fn(),
  off: jest.fn(),
  play: jest.fn(),
  pause: jest.fn(),
  paused: jest.fn(() => true),
  ended: jest.fn(() => false),
  playbackRate: jest.fn(() => 1),
  buffered: jest.fn(() => ({ length: 0 })),
  duration: jest.fn(() => Infinity),
  readyState: jest.fn(() => 4),
  muted: jest.fn(() => false),
  volume: jest.fn(() => 1),
  isFullscreen: jest.fn(() => false),
  requestFullscreen: jest.fn(),
  exitFullscreen: jest.fn(),
  addChild: jest.fn(),
  tech: jest.fn(() => undefined),
  currentTime: jest.fn(() => 0),
};

jest.mock('../components/video/VideoJS/VideoJS', () => ({
  VideoJS: ({ onReady }: { onReady: (player: unknown, videojs: unknown) => void }) => {
    const { useEffect } = jest.requireActual('react');
    useEffect(() => {
      const doc = globalThis.document;
      const el = doc.createElement('div');
      const video = doc.createElement('video');
      el.appendChild(video);
      mockPlayer.el.mockReturnValue(el);
      const fakeVideojs = {
        getComponent: () =>
          class {
            buttonEl = doc.createElement('button');

            addClass(name: string) {
              this.buttonEl.classList.add(name);
            }

            controlText(label: string) {
              this.buttonEl.setAttribute('aria-label', label);
            }

            el() {
              return this.buttonEl;
            }

            show() {
              this.buttonEl.hidden = false;
            }

            hide() {
              this.buttonEl.hidden = true;
            }
          },
        Vhs: {},
        xhr: jest.fn(),
      };
      onReady(mockPlayer, fakeVideojs);
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);
    return <div data-testid="mock-videojs" />;
  },
}));

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

jest.mock('react-hotkeys-hook', () => ({
  useHotkeys: jest.fn(),
}));

describe('OwncastPlayer fullscreen container', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    Object.defineProperty(globalThis, 'fetch', {
      configurable: true,
      writable: true,
      value: jest.fn(async () => ({ ok: true })),
    });
    Object.defineProperty(document, 'fullscreenElement', {
      configurable: true,
      writable: true,
      value: null,
    });
  });

  test('requests fullscreen on #player so PlayerControls and StatsPanel remain visible', async () => {
    const store = createStore();
    const { container } = render(
      <Provider store={store}>
        <VideoSettingsServiceContext.Provider
          value={{ getVideoQualities: jest.fn(async () => []) }}
        >
          <OwncastPlayer source="/hls/stream.m3u8" online title="Test stream" />
        </VideoSettingsServiceContext.Provider>
      </Provider>,
    );

    const playerRoot = container.querySelector('#player') as HTMLDivElement;
    expect(playerRoot).not.toBeNull();
    const requestFullscreen = jest.fn(async () => {});
    const exitFullscreen = jest.fn(async () => {});
    playerRoot.requestFullscreen = requestFullscreen;
    document.exitFullscreen = exitFullscreen;

    fireEvent.click(screen.getByRole('button', { name: 'Fullscreen' }));
    expect(requestFullscreen).toHaveBeenCalledTimes(1);
    expect(mockPlayer.requestFullscreen).not.toHaveBeenCalled();

    // Simulate entering fullscreen and verify Exit fullscreen calls document.exitFullscreen.
    Object.defineProperty(document, 'fullscreenElement', {
      configurable: true,
      writable: true,
      value: playerRoot,
    });
    fireEvent(document, new Event('fullscreenchange'));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Exit fullscreen' })).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Exit fullscreen' }));
    expect(exitFullscreen).toHaveBeenCalledTimes(1);
  });

  test('resolves Auto quality label by quality.index rather than sorted array position', async () => {
    const playingHandlers: Array<() => void> = [];
    mockPlayer.on.mockImplementation((event: string | string[], cb: () => void) => {
      if (event === 'playing' || (Array.isArray(event) && event.includes('playing'))) {
        playingHandlers.push(cb);
      }
    });
    mockPlayer.tech.mockReturnValue({
      vhs: {
        playlists: {
          media: () => ({ uri: '/hls/0/stream.m3u8' }),
        },
      },
    });

    const store = createStore();
    render(
      <Provider store={store}>
        <VideoSettingsServiceContext.Provider
          value={{
            // /api/video/variants sorts descending by bitrate: index 1 comes first, index 0 second.
            getVideoQualities: jest.fn(async () => [
              { index: 1, name: 'High 4Mbps' },
              { index: 0, name: 'Low 1Mbps' },
            ]),
          }}
        >
          <OwncastPlayer source="/hls/stream.m3u8" online title="Test stream" />
        </VideoSettingsServiceContext.Provider>
      </Provider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
    await waitFor(() => expect(screen.getByText('Low 1Mbps')).toBeInTheDocument());

    act(() => {
      playingHandlers.forEach(cb => cb());
    });

    await waitFor(() => expect(screen.getByText('Auto (Low 1Mbps)')).toBeInTheDocument());
  });
});
