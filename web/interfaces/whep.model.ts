// WebRTC playback (WHEP) client shared types. Protocol details:
// docs/architecture/api-contract.md section 5.

// Viewer-facing ICE server as returned by /api/config and WHEP Link headers.
export interface WHEPIceServer {
  urls: string | string[];
  username?: string;
  credential?: string;
}

// Fatal playback failures that should trigger the HLS fallback (except
// 'unsupported' and 'bad_request', which indicate a broken configuration).
export type WHEPErrorCode =
  | 'capacity'
  | 'forbidden'
  | 'rate_limited'
  | 'bad_request'
  | 'timeout'
  | 'network'
  | 'unsupported'
  | 'signaling';

export interface WHEPSessionInfo {
  // Location header of the created session (used for PATCH trickle/DELETE).
  sessionUrl: string;
  answerSdp: string;
  iceServersFromLink: WHEPIceServer[];
  retryAfterSeconds: number | null;
}

export function isWHEPFallbackCode(code: WHEPErrorCode): boolean {
  return (
    code === 'capacity' ||
    code === 'forbidden' ||
    code === 'rate_limited' ||
    code === 'timeout' ||
    code === 'network' ||
    code === 'signaling'
  );
}
