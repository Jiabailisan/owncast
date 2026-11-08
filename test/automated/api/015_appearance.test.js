// Phase 5 appearance backend integration tests (spec 4.6, 7.3, 7.4, 8).
//
// Covers the custom player module, the custom viewer frontend directory and its
// precedence, the Custom CSS/JavaScript suppression while a custom frontend is
// active, and the public appearance/WebRTC stats beacons.
//
// Paths must exist on the host running the Owncast instance, so the test
// creates its fixtures in the OS temp directory and cleans them up.

const fs = require('fs');
const os = require('os');
const path = require('path');
const request = require('supertest');

const API_BASE = process.env.OWNCAST_TEST_BASE_URL || 'http://127.0.0.1:8080';
const api = request(API_BASE);
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'abc123';

function asAdmin(req) {
	return req.auth(ADMIN_USER, ADMIN_PASS);
}

const PLAYER_PATH = '/custom-player/player.js';
const SET_PLAYER = '/api/admin/config/customplayerpath';
const SET_FRONTEND = '/api/admin/config/customfrontenddir';
const ADMIN_CONFIG = '/api/admin/serverconfig';
const WEB_CONFIG = '/api/config';

function makeFixtureDirectory() {
	return fs.mkdtempSync(path.join(os.tmpdir(), 'owncast-appearance-test-'));
}

describe('Appearance customization backend', () => {
	let fixtureRoot = '';
	let playerPath = '';
	let frontendDir = '';

	beforeAll(() => {
		fixtureRoot = makeFixtureDirectory();
		playerPath = path.join(fixtureRoot, 'player.js');
		fs.writeFileSync(
			playerPath,
			"export default function mount(el) { el.textContent = 'appearance-test-player'; return { destroy() {} }; }\n",
		);

		frontendDir = path.join(fixtureRoot, 'frontend');
		fs.mkdirSync(path.join(frontendDir, '_next'), { recursive: true });
		fs.writeFileSync(
			path.join(frontendDir, 'index.html'),
			'<html><body><h1>appearance-test-frontend</h1></body></html>',
		);
		fs.writeFileSync(
			path.join(frontendDir, '_next', 'asset.js'),
			'console.log("appearance-test-asset");\n',
		);
	});

	afterAll(async () => {
		// Restore the default appearance even if a test failed.
		await asAdmin(api.post(SET_FRONTEND)).send({ value: '' });
		await asAdmin(api.post(SET_PLAYER)).send({ value: '' });
		fs.rmSync(fixtureRoot, { recursive: true, force: true });
	});

	test('custom player is inactive by default and validates on save', async () => {
		await asAdmin(api.post(SET_PLAYER)).send({ value: '' }).expect(200);
		await api.get(PLAYER_PATH).expect(404);

		const invalid = await asAdmin(api.post(SET_PLAYER))
			.send({ value: 'relative/player.js' })
			.expect(400);
		expect(invalid.body.success).toBe(false);

		const missing = await asAdmin(api.post(SET_PLAYER))
			.send({ value: path.join(fixtureRoot, 'missing.js') })
			.expect(400);
		expect(missing.body.success).toBe(false);
	});

	test('custom player module is served and advertised', async () => {
		await asAdmin(api.post(SET_PLAYER)).send({ value: playerPath }).expect(200);

		const module = await api.get(PLAYER_PATH).expect(200);
		expect(module.headers['content-type']).toContain('text/javascript');
		expect(module.headers['cache-control']).toContain('no-cache');
		expect(module.text).toContain('appearance-test-player');

		const config = await asAdmin(api.get(WEB_CONFIG)).expect(200);
		expect(config.body.player.custom).toBe(true);
		expect(config.body.player.url).toBe(PLAYER_PATH);

		const adminConfig = await asAdmin(api.get(ADMIN_CONFIG)).expect(200);
		expect(adminConfig.body.appearance.activeFrontend).toBe('custom-player');
		expect(adminConfig.body.appearance.customPlayerValid).toBe(true);
		expect(adminConfig.body.instanceDetails.customPlayerPath).toBe(playerPath);
	});

	test('custom frontend wins over the custom player and serves the viewer', async () => {
		await asAdmin(api.post(SET_FRONTEND)).send({ value: frontendDir }).expect(200);

		const index = await api.get('/').expect(200);
		expect(index.text).toContain('appearance-test-frontend');

		const asset = await api.get('/_next/asset.js').expect(200);
		expect(asset.text).toContain('appearance-test-asset');

		const spaFallback = await api.get('/some/client/side/route').expect(200);
		expect(spaFallback.text).toContain('appearance-test-frontend');

		// The custom player is ignored while a custom frontend is active.
		await api.get(PLAYER_PATH).expect(404);

		const adminIndex = await asAdmin(api.get('/admin/')).expect(200);
		expect(adminIndex.text).toContain('Owncast');
		expect(adminIndex.text).not.toContain('appearance-test-frontend');

		const adminConfig = await asAdmin(api.get(ADMIN_CONFIG)).expect(200);
		expect(adminConfig.body.appearance.activeFrontend).toBe('custom-frontend');
		expect(adminConfig.body.appearance.customFrontendValid).toBe(true);
	});

	test('custom CSS and JavaScript are not served with a custom frontend', async () => {
		const config = await asAdmin(api.get(WEB_CONFIG)).expect(200);
		expect(config.body.customStyles).toBe('');
		expect(config.body.player.custom).toBe(false);

		await api.get('/customjavascript').expect(404);
	});

	test('clearing the custom frontend restores the default viewer', async () => {
		await asAdmin(api.post(SET_FRONTEND)).send({ value: '' }).expect(200);

		const index = await api.get('/').expect(200);
		expect(index.text).toContain('Owncast');
		expect(index.text).not.toContain('appearance-test-frontend');

		// The custom player is active again.
		const module = await api.get(PLAYER_PATH).expect(200);
		expect(module.text).toContain('appearance-test-player');
	});

	test('custom player load errors surface on the admin Appearance report', async () => {
		await asAdmin(api.post('/api/custom-player/error'))
			.send({ message: 'module failed to load: 404' })
			.expect(204);

		const adminConfig = await asAdmin(api.get(ADMIN_CONFIG)).expect(200);
		expect(adminConfig.body.appearance.customPlayerLoadError.message).toContain(
			'module failed to load',
		);
	});

	test('WebRTC stats beacon accepts known sessions and 404s unknown ones', async () => {
		// No WebRTC sessions exist on the test instance.
		await api
			.post('/api/metrics/webrtc')
			.send({ sessionId: 'does-not-exist', fps: 30 })
			.expect(404);

		await api.post('/api/metrics/webrtc').send({}).expect(400);
	});

	test('appearance configuration requires admin auth', async () => {
		await api.post(SET_PLAYER).send({ value: '' }).expect(401);
		await api.post(SET_FRONTEND).send({ value: '' }).expect(401);
	});
});
