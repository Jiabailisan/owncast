import {
  TRANSPORT_STORAGE_KEY,
  readTransportPreference,
  resolveTransport,
  writeTransportPreference,
} from '../components/video/PlayerControls/preferences';

describe('player preferences', () => {
  beforeEach(() => localStorage.clear());

  test('defaults to auto when nothing is persisted', () => {
    expect(readTransportPreference()).toBe('auto');
  });

  test('persists the transport choice', () => {
    writeTransportPreference('hls');
    expect(localStorage.getItem(TRANSPORT_STORAGE_KEY)).toBe('hls');
    expect(readTransportPreference()).toBe('hls');
  });

  test('ignores invalid stored transports', () => {
    localStorage.setItem(TRANSPORT_STORAGE_KEY, 'bogus');
    expect(readTransportPreference()).toBe('auto');
  });

  test('auto prefers WebRTC when available and not forced HLS', () => {
    expect(resolveTransport('auto', { webrtcAvailable: true, sessionFallback: false })).toBe(
      'webrtc',
    );
    expect(resolveTransport('auto', { webrtcAvailable: false, sessionFallback: false })).toBe(
      'hls',
    );
  });

  test('auto falls back to HLS after a WebRTC failure', () => {
    expect(resolveTransport('auto', { webrtcAvailable: true, sessionFallback: true })).toBe('hls');
  });

  test('explicit choices win (WebRTC only when available)', () => {
    expect(resolveTransport('hls', { webrtcAvailable: true, sessionFallback: false })).toBe('hls');
    expect(resolveTransport('webrtc', { webrtcAvailable: true, sessionFallback: true })).toBe(
      'webrtc',
    );
    expect(resolveTransport('webrtc', { webrtcAvailable: false, sessionFallback: false })).toBe(
      'hls',
    );
  });
});
