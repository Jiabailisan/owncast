import {
  buildWhepVariantUrl,
  connectWHEP,
  mapWhepResponseError,
  parseIceServersFromLinkHeader,
  parseRetryAfterSeconds,
  WHEPError,
} from '../services/whep-client';

type FetchResponseLike = {
  ok: boolean;
  status: number;
  headers: { get: (name: string) => string | null };
  text: () => Promise<string>;
};

const makeResponse = (
  body: string,
  options: { status?: number; headers?: Record<string, string> } = {},
): FetchResponseLike => {
  const status = options.status ?? 200;
  const headerMap: Record<string, string> = {};
  Object.entries(options.headers || {}).forEach(([key, value]) => {
    headerMap[key.toLowerCase()] = value;
  });
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (name: string) => headerMap[name.toLowerCase()] ?? null },
    text: async () => body,
  };
};

const fakeStream = { id: 'stream' } as unknown as MediaStream;

class FakePeerConnection {
  public iceGatheringState = 'complete';

  public localDescription: { sdp: string } | null = null;

  public closed = false;

  private trackHandler: ((event: unknown) => void) | null = null;

  public addTransceiver = jest.fn();

  public createOffer = jest.fn(async () => ({ type: 'offer', sdp: 'offer-sdp' }));

  public setLocalDescription = jest.fn(async () => {
    this.localDescription = { sdp: 'offer-sdp' };
  });

  public setRemoteDescription = jest.fn(async () => {
    if (this.trackHandler) {
      this.trackHandler({ streams: [fakeStream] });
    }
  });

  public addEventListener = jest.fn((type: string, handler: (event: unknown) => void) => {
    if (type === 'track') this.trackHandler = handler;
  });

  public removeEventListener = jest.fn();

  public close = jest.fn(() => {
    this.closed = true;
  });
}

const asPeerConnection = (pc: FakePeerConnection) => pc as unknown as RTCPeerConnection;

