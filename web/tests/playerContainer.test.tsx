import { render, screen, fireEvent } from '@testing-library/react';
import { Provider, createStore } from 'jotai';

import { PlayerContainer, WEBRTC_FALLBACK_SESSION_KEY } from '../components/video/PlayerContainer';
import { clientConfigStateAtom } from '../components/stores/ClientConfigStore';
import { makeEmptyClientConfig, ClientConfig } from '../interfaces/client-config.model';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

jest.mock('../components/video/OwncastPlayer/OwncastPlayer', () => ({
  OwncastPlayer: (props: any) => (
    <div
      data-testid="hls-player"
      data-transport-error={props.transportError}
      className={props.className}
    />
  ),
}));

jest.mock('../components/video/custom/CustomPlayerHost', () => ({
  CustomPlayerHost: (props: any) => (
    <div data-testid="custom-player" data-player-url={props.playerUrl}>
      <button
        type="button"
        data-testid="custom-load-failure"
        onClick={() => props.onFallback(new Error('module load failed'))}
      >
        fail
      </button>
    </div>
  ),
}));

jest.mock('../components/video/webrtc/WebRTCPlayer', () => ({
  WebRTCPlayer: (props: any) => (
    <div data-testid="webrtc-player" data-whep-url={props.whepUrl} className={props.className}>
      <button
        type="button"
        data-testid="force-fallback"
        onClick={() => props.onFallback({ code: 'capacity', message: 'full' })}
      >
        fail
      </button>
    </div>
  ),
}));

const enabledConfig = (): Partial<ClientConfig> => ({
  webrtc: {
    enabled: true,
    whepUrl: '/webrtc/whep',
    variants: [],
    iceServers: [],
    fallbackToHls: true,
  },
});

const customPlayerConfig = (): Partial<ClientConfig> => ({
  player: {
    custom: true,
    url: '/custom-player/player.js',
  },
});

const renderWithConfig = (
  config: Partial<ClientConfig>,
  online = true,
  className?: string,
  streamIdentity = '',
) => {
  const store = createStore();
  store.set(clientConfigStateAtom, { ...makeEmptyClientConfig(), ...config });
  return render(
    <Provider store={store}>
      <PlayerContainer
        source="/hls/stream.m3u8"
        online={online}
        streamIdentity={streamIdentity}
        title="Test stream"
        className={className}
      />
    </Provider>,
  );
};

