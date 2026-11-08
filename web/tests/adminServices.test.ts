import AdminSettingsService from '../services/admin-settings-service';
import LiveStatusService, { buildLiveViewersUrl } from '../services/live-status-service';
import { APIError } from '../utils/apis';

type ResponseLike = {
  ok: boolean;
  status: number;
  headers: { get: (name: string) => string | null };
  text: () => Promise<string>;
};

const makeResponse = (
  body: unknown,
  options: { status?: number; headers?: Record<string, string> } = {},
): ResponseLike => {
  const status = options.status ?? 200;
  const headerMap: Record<string, string> = {};
  Object.entries(options.headers || {}).forEach(([key, value]) => {
    headerMap[key.toLowerCase()] = value;
  });
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (name: string) => headerMap[name.toLowerCase()] ?? null },
    text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
  };
};

const lastFetchCall = (mock: jest.Mock) => {
  const [url, init] = mock.mock.calls[mock.mock.calls.length - 1];
  return { url: url as string, init: init as RequestInit };
};

describe('admin settings service', () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    jest.clearAllMocks();
  });

  test('loads settings metadata', async () => {
    const metadata = { groups: [], srt: { fields: [] }, webrtc: { fields: [] } };
    const fetchMock = jest.fn(async () => makeResponse(metadata));
    global.fetch = fetchMock as unknown as typeof fetch;

    const result = await AdminSettingsService.getSettingsMetadata();
    expect(result).toEqual(metadata);
    expect(lastFetchCall(fetchMock).url).toContain('api/admin/config/settings-metadata');
  });

  test('loads the SRT config and exposes pending restart state', async () => {
    const payload = {
      config: { enabled: true, port: 8890 },
      defaults: { enabled: false, port: 8890 },
      pendingRestart: { pending: true, fields: ['port'] },
      listener: {
        enabled: true,
        listening: true,
        boundPort: 8890,
        activeSession: null,
        lastError: '',
      },
    };
    const fetchMock = jest.fn(async () => makeResponse(payload));
    global.fetch = fetchMock as unknown as typeof fetch;

    const result = await AdminSettingsService.getSRTConfig();
    expect(result.pendingRestart).toEqual({ pending: true, fields: ['port'] });
    expect(result.config.port).toBe(8890);
  });

  test('saves the SRT config with PUT, JSON body and the CSRF header', async () => {
    const fetchMock = jest.fn(async () => makeResponse({ success: true }));
    global.fetch = fetchMock as unknown as typeof fetch;

    await AdminSettingsService.saveSRTConfig({ port: 8891 });
    const { url, init } = lastFetchCall(fetchMock);
    expect(url).toContain('api/admin/config/srt');
    expect(init.method).toBe('PUT');
    expect((init.headers as Record<string, string>)['X-Owncast-CSRF-Protection']).toBe('1');
    expect(JSON.parse(init.body as string)).toEqual({ port: 8891 });
  });

  test('throws APIError with field-level validation errors', async () => {
    const fetchMock = jest.fn(async () =>
      makeResponse(
        {
          success: false,
          message: 'validation failed',
          fieldErrors: [{ field: 'port', message: 'must not collide with the web port' }],
        },
        { status: 400 },
      ),
    );
    global.fetch = fetchMock as unknown as typeof fetch;

    await expect(AdminSettingsService.saveWebRTCConfig({ udpPort: 8189 })).rejects.toMatchObject({
      name: 'APIError',
      status: 400,
      fieldErrors: [{ field: 'port', message: 'must not collide with the web port' }],
    });
  });

  test('runs preflight and domain tests', async () => {
    const fetchMock = jest.fn(async () => makeResponse({ ffmpeg: {}, ports: [], warnings: [] }));
    global.fetch = fetchMock as unknown as typeof fetch;

    await AdminSettingsService.runPreflight('webrtc');
    let call = lastFetchCall(fetchMock);
    expect(call.url).toContain('/config/preflight');
    expect(call.init.method).toBe('POST');
    expect(JSON.parse(call.init.body as string)).toEqual({ target: 'webrtc' });

    await AdminSettingsService.testWebRTCDomain('live.example.com');
    call = lastFetchCall(fetchMock);
    expect(call.url).toContain('/config/webrtc/test-domain');
    expect(JSON.parse(call.init.body as string)).toEqual({ domain: 'live.example.com' });
  });

  test('propagates APIError and parses Retry-After', async () => {
    const fetchMock = jest.fn(async () =>
      makeResponse(
        { code: 'webrtc_capacity', message: 'full' },
        { status: 503, headers: { 'Retry-After': '4' } },
      ),
    );
    global.fetch = fetchMock as unknown as typeof fetch;

    try {
      await LiveStatusService.getStatus();
      throw new Error('expected rejection');
    } catch (error) {
      expect(error).toBeInstanceOf(APIError);
      expect((error as APIError).status).toBe(503);
      expect((error as APIError).retryAfterSeconds).toBe(4);
    }
  });
});

describe('live status service', () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    jest.clearAllMocks();
  });

  test('builds the viewers query string', () => {
    expect(buildLiveViewersUrl()).toContain('api/admin/live/viewers');
    const url = buildLiveViewersUrl({
      page: 2,
      pageSize: 25,
      sort: 'duration',
      order: 'desc',
      protocol: 'webrtc',
      q: '203.0.113',
    });
    expect(url).toContain('page=2');
    expect(url).toContain('pageSize=25');
    expect(url).toContain('protocol=webrtc');
    expect(url).toContain('q=203.0.113');
  });

  test('loads status, viewers and the ffmpeg panel', async () => {
    const fetchMock = jest.fn(async (url: string) => {
      if (url.includes('/live/ffmpeg')) {
        return makeResponse({ processModel: 'shared', processes: [] });
      }
      if (url.includes('/live/viewers')) {
        return makeResponse({ viewers: [], total: 0, page: 1, pageSize: 25 });
      }
      return makeResponse({ state: 'offline' });
    });
    global.fetch = fetchMock as unknown as typeof fetch;

    const status = await LiveStatusService.getStatus();
    expect(status.state).toBe('offline');
    const viewers = await LiveStatusService.getViewers({ page: 1 });
    expect(viewers.total).toBe(0);
    const panel = await LiveStatusService.getFfmpegPanel();
    expect(panel.processModel).toBe('shared');
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });
});
