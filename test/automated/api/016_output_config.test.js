// Output settings (FFmpeg process model) admin API integration tests.
//
// Covers GET/PUT /api/admin/config/output: admin auth, default values,
// validation field errors, round-trip persistence and CSRF behavior. The
// applyMode is restart-stream, so no live apply is asserted.

const request = require('supertest');

const API_BASE = process.env.OWNCAST_TEST_BASE_URL || 'http://127.0.0.1:8080';
const api = request(API_BASE);
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'abc123';

function asAdmin(req) {
	return req.auth(ADMIN_USER, ADMIN_PASS);
}

const OUTPUT_PATH = '/api/admin/config/output';

describe('Output settings API', () => {
	afterAll(async () => {
		// Leave the instance on the default model.
		await asAdmin(api.put(OUTPUT_PATH)).send({ processModel: 'shared' });
	});

	test('GET returns config, defaults and pendingRestart', async () => {
		const res = await asAdmin(api.get(OUTPUT_PATH)).expect(200);
		expect(res.body.config).toBeDefined();
		expect(res.body.defaults).toBeDefined();
		expect(res.body.pendingRestart).toBeDefined();
		expect(['shared', 'isolated']).toContain(res.body.config.processModel);
		expect(res.body.defaults.processModel).toBe('shared');
		expect(typeof res.body.pendingRestart.pending).toBe('boolean');
		expect(Array.isArray(res.body.pendingRestart.fields)).toBe(true);
	});

	test('PUT rejects invalid process models with fieldErrors', async () => {
		const res = await asAdmin(api.put(OUTPUT_PATH))
			.send({ processModel: 'parallel' })
			.expect(400);
		expect(res.body.success).toBe(false);
		expect(Array.isArray(res.body.fieldErrors)).toBe(true);
		expect(res.body.fieldErrors.some((e) => e.field === 'processModel')).toBe(
			true,
		);

		const missing = await asAdmin(api.put(OUTPUT_PATH)).send({}).expect(400);
		expect(
			missing.body.fieldErrors.some((e) => e.field === 'processModel'),
		).toBe(true);
	});

	test('PUT round-trips the process model', async () => {
		const updated = await asAdmin(api.put(OUTPUT_PATH))
			.send({ processModel: 'isolated' })
			.expect(200);
		expect(updated.body.success).toBe(true);
		expect(updated.body.config.processModel).toBe('isolated');
		expect(updated.body.defaults.processModel).toBe('shared');
		expect(updated.body.pendingRestart).toBeDefined();
		// restart-stream: no stream is live in this suite, so nothing is pending.
		expect(updated.body.pendingRestart.pending).toBe(false);

		const fetched = await asAdmin(api.get(OUTPUT_PATH)).expect(200);
		expect(fetched.body.config.processModel).toBe('isolated');

		const restored = await asAdmin(api.put(OUTPUT_PATH))
			.send({ processModel: 'shared' })
			.expect(200);
		expect(restored.body.config.processModel).toBe('shared');
	});

	test('output settings require admin auth', async () => {
		await api.get(OUTPUT_PATH).expect(401);
		await api.put(OUTPUT_PATH).send({ processModel: 'shared' }).expect(401);
	});

	test('CSRF protection rejects cookie-only mutations', async () => {
		const agent = request.agent(API_BASE);
		await agent.get(OUTPUT_PATH).auth(ADMIN_USER, ADMIN_PASS).expect(200);
		// No Authorization header and no CSRF header: the admin session cookie
		// alone must not authorize a mutation.
		await agent.put(OUTPUT_PATH).send({ processModel: 'shared' }).expect(403);
	});
});