describe('PlayerContainer protocol selection', () => {
  beforeEach(() => {
    (window as any).RTCPeerConnection = jest.fn();
    sessionStorage.clear();
  });

  afterEach(() => {
    delete (window as any).RTCPeerConnection;
  });

  test('renders the unchanged HLS player when WebRTC is disabled', () => {
    renderWithConfig({});
    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
    expect(screen.queryByTestId('webrtc-player')).not.toBeInTheDocument();
  });

  test('renders the HLS player root as the direct child of the content region', () => {
    const { container } = renderWithConfig({}, true, 'topSectionElement');
    const root = screen.getByTestId('hls-player');
    expect(container.firstChild).toBe(root);
    expect(root).toHaveClass('topSectionElement');
  });

  test('renders the WebRTC player root as the direct child of the content region', () => {
    const { container } = renderWithConfig(enabledConfig(), true, 'topSectionElement');
    const root = screen.getByTestId('webrtc-player');
    expect(container.firstChild).toBe(root);
    expect(root).toHaveClass('topSectionElement');
  });

  test('renders WebRTC when the server enables it', () => {
    renderWithConfig(enabledConfig());
    expect(screen.getByTestId('webrtc-player')).toHaveAttribute('data-whep-url', '/webrtc/whep');
    expect(screen.queryByTestId('hls-player')).not.toBeInTheDocument();
  });

  test('falls back to HLS and hands the failure to the error banner after a WebRTC failure', () => {
    renderWithConfig(enabledConfig());
    fireEvent.click(screen.getByTestId('force-fallback'));

    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
    expect(screen.queryByTestId('webrtc-player')).not.toBeInTheDocument();
    // The ported player renders the reference-style top-right error banner
    // from this prop (5s), replacing the old bottom notice.
    expect(screen.getByTestId('hls-player')).toHaveAttribute('data-transport-error', 'full');
    expect(JSON.parse(sessionStorage.getItem(WEBRTC_FALLBACK_SESSION_KEY) || '{}')).toEqual({
      value: true,
      identity: '',
    });
  });

  test('renders the custom player module instead of WebRTC/HLS when configured', () => {
    renderWithConfig(customPlayerConfig());
    expect(screen.getByTestId('custom-player')).toHaveAttribute(
      'data-player-url',
      '/custom-player/player.js',
    );
    expect(screen.queryByTestId('hls-player')).not.toBeInTheDocument();
    expect(screen.queryByTestId('webrtc-player')).not.toBeInTheDocument();
  });

  test('falls back to the default player when the custom module fails', () => {
    renderWithConfig(customPlayerConfig());
    fireEvent.click(screen.getByTestId('custom-load-failure'));

    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
    expect(screen.queryByTestId('custom-player')).not.toBeInTheDocument();
  });

  test('forceProtocol overrides the custom player (test/story override)', () => {
    const store = createStore();
    store.set(clientConfigStateAtom, { ...makeEmptyClientConfig(), ...customPlayerConfig() });
    render(
      <Provider store={store}>
        <PlayerContainer source="/hls/stream.m3u8" online title="Test stream" forceProtocol="hls" />
      </Provider>,
    );
    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
    expect(screen.queryByTestId('custom-player')).not.toBeInTheDocument();
  });

  test('remembers the fallback for the tab so a remount does not retry', () => {
    sessionStorage.setItem(
      WEBRTC_FALLBACK_SESSION_KEY,
      JSON.stringify({ value: true, identity: '' }),
    );
    renderWithConfig(enabledConfig());
    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
  });

  test('clears a stored fallback when a new stream comes online', () => {
    sessionStorage.setItem(
      WEBRTC_FALLBACK_SESSION_KEY,
      JSON.stringify({ value: true, identity: '' }),
    );
    const store = createStore();
    store.set(clientConfigStateAtom, { ...makeEmptyClientConfig(), ...enabledConfig() });
    const { rerender } = render(
      <Provider store={store}>
        <PlayerContainer source="/hls/stream.m3u8" online={false} title="Test stream" />
      </Provider>,
    );
    expect(screen.getByTestId('hls-player')).toBeInTheDocument();

    rerender(
      <Provider store={store}>
        <PlayerContainer source="/hls/stream.m3u8" online title="Test stream" />
      </Provider>,
    );

    expect(screen.getByTestId('webrtc-player')).toBeInTheDocument();
    expect(sessionStorage.getItem(WEBRTC_FALLBACK_SESSION_KEY)).toBeNull();
  });

  // A fast switch can go from one stream to the next without the page ever seeing the offline
  // gap, so the previous stream's fallback must not follow the viewer into the new one.
  test('a fallback from a previous stream does not apply to a new stream', () => {
    sessionStorage.setItem(
      WEBRTC_FALLBACK_SESSION_KEY,
      JSON.stringify({ value: true, identity: 'stream-A' }),
    );
    renderWithConfig(enabledConfig(), true, undefined, 'stream-B');
    expect(screen.getByTestId('webrtc-player')).toBeInTheDocument();
    expect(screen.queryByTestId('hls-player')).not.toBeInTheDocument();
  });

  // Within one stream the fallback has to hold, otherwise every status poll would reconnect.
  test('a fallback stays for the stream that produced it', () => {
    sessionStorage.setItem(
      WEBRTC_FALLBACK_SESSION_KEY,
      JSON.stringify({ value: true, identity: 'stream-A' }),
    );
    renderWithConfig(enabledConfig(), true, undefined, 'stream-A');
    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
    expect(screen.queryByTestId('webrtc-player')).not.toBeInTheDocument();
  });

  // A switch happens inside one mounted component: the fallback callback has to use the
  // identity of the stream that failed now, not the one that was current when it was created.
  test('a failure after a switch stores the new stream identity', () => {
    const store = createStore();
    store.set(clientConfigStateAtom, { ...makeEmptyClientConfig(), ...enabledConfig() });
    const { rerender } = render(
      <Provider store={store}>
        <PlayerContainer
          source="/hls/stream.m3u8"
          online
          streamIdentity="stream-A"
          title="Test stream"
        />
      </Provider>,
    );
    rerender(
      <Provider store={store}>
        <PlayerContainer
          source="/hls/stream.m3u8"
          online
          streamIdentity="stream-B"
          title="Test stream"
        />
      </Provider>,
    );
    fireEvent.click(screen.getByTestId('force-fallback'));
    expect(JSON.parse(sessionStorage.getItem(WEBRTC_FALLBACK_SESSION_KEY) || '{}')).toEqual({
      value: true,
      identity: 'stream-B',
    });
  });

  // And that stored fallback belongs to B: remounting for B keeps it.
  test('the fallback stored for the new stream survives a remount of that stream', () => {
    sessionStorage.setItem(
      WEBRTC_FALLBACK_SESSION_KEY,
      JSON.stringify({ value: true, identity: 'stream-B' }),
    );
    renderWithConfig(enabledConfig(), true, undefined, 'stream-B');
    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
  });

  test('respects fallbackToHls=false by staying on WebRTCPlayer without switching to HLS', () => {
    const noFallbackConfig = enabledConfig();
    noFallbackConfig.webrtc!.fallbackToHls = false;
    sessionStorage.setItem(
      WEBRTC_FALLBACK_SESSION_KEY,
      JSON.stringify({ value: true, identity: '' }),
    );
    renderWithConfig(noFallbackConfig);
    expect(screen.getByTestId('webrtc-player')).toBeInTheDocument();
    expect(screen.queryByTestId('hls-player')).not.toBeInTheDocument();
  });
});