describe('whep client', () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    jest.clearAllMocks();
  });

  test('uses the variant the server reports when the request did not pin one', async () => {
    const pc = new FakePeerConnection();
    const fetchMock = jest.fn(async () =>
      makeResponse('answer-sdp', {
        status: 201,
        headers: {
          Location: '/webrtc/whep/session/def',
          'X-Owncast-Variant': '2',
        },
      }),
    );
    global.fetch = fetchMock as unknown as typeof fetch;

    const session = await connectWHEP({
      whepUrl: '/webrtc/whep',
      variantIndex: null,
      fetchImpl: fetchMock as unknown as typeof fetch,
      pcFactory: () => asPeerConnection(pc),
    });

    expect(session.variantIndex).toBe(2);
  });

  test('creates a session: offer POST, Location tracking, stream and ICE Link parsing', async () => {
    const pc = new FakePeerConnection();
    const fetchMock = jest.fn(async () =>
      makeResponse('answer-sdp', {
        status: 201,
        headers: {
          Location: '/webrtc/whep/session/abc',
          Link: '<stun:stun.example.org:3478>; rel="ice-server", <turn:turn.example.org:3478>; rel="ice-server"; username="user"; credential="pass"',
        },
      }),
    );
    global.fetch = fetchMock as unknown as typeof fetch;

    const session = await connectWHEP({
      whepUrl: '/webrtc/whep',
      variantIndex: 1,
      fetchImpl: fetchMock as unknown as typeof fetch,
      pcFactory: () => asPeerConnection(pc),
    });

    expect(pc.addTransceiver).toHaveBeenCalledWith('video', { direction: 'recvonly' });
    expect(pc.addTransceiver).toHaveBeenCalledWith('audio', { direction: 'recvonly' });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/webrtc/whep?variant=1');
    expect(init.method).toBe('POST');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/sdp');
    expect(init.body).toBe('offer-sdp');
    expect(session.sessionUrl).toBe('/webrtc/whep/session/abc');
    expect(session.stream).toBe(fakeStream);
    expect(session.variantIndex).toBe(1);
    expect(session.iceServersFromLink).toEqual([
      { urls: ['stun:stun.example.org:3478'] },
      {
        urls: ['turn:turn.example.org:3478'],
        username: 'user',
        credential: 'pass',
      },
    ]);

    await session.deleteSession();
    const deleteCall = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(deleteCall[0]).toBe('/webrtc/whep/session/abc');
    expect(deleteCall[1].method).toBe('DELETE');

    session.close();
    expect(pc.closed).toBe(true);
  });

  test('maps capacity rejection (503 + Retry-After) to a fallback error', async () => {
    const fetchMock = jest.fn(async () =>
      makeResponse('{"code":"webrtc_capacity"}', {
        status: 503,
        headers: { 'Retry-After': '7' },
      }),
    );
    global.fetch = fetchMock as unknown as typeof fetch;

    await expect(
      connectWHEP({
        whepUrl: '/webrtc/whep',
        fetchImpl: fetchMock as unknown as typeof fetch,
        pcFactory: () => asPeerConnection(new FakePeerConnection()),
      }),
    ).rejects.toMatchObject({
      code: 'capacity',
      status: 503,
      retryAfterSeconds: 7,
    });
  });

  test('maps forbidden and rate-limited responses', async () => {
    const forbidden = mapWhepResponseError(403, null, '');
    expect(forbidden.code).toBe('forbidden');
    const rateLimited = mapWhepResponseError(429, null, '');
    expect(rateLimited.code).toBe('rate_limited');
    const badRequest = mapWhepResponseError(400, null, '');
    expect(badRequest.code).toBe('bad_request');
    const unknown = mapWhepResponseError(500, null, '');
    expect(unknown.code).toBe('signaling');
  });

  test('fails when the response is missing the session Location', async () => {
    const fetchMock = jest.fn(async () => makeResponse('answer-sdp', { status: 201 }));
    global.fetch = fetchMock as unknown as typeof fetch;
    await expect(
      connectWHEP({
        whepUrl: '/webrtc/whep',
        fetchImpl: fetchMock as unknown as typeof fetch,
        pcFactory: () => asPeerConnection(new FakePeerConnection()),
      }),
    ).rejects.toMatchObject({ code: 'signaling' });
  });

  test('maps network failures to a typed error', async () => {
    const fetchMock = jest.fn(async () => {
      throw new Error('connection refused');
    });
    global.fetch = fetchMock as unknown as typeof fetch;
    await expect(
      connectWHEP({
        whepUrl: '/webrtc/whep',
        fetchImpl: fetchMock as unknown as typeof fetch,
        pcFactory: () => asPeerConnection(new FakePeerConnection()),
      }),
    ).rejects.toMatchObject({ code: 'network' });
  });

  test('reports unsupported when RTCPeerConnection is unavailable', async () => {
    const fetchMock = jest.fn();
    await expect(
      connectWHEP({
        whepUrl: '/webrtc/whep',
        fetchImpl: fetchMock as unknown as typeof fetch,
      }),
    ).rejects.toMatchObject({ code: 'unsupported' });
  });
});

describe('whep helpers', () => {
  test('buildWhepVariantUrl appends the variant without breaking queries', () => {
    expect(buildWhepVariantUrl('/webrtc/whep', null)).toBe('/webrtc/whep');
    expect(buildWhepVariantUrl('/webrtc/whep', 2)).toBe('/webrtc/whep?variant=2');
    expect(buildWhepVariantUrl('/webrtc/whep?x=1', 0)).toBe('/webrtc/whep?x=1&variant=0');
  });

  test('parseIceServersFromLinkHeader ignores unrelated relations', () => {
    expect(
      parseIceServersFromLinkHeader(
        '<https://example.com/page>; rel="describedby", <stun:a:3478>; rel="ice-server"',
      ),
    ).toEqual([{ urls: ['stun:a:3478'] }]);
    expect(parseIceServersFromLinkHeader(null)).toEqual([]);
  });

  test('parseRetryAfterSeconds supports seconds and HTTP dates', () => {
    expect(parseRetryAfterSeconds('5')).toBe(5);
    expect(parseRetryAfterSeconds(null)).toBeNull();
    const date = new Date(Date.now() + 5000).toUTCString();
    const parsed = parseRetryAfterSeconds(date);
    expect(parsed).not.toBeNull();
    expect(parsed as number).toBeGreaterThanOrEqual(3);
  });

  test('WHEPError keeps its typed code and status', () => {
    const error = new WHEPError('timeout', 'no media', { status: 504 });
    expect(error.name).toBe('WHEPError');
    expect(error.code).toBe('timeout');
    expect(error.status).toBe(504);
  });
});
