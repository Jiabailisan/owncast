// Persisted viewer player preferences (provided-player behavior): the selected
// transport and quality survive reloads exactly like the reference player.

import { getLocalStorage, setLocalStorage } from '../../../utils/localStorage';
import { TransportPreference } from './types';

// Persistence keys match the reference player: transport, volume and the
// latency toggle. Quality is session-only for HLS (not persisted).
export const TRANSPORT_STORAGE_KEY = 'owncast_transport';
export const LATENCY_STORAGE_KEY = 'latencyCompensatorEnabled';

const TRANSPORT_VALUES: TransportPreference[] = ['auto', 'webrtc', 'hls'];

export function readTransportPreference(): TransportPreference {
  const saved = getLocalStorage(TRANSPORT_STORAGE_KEY);
  return TRANSPORT_VALUES.includes(saved as TransportPreference)
    ? (saved as TransportPreference)
    : 'auto';
}

export function writeTransportPreference(transport: TransportPreference): void {
  setLocalStorage(TRANSPORT_STORAGE_KEY, transport);
}

// Resolve which engine actually plays for a saved preference / availability /
// previous WebRTC failure. Auto prefers WebRTC when it is configured and has
// not failed in this tab, otherwise HLS.
export function resolveTransport(
  preference: TransportPreference,
  options: { webrtcAvailable: boolean; sessionFallback: boolean },
): 'webrtc' | 'hls' {
  if (preference === 'hls') return 'hls';
  if (preference === 'webrtc') return options.webrtcAvailable ? 'webrtc' : 'hls';
  return options.webrtcAvailable && !options.sessionFallback ? 'webrtc' : 'hls';
}
