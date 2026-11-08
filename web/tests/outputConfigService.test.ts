import OutputConfigService from '../services/output-config-service';

type ResponseLike = {
  ok: boolean;
  status: number;
  headers: { get: (name: string) => string | null };
  text: () => Promise<string>;
};

const makeResponse = (body: unknown, options: { status?: number } = {}): ResponseLike => {
  const status = options.status ?? 200;
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
  };
};

describe('output config service', () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    jest.clearAllMocks();
  });

  test('loads the output config with the contract response shape', async () => {
    const payload = {
      config: { processModel: 'isolated' },
      defaults: { processModel: 'shared' },
      pendingRestart: { pending: true, fields: ['processModel'] },
    };
    const fetchMock = jest.fn().mockResolvedValue(makeResponse(payload));
    global.fetch = fetchMock as unknown as typeof fetch;

    const result = await OutputConfigService.getOutputConfig();
    expect(result.config.processModel).toBe('isolated');
    expect(result.defaults.processModel).toBe('shared');
    expect(result.pendingRestart).toEqual({ pending: true, fields: ['processModel'] });
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain('config/output');
    expect(init.method).toBe('GET');
  });

  test('saves the raw processModel body with PUT and returns the state', async () => {
    const payload = {
      success: true,
      config: { processModel: 'isolated' },
      defaults: { processModel: 'shared' },
      pendingRestart: { pending: false, fields: [] },
    };
    const fetchMock = jest.fn().mockResolvedValue(makeResponse(payload));
    global.fetch = fetchMock as unknown as typeof fetch;

    const result = await OutputConfigService.saveOutputConfig('isolated');
    expect(result.config.processModel).toBe('isolated');
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain('config/output');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body as string)).toEqual({ processModel: 'isolated' });
    // Admin mutations carry the CSRF header.
    expect((init.headers as Record<string, string>)['X-Owncast-CSRF-Protection']).toBe('1');
  });

  test('propagates field-level validation errors as APIError', async () => {
    const fetchMock = jest.fn().mockResolvedValue(
      makeResponse(
        {
          success: false,
          message: 'validation failed',
          fieldErrors: [{ field: 'processModel', message: 'must be one of shared, isolated' }],
        },
        { status: 400 },
      ),
    );
    global.fetch = fetchMock as unknown as typeof fetch;

    await expect(OutputConfigService.saveOutputConfig('bogus' as never)).rejects.toMatchObject({
      name: 'APIError',
      status: 400,
      fieldErrors: [{ field: 'processModel', message: 'must be one of shared, isolated' }],
    });
  });
});
