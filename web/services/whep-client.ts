// WHEP playback client (WebRTC HTTP Egress Protocol).
// Contract: docs/architecture/api-contract.md section 5.
//
// The client is framework-free and injectable so it can be unit-tested without
// a browser: fetchImpl and pcFactory are overridable. It returns the media
// stream plus the means to tear the session down (DELETE) and close the peer
// connection.

import { WHEPIceServer, WHEPErrorCode } from '../interfaces/whep.model';

export interface WHEPErrorOptions {
  status?: number;
  retryAfterSeconds?: number | null;
  cause?: unknown;
}

// Typed playback failure. 'capacity'/'forbidden'/'rate_limited'/'timeout'/
// 'network'/'signaling' should trigger the automatic HLS fallback;
// 'bad_request' and 'unsupported' indicate a broken configuration.
export class WHEPError extends Error {
  readonly code: WHEPErrorCode;

  readonly status: number;

  readonly retryAfterSeconds: number | null;

  constructor(code: WHEPErrorCode, message: string, options: WHEPErrorOptions = {}) {
    super(message);
    this.name = 'WHEPError';
    this.code = code;
    this.status = options.status ?? 0;
    this.retryAfterSeconds = options.retryAfterSeconds ?? null;
    if (options.cause !== undefined) {
      (this as Error & { cause?: unknown }).cause = options.cause;
    }
  }
}

export interface ConnectWHEPRequest {
  // WHEP endpoint, e.g. "/webrtc/whep" or an absolute URL.
  whepUrl: string;
  variantIndex?: number | null;
  iceServers?: WHEPIceServer[];
  // Cap on the ICE gathering wait before sending the offer (ms).
  offerTimeoutMs?: number;
  // How long to wait for the first remote track after the answer (ms).
  firstTrackTimeoutMs?: number;
  signal?: AbortSignal;
  fetchImpl?: typeof fetch;
  pcFactory?: (config: RTCConfiguration) => RTCPeerConnection;
}

export interface WHEPSessionHandle {
  pc: RTCPeerConnection;
  stream: MediaStream;
  // Absolute or root-relative Location URL used for PATCH/DELETE.
  sessionUrl: string;
  variantIndex: number | null;
  iceServersFromLink: WHEPIceServer[];
  // Close the peer connection only.
  close: () => void;
  // DELETE the server-side session (idempotent, failure tolerated).
  deleteSession: () => Promise<void>;
}

const DEFAULT_OFFER_TIMEOUT_MS = 4000;
const DEFAULT_FIRST_TRACK_TIMEOUT_MS = 10000;

export function toRTCIceServers(iceServers?: WHEPIceServer[]): RTCIceServer[] {
  return (iceServers || [])
    .filter(server => server && server.urls && server.urls.length > 0)
    .map(server => ({
      urls: server.urls,
      ...(server.username ? { username: server.username } : {}),
      ...(server.credential ? { credential: server.credential } : {}),
    }));
}

// Append the variant selection to the WHEP endpoint without disturbing an
// existing query string or fragment.
export function buildWhepVariantUrl(whepUrl: string, variantIndex?: number | null): string {
  if (variantIndex === undefined || variantIndex === null) {
    return whepUrl;
  }
  const hashIndex = whepUrl.indexOf('#');
  const withoutHash = hashIndex >= 0 ? whepUrl.slice(0, hashIndex) : whepUrl;
  const hash = hashIndex >= 0 ? whepUrl.slice(hashIndex) : '';
  const separator = withoutHash.includes('?') ? '&' : '?';
  return withoutHash + separator + 'variant=' + encodeURIComponent(String(variantIndex)) + hash;
}

