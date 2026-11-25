import { render, screen, fireEvent, waitFor } from '@testing-library/react';
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
});
