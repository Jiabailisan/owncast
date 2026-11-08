// Phase 4 admin Live Stream Status API integration tests.
//
// Covers docs/architecture/api-contract.md section 4:
//   - GET /api/admin/live/status: aggregated snapshot shape, nullable metrics,
//     additive srt listener block, consistency with GET /admin/config/srt,
//   - GET /api/admin/live/viewers: pagination, clamping, filter/sort
//     normalization, protocol filtering,
//   - GET /api/admin/live/ffmpeg: process model + redacted argv/sections,
//   - admin authentication on all three read-only endpoints.
//
// The base URL defaults to the automated suite's server (127.0.0.1:8080) but
// can be overridden with OWNCAST_TEST_BASE_URL for an out-of-band instance.
// Every response is additionally validated against openapi.yaml by the shared
// jest setup hook.

const request = require('supertest');

const API_BASE = process.env.OWNCAST_TEST_BASE_URL || 'http://127.0.0.1:8080';
const api = request(API_BASE);
const ADMIN_USER = 'admin';
const ADMIN_PASS = process.env.OWNCAST_ADMIN_PASSWORD || 'abc123';

function asAdmin(req) {
	return req.auth(ADMIN_USER, ADMIN_PASS);
}

const STATUS_PATH = '/api/admin/live/status';
const VIEWERS_PATH = '/api/admin/live/viewers';
const FFMPEG_PATH = '/api/admin/live/ffmpeg';
const SRT_CONFIG_PATH = '/api/admin/config/srt';

const LIVE_STATES = ['live', 'offline', 'waiting', 'error', 'degraded'];
const INPUT_PROTOCOLS = ['rtmp', 'srt', ''];

describe('Admin live status authentication', () => {
	test('all live endpoints require admin auth', async () => {
		for (const path of [STATUS_PATH, VIEWERS_PATH, FFMPEG_PATH]) {
			await api.get(path).expect(401);
		}
	});

	test('a wrong admin password is rejected', async () => {
		await api.get(STATUS_PATH).auth(ADMIN_USER, 'wrong-password').expect(401);
	});
});

describe('GET /api/admin/live/status', () => {
	test('returns the aggregated snapshot shape', async () => {
		const res = await asAdmin(api.get(STATUS_PATH)).expect(200);
		const body = res.body;

		expect(LIVE_STATES).toContain(body.state);
		expect(INPUT_PROTOCOLS).toContain(body.inputProtocol);
		expect(typeof body.uptimeSeconds).toBe('number');
		expect(body.uptimeSeconds).toBeGreaterThanOrEqual(0);

		// Source panel: nullable metrics are present as keys, never estimated.
		expect(body.source).toBeDefined();
		for (const key of [
			'width',
			'height',
			'fps',
			'videoBitrateKbps',
			'audioBitrateKbps',
			'hasBFrames',
		]) {
			expect(Object.prototype.hasOwnProperty.call(body.source, key)).toBe(true);
		}

		// Ingest metrics are protocol-neutral and nullable.
		for (const key of [
			'bitrateKbps',
			'packetLossPct',
			'rttMs',
			'retransmittedPackets',
			'droppedPackets',
		]) {
			expect(Object.prototype.hasOwnProperty.call(body.ingest, key)).toBe(true);
		}

		// Viewer counts and FFmpeg process summary.
		for (const key of ['total', 'webrtc', 'hls']) {
			expect(typeof body.viewers[key]).toBe('number');
			expect(body.viewers[key]).toBeGreaterThanOrEqual(0);
		}
		expect(typeof body.ffmpeg.processModel).toBe('string');
		expect(body.ffmpeg.processModel.length).toBeGreaterThan(0);
		expect(Array.isArray(body.ffmpeg.processes)).toBe(true);

		// WebRTC capacity/warnings and GeoIP state.
		expect(typeof body.webrtc.enabled).toBe('boolean');
		expect(typeof body.webrtc.capacity.used).toBe('number');
		expect(typeof body.webrtc.capacity.limit).toBe('number');
		expect(Array.isArray(body.webrtc.warnings)).toBe(true);
		expect(typeof body.geoip.configured).toBe('boolean');
		expect(Array.isArray(body.warnings)).toBe(true);
	});

	test('offline snapshots never carry a start time or live session identity', async () => {
		const res = await asAdmin(api.get(STATUS_PATH)).expect(200);
		const body = res.body;

		if (body.state !== 'offline') {
			return;
		}
		expect(body.startTime === null || body.startTime === undefined).toBe(true);
		expect(body.uptimeSeconds).toBe(0);
		expect(body.ingest.sessionID === undefined || body.ingest.sessionID === '').toBe(true);
	});

	test('srt listener block matches GET /api/admin/config/srt listener state', async () => {
		const statusRes = await asAdmin(api.get(STATUS_PATH)).expect(200);
		const configRes = await asAdmin(api.get(SRT_CONFIG_PATH)).expect(200);

		const liveSrt = statusRes.body.srt;
		const configuredSrt = configRes.body.listener;
		expect(liveSrt).toBeDefined();
		for (const key of ['enabled', 'state', 'listening', 'port', 'pendingRestart']) {
			expect(liveSrt[key]).toEqual(configuredSrt[key]);
		}
	});
});

