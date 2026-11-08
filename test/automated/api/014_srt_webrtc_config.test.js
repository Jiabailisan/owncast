// Phase 2 SRT/WebRTC admin configuration API integration tests.
//
// Covers docs/architecture/api-contract.md sections 1-3:
//   - settings metadata registry shape,
//   - SRT and WebRTC configuration GET/PUT, secret write-only semantics,
//     field-level validation errors and pending restarts,
//   - preflight and test-domain diagnostics.
//
// The base URL defaults to the automated suite's server (127.0.0.1:8080) but
// can be overridden with OWNCAST_TEST_BASE_URL for an out-of-band instance.
// Every response is additionally validated against openapi.yaml by the shared
// jest setup hook.

const request = require('supertest');

const API_BASE = process.env.OWNCAST_TEST_BASE_URL || 'http://127.0.0.1:8080';
const api = request(API_BASE);
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'abc123';

function asAdmin(req) {
	return req.auth(ADMIN_USER, ADMIN_PASS);
}

const METADATA_PATH = '/api/admin/config/settings-metadata';
const SRT_PATH = '/api/admin/config/srt';
const WEBRTC_PATH = '/api/admin/config/webrtc';
const PREFLIGHT_PATH = '/api/admin/config/preflight';
const DOMAIN_PATH = '/api/admin/config/webrtc/test-domain';

describe('SRT/WebRTC settings metadata', () => {
	test('returns groups, sections and field metadata', async () => {
		const res = await asAdmin(api.get(METADATA_PATH)).expect(200);

		expect(Array.isArray(res.body.groups)).toBe(true);
		expect(res.body.groups.length).toBeGreaterThan(5);

		const srtFields = res.body.srt.fields;
		const webrtcFields = res.body.webrtc.fields;
		expect(Array.isArray(srtFields)).toBe(true);
		expect(Array.isArray(webrtcFields)).toBe(true);
		expect(srtFields.length).toBeGreaterThan(20);
		expect(webrtcFields.length).toBeGreaterThan(40);

		for (const field of srtFields.concat(webrtcFields)) {
			expect(typeof field.key).toBe('string');
			expect(typeof field.label).toBe('string');
			expect(field.label.length).toBeGreaterThan(0);
			expect(typeof field.type).toBe('string');
			expect(typeof field.description).toBe('string');
			expect(typeof field.applyMode).toBe('string');
			expect(typeof field.readOnly).toBe('boolean');
			expect(typeof field.secret).toBe('boolean');
		}

		// Structured TURN entries use the dedicated metadata type.
		const turnServers = webrtcFields.find((f) => f.key === 'turnServers');
		expect(turnServers.type).toBe('turnServerList');
		expect(turnServers.secret).toBe(true);

		// Secrets are marked write-only.
		const passphrase = srtFields.find((f) => f.key === 'passphrase');
		expect(passphrase.secret).toBe(true);
		expect(passphrase.type).toBe('secret');
	});

	test('documents non-applicable Opus settings as read-only fixed entries', async () => {
		const res = await asAdmin(api.get(METADATA_PATH)).expect(200);
		const fields = res.body.webrtc.fields;
		for (const key of ['opusFEC', 'opusDTX']) {
			const field = fields.find((f) => f.key === key);
			expect(field).toBeDefined();
			expect(field.readOnly).toBe(true);
			expect(field.applyMode).toBe('n/a');
			expect(field.secret).toBe(false);
		}
	});

	test('requires admin authentication', async () => {
		await api.get(METADATA_PATH).expect(401);
	});
});