// Parse RFC 8288 Link headers for rel="ice-server" entries. Only the
// ICE-server relation is meaningful to the player; malformed entries are
// ignored rather than failing playback.
export function parseIceServersFromLinkHeader(
  linkHeader: string | null | undefined,
): WHEPIceServer[] {
  if (!linkHeader) return [];
  const servers: WHEPIceServer[] = [];
  const relationPattern = /<([^>]+)>\s*;([^,]*)/g;
  let match = relationPattern.exec(linkHeader);
  while (match !== null) {
    const url = match[1];
    const params = match[2] || '';
    if (/rel\s*=\s*"?ice-server"?/i.test(params)) {
      const usernameMatch = /username\s*=\s*"([^"]*)"/i.exec(params);
      const credentialMatch = /credential\s*=\s*"([^"]*)"/i.exec(params);
      servers.push({
        urls: [url],
        ...(usernameMatch ? { username: usernameMatch[1] } : {}),
        ...(credentialMatch ? { credential: credentialMatch[1] } : {}),
      });
    }
    match = relationPattern.exec(linkHeader);
  }
  return servers;
}

export function parseRetryAfterSeconds(value: string | null | undefined): number | null {
  if (!value) return null;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) {
    return seconds;
  }
  const date = Date.parse(value);
  if (!Number.isNaN(date)) {
    return Math.max(0, Math.ceil((date - Date.now()) / 1000));
  }
  return null;
}

// Map a non-2xx WHEP response to a typed error. The capacity rejection is the
// one the viewer relies on for the HLS fallback.
export function mapWhepResponseError(
  status: number,
  retryAfterSeconds: number | null,
  bodyText: string,
): WHEPError {
  let serverCode = '';
  try {
    const body = bodyText ? JSON.parse(bodyText) : null;
    if (body && typeof body.code === 'string') serverCode = body.code;
  } catch {
    // body is not JSON; ignore.
  }

  const options: WHEPErrorOptions = { status, retryAfterSeconds };

  if (status === 503 || serverCode === 'webrtc_capacity') {
    return new WHEPError('capacity', 'WebRTC capacity is full', options);
  }
  if (status === 403 || status === 401) {
    return new WHEPError('forbidden', 'Playback is not allowed for this session', options);
  }
  if (status === 429) {
    return new WHEPError('rate_limited', 'Too many signaling requests', options);
  }
  if (status === 400 || status === 422) {
    return new WHEPError('bad_request', 'The playback offer was rejected', options);
  }
  return new WHEPError('signaling', 'WebRTC signaling failed', options);
}

// Wait (briefly) for ICE gathering to finish. A timeout is not fatal: the
// offer is sent with the candidates gathered so far.
function waitForIceGathering(pc: RTCPeerConnection, timeoutMs: number): Promise<void> {
  return new Promise(resolve => {
    if (pc.iceGatheringState === 'complete') {
      resolve();
      return;
    }
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      pc.removeEventListener('icegatheringstatechange', onChange);
      resolve();
    };
    const onChange = () => {
      if (pc.iceGatheringState === 'complete') finish();
    };
    const timer = setTimeout(finish, timeoutMs);
    pc.addEventListener('icegatheringstatechange', onChange);
  });
}

interface RemoteStreamWaiter {
  promise: Promise<MediaStream>;
  cancel: () => void;
}

function waitForRemoteStream(pc: RTCPeerConnection, timeoutMs: number): RemoteStreamWaiter {
  let resolveStream: (stream: MediaStream) => void = () => {};
  let settled = false;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const onTrack = (event: RTCTrackEvent) => {
    if (settled) return;
    settled = true;
    if (timer !== null) clearTimeout(timer);
    const stream =
      event.streams && event.streams.length > 0 ? event.streams[0] : new MediaStream([event.track]);
    resolveStream(stream);
  };

  const promise = new Promise<MediaStream>((resolve, reject) => {
    resolveStream = resolve;
    timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      pc.removeEventListener('track', onTrack);
      reject(new WHEPError('timeout', 'No media track was received'));
    }, timeoutMs);
    pc.addEventListener('track', onTrack);
  });

  return {
    promise,
    cancel: () => {
      if (settled) return;
      settled = true;
      if (timer !== null) clearTimeout(timer);
      pc.removeEventListener('track', onTrack);
    },
  };
}