describe('GET /api/admin/live/viewers', () => {
	test('returns the contract defaults for an unfiltered request', async () => {
		const res = await asAdmin(api.get(VIEWERS_PATH)).expect(200);
		const body = res.body;

		expect(Array.isArray(body.viewers)).toBe(true);
		expect(typeof body.total).toBe('number');
		expect(body.page).toBe(1);
		expect(body.pageSize).toBe(25);
		for (const viewer of body.viewers) {
			expect(typeof viewer.clientID).toBe('string');
			expect(typeof viewer.ipAddress).toBe('string');
			expect(['hls', 'webrtc']).toContain(viewer.protocol);
			expect(['server', 'client']).toContain(viewer.source);
			expect(typeof viewer.durationSeconds).toBe('number');
			expect(typeof viewer.connectedAt).toBe('string');
		}
	});

	test('clamps page and pageSize to the documented bounds', async () => {
		const res = await asAdmin(
			api.get(VIEWERS_PATH).query({ page: 0, pageSize: 500 }),
		).expect(200);
		expect(res.body.page).toBe(1);
		expect(res.body.pageSize).toBe(200);

		const negative = await asAdmin(
			api.get(VIEWERS_PATH).query({ page: -3, pageSize: -1 }),
		).expect(200);
		expect(negative.body.page).toBe(1);
		expect(negative.body.pageSize).toBe(25);
	});

	test('normalizes unknown sort, order and protocol values instead of failing', async () => {
		const res = await asAdmin(
			api.get(VIEWERS_PATH).query({
				sort: 'not-a-sort',
				order: 'sideways',
				protocol: 'carrier-pigeon',
			}),
		).expect(200);
		expect(res.body.page).toBe(1);
		expect(res.body.pageSize).toBe(25);
		expect(Array.isArray(res.body.viewers)).toBe(true);
	});

	test('filters by protocol and query text', async () => {
		const webrtcOnly = await asAdmin(
			api.get(VIEWERS_PATH).query({ protocol: 'webrtc' }),
		).expect(200);
		for (const viewer of webrtcOnly.body.viewers) {
			expect(viewer.protocol).toBe('webrtc');
		}

		const hlsOnly = await asAdmin(
			api.get(VIEWERS_PATH).query({ protocol: 'hls' }),
		).expect(200);
		for (const viewer of hlsOnly.body.viewers) {
			expect(viewer.protocol).toBe('hls');
		}

		const noMatch = await asAdmin(
			api.get(VIEWERS_PATH).query({ q: 'definitely-no-such-viewer-9f13c2' }),
		).expect(200);
		expect(noMatch.body.total).toBe(0);
		expect(noMatch.body.viewers).toEqual([]);
	});

	test('returns an empty page when paging past the end', async () => {
		const res = await asAdmin(
			api.get(VIEWERS_PATH).query({ page: 999, pageSize: 10 }),
		).expect(200);
		expect(res.body.page).toBe(999);
		expect(res.body.viewers).toEqual([]);
	});
});

describe('GET /api/admin/live/ffmpeg', () => {
	test('returns the process model and a redacted process list', async () => {
		const res = await asAdmin(api.get(FFMPEG_PATH)).expect(200);
		const body = res.body;

		expect(typeof body.processModel).toBe('string');
		expect(body.processModel.length).toBeGreaterThan(0);
		expect(Array.isArray(body.processes)).toBe(true);

		for (const process of body.processes) {
			expect(typeof process.id).toBe('string');
			expect(typeof process.pid).toBe('number');
			expect(Array.isArray(process.purpose)).toBe(true);
			expect(typeof process.uptimeSeconds).toBe('number');
			expect(typeof process.restartCount).toBe('number');

			if (process.argv) {
				expect(Array.isArray(process.argv)).toBe(true);
				for (const arg of process.argv) {
					expect(arg).not.toMatch(/passphrase=/i);
					expect(arg).not.toMatch(/credential=/i);
					expect(arg).not.toMatch(/secret=/i);
				}
			}
			if (process.sections) {
				expect(typeof process.sections).toBe('object');
				expect(Array.isArray(process.sections.input)).toBe(true);
				expect(Array.isArray(process.sections.output)).toBe(true);
			}
		}
	});
});