describe('SRT configuration API', () => {
	const validPassphrase = 'jest-secret-password-123';

	test('GET returns config, defaults, pendingRestart and listener state', async () => {
		const res = await asAdmin(api.get(SRT_PATH)).expect(200);
		expect(res.body.config).toBeDefined();
		expect(res.body.defaults).toBeDefined();
		expect(res.body.pendingRestart).toBeDefined();
		expect(res.body.listener).toBeDefined();
		expect(typeof res.body.config.passphraseSet).toBe('boolean');
		// The secret value itself is never returned.
		expect(res.body.config.passphrase).toBeUndefined();
		expect(['stopped', 'disabled', 'running', 'failed']).toContain(
			res.body.listener.state,
		);
	});

	test('PUT validates cross-field rules with fieldErrors', async () => {
		const res = await asAdmin(api.put(SRT_PATH))
			.send({ payloadSize: 1456, mss: 500 })
			.expect(400);
		expect(res.body.success).toBe(false);
		expect(Array.isArray(res.body.fieldErrors)).toBe(true);
		expect(res.body.fieldErrors.some((e) => e.field === 'payloadSize')).toBe(
			true,
		);
	});

	test('PUT rejects invalid enums and short passphrases', async () => {
		const enumRes = await asAdmin(api.put(SRT_PATH))
			.send({ streamIdFormats: ['carrier-pigeon'] })
			.expect(400);
		expect(
			enumRes.body.fieldErrors.some((e) => e.field === 'streamIdFormats'),
		).toBe(true);

		const passphraseRes = await asAdmin(api.put(SRT_PATH))
			.send({ passphrase: 'short' })
			.expect(400);
		expect(
			passphraseRes.body.fieldErrors.some((e) => e.field === 'passphrase'),
		).toBe(true);
	});

	test('PUT round-trips values and keeps secrets write-only', async () => {
		const update = await asAdmin(api.put(SRT_PATH))
			.send({
				enabled: false,
				bindAddress: '127.0.0.1',
				port: 18890,
				receiverLatencyMs: 150,
				passphrase: validPassphrase,
			})
			.expect(200);
		expect(update.body.success).toBe(true);
		// Applied immediately (no publisher connected): not pending.
		expect(update.body.pendingRestart.pending).toBe(false);
		expect(update.body.config.receiverLatencyMs).toBe(150);
		expect(update.body.config.passphraseSet).toBe(true);
		expect(update.body.config.passphrase).toBeUndefined();
		expect(JSON.stringify(update.body)).not.toContain(validPassphrase);

		const fetched = await asAdmin(api.get(SRT_PATH)).expect(200);
		expect(fetched.body.config.port).toBe(18890);
		expect(fetched.body.config.passphraseSet).toBe(true);

		// Omitting the passphrase keeps the stored secret.
		const omitted = await asAdmin(api.put(SRT_PATH))
			.send({ receiverLatencyMs: 160 })
			.expect(200);
		expect(omitted.body.config.passphraseSet).toBe(true);

		// An explicit empty passphrase clears it.
		const cleared = await asAdmin(api.put(SRT_PATH))
			.send({ passphrase: '' })
			.expect(200);
		expect(cleared.body.config.passphraseSet).toBe(false);
	});
});

describe('WebRTC configuration API', () => {
	test('GET returns config, defaults, pendingRestart and service state', async () => {
		const res = await asAdmin(api.get(WEBRTC_PATH)).expect(200);
		expect(res.body.config).toBeDefined();
		expect(res.body.defaults).toBeDefined();
		expect(res.body.pendingRestart).toBeDefined();
		expect(res.body.service).toBeDefined();
		expect(Array.isArray(res.body.service.warnings)).toBe(true);
		expect(res.body.config.opusFEC).toBeDefined();
		expect(res.body.config.opusDTX).toBeDefined();
	});

	test('PUT validates ranges and enums with fieldErrors', async () => {
		const res = await asAdmin(api.put(WEBRTC_PATH))
			.send({ maxViewers: -1, h264Profile: 'high10', udpPortMin: 51000, udpPortMax: 50000 })
			.expect(400);
		const fields = res.body.fieldErrors.map((e) => e.field);
		expect(fields).toContain('maxViewers');
		expect(fields).toContain('h264Profile');
		expect(fields).toContain('udpPortMax');
	});

	test('PUT ignores read-only fixed Opus fields', async () => {
		const before = await asAdmin(api.get(WEBRTC_PATH)).expect(200);
		await asAdmin(api.put(WEBRTC_PATH))
			.send({ opusFEC: !before.body.config.opusFEC, opusDTX: !before.body.config.opusDTX })
			.expect(200);
		const after = await asAdmin(api.get(WEBRTC_PATH)).expect(200);
		expect(after.body.config.opusFEC).toBe(before.body.config.opusFEC);
		expect(after.body.config.opusDTX).toBe(before.body.config.opusDTX);
	});

	test('restart-service settings restart only the WebRTC service', async () => {
		const enabled = await asAdmin(api.put(WEBRTC_PATH))
			.send({
				enabled: true,
				bindAddress: '127.0.0.1',
				udpMode: 'mux',
				udpPort: 18289,
				tcpEnabled: true,
				tcpPort: 18289,
			})
			.expect(200);
		expect(enabled.body.success).toBe(true);
		expect(enabled.body.pendingRestart.pending).toBe(false);
		expect(enabled.body.config.enabled).toBe(true);
		expect(enabled.body.service.enabled).toBe(true);
		expect(enabled.body.service.listeningAddress).toBeTruthy();

		// A port change is restart-service: the handler must restart the
		// engine so the new address is live without a server restart.
		const restarted = await asAdmin(api.put(WEBRTC_PATH))
			.send({ udpPort: 18290, tcpPort: 18290 })
			.expect(200);
		expect(restarted.body.pendingRestart.pending).toBe(false);
		expect(restarted.body.service.listeningAddress).toContain('18290');

		const disabled = await asAdmin(api.put(WEBRTC_PATH))
			.send({ enabled: false })
			.expect(200);
		expect(disabled.body.service.enabled).toBe(false);
	});

	test('PUT round-trips TURN entries with masked secrets', async () => {
		const update = await asAdmin(api.put(WEBRTC_PATH))
			.send({
				enabled: false,
				maxViewers: 7,
				turnServers: [
					{
						url: 'turn:turn.example.org:3478',
						username: 'viewer',
						credential: 'jest-turn-credential',
					},
				],
			})
			.expect(200);
		expect(update.body.config.maxViewers).toBe(7);
		const turn = update.body.config.turnServers[0];
		expect(turn.credentialSet).toBe(true);
		expect(turn.credential).toBeUndefined();
		expect(JSON.stringify(update.body)).not.toContain('jest-turn-credential');

		// Omitting the credential keeps the stored secret.
		const omitted = await asAdmin(api.put(WEBRTC_PATH))
			.send({
				turnServers: [{ url: 'turn:turn.example.org:3478', username: 'viewer' }],
			})
			.expect(200);
		expect(omitted.body.config.turnServers[0].credentialSet).toBe(true);
	});
});