// Perform the WHEP exchange and return a ready-to-attach session.
export async function connectWHEP(request: ConnectWHEPRequest): Promise<WHEPSessionHandle> {
  const {
    whepUrl,
    variantIndex = null,
    iceServers,
    offerTimeoutMs = DEFAULT_OFFER_TIMEOUT_MS,
    firstTrackTimeoutMs = DEFAULT_FIRST_TRACK_TIMEOUT_MS,
    signal,
    fetchImpl = fetch,
    pcFactory,
  } = request;

  const factory =
    pcFactory ||
    ((config: RTCConfiguration) => {
      if (typeof RTCPeerConnection === 'undefined') {
        throw new WHEPError('unsupported', 'RTCPeerConnection is not available');
      }
      return new RTCPeerConnection(config);
    });

  if (signal?.aborted) {
    throw new WHEPError('network', 'WHEP request was aborted');
  }

  const pc = factory({ iceServers: toRTCIceServers(iceServers) });
  let streamWaiter: RemoteStreamWaiter | null = null;

  try {
    pc.addTransceiver('video', { direction: 'recvonly' });
    pc.addTransceiver('audio', { direction: 'recvonly' });

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    await waitForIceGathering(pc, offerTimeoutMs);

    const sdpOffer = (pc.localDescription && pc.localDescription.sdp) || offer.sdp || '';
    const url = buildWhepVariantUrl(whepUrl, variantIndex);

    let response: Response;
    try {
      response = await fetchImpl(url, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/sdp',
          Accept: 'application/sdp',
        },
        body: sdpOffer,
        ...(signal ? { signal } : {}),
      });
    } catch (error) {
      if (signal?.aborted || (error as Error)?.name === 'AbortError') {
        throw new WHEPError('network', 'WHEP request was aborted', { cause: error });
      }
      throw new WHEPError('network', 'Could not reach the WebRTC endpoint', { cause: error });
    }

    if (!response.ok) {
      throw mapWhepResponseError(
        response.status,
        parseRetryAfterSeconds(response.headers.get('Retry-After')),
        await response.text(),
      );
    }

    const answerSdp = await response.text();
    if (!answerSdp) {
      throw new WHEPError('signaling', 'The WebRTC endpoint returned an empty answer');
    }

    const sessionUrl = response.headers.get('Location');
    if (!sessionUrl) {
      throw new WHEPError('signaling', 'The WebRTC endpoint did not return a session Location');
    }

    const iceServersFromLink = parseIceServersFromLinkHeader(response.headers.get('Link'));

    // The server picks the rendition when the request did not pin one; it reports
    // the choice back so the UI can name the quality being watched.
    const reportedVariant = Number(response.headers.get('X-Owncast-Variant'));
    const servedVariantIndex = Number.isInteger(reportedVariant) ? reportedVariant : variantIndex;

    // Start waiting for the track before applying the answer so an immediate
    // track event cannot be missed.
    streamWaiter = waitForRemoteStream(pc, firstTrackTimeoutMs);
    await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });

    const stream = await streamWaiter.promise;

    return {
      pc,
      stream,
      sessionUrl,
      variantIndex: servedVariantIndex ?? null,
      iceServersFromLink,
      close: () => {
        if (streamWaiter) streamWaiter.cancel();
        pc.close();
      },
      deleteSession: async () => {
        try {
          await fetchImpl(sessionUrl, { method: 'DELETE' });
        } catch {
          // Session teardown is best-effort; the server expires it anyway.
        }
      },
    };
  } catch (error) {
    if (streamWaiter) streamWaiter.cancel();
    try {
      pc.close();
    } catch {
      // Ignore close failures while surfacing the original error.
    }
    if (error instanceof WHEPError) throw error;
    throw new WHEPError('signaling', (error as Error)?.message || 'WebRTC session failed', {
      cause: error,
    });
  }
}
