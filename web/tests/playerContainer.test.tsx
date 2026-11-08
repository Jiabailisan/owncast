import { render, screen, fireEvent } from '@testing-library/react';
import { Provider, createStore } from 'jotai';

import { PlayerContainer, WEBRTC_FALLBACK_SESSION_KEY } from '../components/video/PlayerContainer';
import { clientConfigStateAtom } from '../components/stores/ClientConfigStore';
import { makeEmptyClientConfig, ClientConfig } from '../interfaces/client-config.model';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

jest.mock('../components/video/OwncastPlayer/OwncastPlayer', () => ({
  OwncastPlayer: () => <div data-testid="hls-player" />,
}));

jest.mock('../components/video/webrtc/WebRTCPlayer', () => ({
  WebRTCPlayer: (props: any) => (
    <div data-testid="webrtc-player" data-whep-url={props.whepUrl}>
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

const renderWithConfig = (config: Partial<ClientConfig>, online = true) => {
  const store = createStore();
  store.set(clientConfigStateAtom, { ...makeEmptyClientConfig(), ...config });
  return render(
    <Provider store={store}>
      <PlayerContainer source="/hls/stream.m3u8" online={online} title="Test stream" />
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

  test('renders WebRTC when the server enables it', () => {
    renderWithConfig(enabledConfig());
    expect(screen.getByTestId('webrtc-player')).toHaveAttribute('data-whep-url', '/webrtc/whep');
    expect(screen.queryByTestId('hls-player')).not.toBeInTheDocument();
  });

  test('falls back to HLS with a notice after a WebRTC failure', () => {
    renderWithConfig(enabledConfig());
    fireEvent.click(screen.getByTestId('force-fallback'));

    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
    expect(screen.queryByTestId('webrtc-player')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent(/Low-latency playback is unavailable/i);
    expect(sessionStorage.getItem(WEBRTC_FALLBACK_SESSION_KEY)).toBe('true');
  });

  test('remembers the fallback for the tab so a remount does not retry', () => {
    sessionStorage.setItem(WEBRTC_FALLBACK_SESSION_KEY, 'true');
    renderWithConfig(enabledConfig());
    expect(screen.getByTestId('hls-player')).toBeInTheDocument();
  });

  test('clears a stored fallback when a new stream comes online', () => {
    sessionStorage.setItem(WEBRTC_FALLBACK_SESSION_KEY, 'true');
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
});
