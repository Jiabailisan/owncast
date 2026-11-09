import { render, screen, waitFor, fireEvent } from '@testing-library/react';

import { WebRTCPlayer } from '../components/video/webrtc/WebRTCPlayer';
import { startWebRTCStatsBeacon } from '../components/video/webrtc/webrtc-stats';
import { WHEPSessionHandle, WHEPError } from '../services/whep-client';

jest.mock('../components/video/webrtc/webrtc-stats', () => ({
  ...jest.requireActual('../components/video/webrtc/webrtc-stats'),
  startWebRTCStatsBeacon: jest.fn(() => ({ stop: jest.fn(), sampleOnce: jest.fn() })),
}));

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

jest.mock('react-hotkeys-hook', () => ({
  useHotkeys: jest.fn(),
}));

const fakeStream = { id: 'stream' } as unknown as MediaStream;
const fakePc = { getStats: async () => new Map() } as unknown as RTCPeerConnection;

const makeSession = (): WHEPSessionHandle => ({
  pc: fakePc,
  stream: fakeStream,
  sessionUrl: '/webrtc/whep/session/abc',
  variantIndex: 0,
  iceServersFromLink: [],
  close: jest.fn(),
  deleteSession: jest.fn(async () => {}),
});

describe('WebRTCPlayer', () => {
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
    (HTMLMediaElement.prototype.play as jest.Mock).mockClear();
    localStorage.clear();
  });

  test('connects, reports the session and renders controls', async () => {
    const onReady = jest.fn();
    const session = makeSession();
    const { container } = render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        onReady={onReady}
        enableStats={false}
        sessionFactory={async () => session}
        className="topSectionElement"
      />,
    );

    await waitFor(() => expect(onReady).toHaveBeenCalledWith(session));
    expect(screen.getAllByLabelText('Play').length).toBeGreaterThan(0);
    expect(screen.getByLabelText('Fullscreen')).toBeInTheDocument();
    // The theme contract: #player is the root, with the region class and the
    // transport marker, for both HLS and WebRTC.
    expect(container.querySelector('#player')).toHaveAttribute('data-protocol', 'webrtc');
    expect(container.querySelector('#player')).toHaveClass('topSectionElement');
  });

  test('reports the bare WHEP resource id with the client stats beacon', async () => {
    const session = makeSession();
    render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        enableStats
        sessionFactory={async () => session}
      />,
    );

    await waitFor(() => expect(startWebRTCStatsBeacon).toHaveBeenCalled());
    const options = (startWebRTCStatsBeacon as jest.Mock).mock.calls[0][0];
    // The server keys sessions by the bare id; reporting the Location path
    // made every beacon 404.
    expect(options.sessionId).toBe('abc');
  });

  test('reports a fatal error to the container for the HLS fallback', async () => {
    const onFallback = jest.fn();
    render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        onFallback={onFallback}
        enableStats={false}
        sessionFactory={async () => {
          throw new WHEPError('capacity', 'WebRTC capacity is full');
        }}
      />,
    );

    await waitFor(() => expect(onFallback).toHaveBeenCalledTimes(1));
    expect(onFallback.mock.calls[0][0].code).toBe('capacity');
    expect(screen.getByRole('alert')).toHaveTextContent(/WebRTC capacity is full/);
  });

  test('tears the session down on unmount', async () => {
    const session = makeSession();
    const { unmount } = render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        enableStats={false}
        sessionFactory={async () => session}
      />,
    );
    await waitFor(() => expect(screen.getByLabelText('Play')).toBeInTheDocument());

    unmount();

    expect(session.deleteSession).toHaveBeenCalledTimes(1);
    expect(session.close).toHaveBeenCalledTimes(1);
  });

  test('settings menu shows the WebRTC note and hides the HLS-only quality list', async () => {
    const sessionFactory = jest.fn(async () => makeSession());
    render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        enableStats={false}
        variants={[
          { index: 0, name: '720p' },
          { index: 1, name: '360p' },
        ]}
        sessionFactory={sessionFactory}
      />,
    );

    await waitFor(() => expect(sessionFactory).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
    expect(screen.getByText('Stats for nerds')).toBeInTheDocument();
    expect(screen.getByText('Minimize latency')).toBeInTheDocument();
    expect(screen.getByTestId('transport-note')).toHaveTextContent('WebRTC (auto)');
    // The provided player only lists quality levels for HLS.
    expect(screen.queryByText('720p')).not.toBeInTheDocument();
  });

  test('shows the big unmute overlay only when autoplay muted playback itself', async () => {
    // First unmuted play is blocked (mimicking browser autoplay policy), the
    // muted retry succeeds: the player muted itself, so the overlay appears.
    (HTMLMediaElement.prototype.play as jest.Mock).mockImplementation(function play(
      this: HTMLMediaElement,
    ) {
      if (!this.muted) {
        return Promise.reject(new Error('autoplay blocked'));
      }
      (this as HTMLMediaElement & { __paused?: boolean }).__paused = false;
      return Promise.resolve();
    });

    render(
      <WebRTCPlayer
        whepUrl="/webrtc/whep"
        online
        title="Test stream"
        autoplay="always"
        enableStats={false}
        sessionFactory={async () => makeSession()}
      />,
    );

    await waitFor(() => expect(screen.getByLabelText('Unmute')).toBeInTheDocument());
  });
});