describe('Live streaming diagnostics', () => {
	test('preflight probes FFmpeg capabilities and ports', async () => {
		const res = await asAdmin(api.post(PREFLIGHT_PATH))
			.send({ target: 'all' })
			.expect(200);
		expect(res.body.ffmpeg).toBeDefined();
		expect(typeof res.body.ffmpeg.path).toBe('string');
		expect(Array.isArray(res.body.ffmpeg.h264Encoders)).toBe(true);
		expect(typeof res.body.ffmpeg.opusEncoder).toBe('boolean');
		expect(typeof res.body.ffmpeg.srtProtocol).toBe('boolean');
		expect(Array.isArray(res.body.ports)).toBe(true);
		for (const port of res.body.ports) {
			expect(['udp', 'tcp']).toContain(port.protocol);
			expect(typeof port.available).toBe('boolean');
		}
		expect(Array.isArray(res.body.warnings)).toBe(true);
	});

	test('preflight rejects unknown targets', async () => {
		const res = await asAdmin(api.post(PREFLIGHT_PATH))
			.send({ target: 'bogus' })
			.expect(400);
		expect(res.body.fieldErrors.some((e) => e.field === 'target')).toBe(true);
	});

	test('test-domain resolves a domain and advertises IPs', async () => {
		const res = await asAdmin(api.post(DOMAIN_PATH))
			.send({ domain: 'localhost' })
			.expect(200);
		expect(res.body.domain).toBe('localhost');
		expect(Array.isArray(res.body.resolvedIPs)).toBe(true);
		expect(res.body.resolvedIPs.length).toBeGreaterThan(0);
		expect(Array.isArray(res.body.advertisedIPs)).toBe(true);
		expect(typeof res.body.strictDomain).toBe('boolean');
		expect(res.body.error).toBe('');
	});

	test('test-domain rejects an invalid domain with a field error', async () => {
		const res = await asAdmin(api.post(DOMAIN_PATH))
			.send({ domain: 'not a domain' })
			.expect(400);
		expect(res.body.fieldErrors.some((e) => e.field === 'domain')).toBe(true);
	});

	test('admin auth is required for reads and writes', async () => {
		await api.get(SRT_PATH).expect(401);
		await api.put(SRT_PATH).send({}).expect(401);
		await api.post(PREFLIGHT_PATH).send({ target: 'all' }).expect(401);
	});

	test('CSRF protection rejects cookie-only mutations', async () => {
		const agent = request.agent(API_BASE);
		await agent
			.get(SRT_PATH)
			.auth(ADMIN_USER, ADMIN_PASS)
			.expect(200);
		// No Authorization header and no CSRF header: the admin session cookie
		// alone must not authorize a mutation.
		await agent.put(SRT_PATH).send({}).expect(403);
	});
});
