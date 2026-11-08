import { makeEmptyLiveStatus } from '../../interfaces/live-status.model';

// Shared fixtures for the Live Streaming admin stories. Shapes match
// docs/architecture/api-contract.md so the stories double as a visual contract
// reference.

export const settingsMetadata = {
  groups: [
    { id: 'srt.listener', label: 'Listener' },
    { id: 'srt.auth', label: 'Authentication' },
    { id: 'webrtc.general', label: 'General' },
    { id: 'webrtc.media', label: 'Media' },
  ],
  srt: {
    fields: [
      {
        key: 'enabled',
        group: 'srt.listener',
        type: 'bool',
        default: false,
        description: 'Enable the SRT ingest listener.',
        secret: false,
        applyMode: 'restart-service',
        readOnly: false,
      },
      {
        key: 'port',
        group: 'srt.listener',
        type: 'int',
        default: 8890,
        min: 1024,
        max: 65535,
        unit: 'port',
        description: 'UDP port the SRT listener binds.',
        secret: false,
        applyMode: 'restart-service',
        readOnly: false,
      },
      {
        key: 'passphrase',
        group: 'srt.auth',
        type: 'secret',
        default: '',
        description: 'Optional link encryption passphrase.',
        secret: true,
        applyMode: 'restart-service',
        readOnly: false,
      },
      {
        key: 'receiverLatencyMs',
        group: 'srt.listener',
        type: 'int',
        default: 120,
        min: 20,
        max: 10000,
        unit: 'ms',
        description: 'Receiver latency buffer.',
        secret: false,
        applyMode: 'restart-stream',
        readOnly: false,
      },
    ],
  },
  webrtc: {
    fields: [
      {
        key: 'enabled',
        group: 'webrtc.general',
        type: 'bool',
        default: false,
        description: 'Enable WebRTC delivery.',
        secret: false,
        applyMode: 'restart-service',
        readOnly: false,
      },
      {
        key: 'domain',
        group: 'webrtc.general',
        type: 'string',
        default: '',
        description: 'Public domain used for ICE candidates and the WHEP URL.',
        secret: false,
        applyMode: 'restart-service',
        readOnly: false,
      },
      {
        key: 'keyframeIntervalSeconds',
        group: 'webrtc.media',
        type: 'float',
        default: 1,
        min: 0.5,
        max: 10,
        unit: 's',
        description: 'Keyframe interval for WebRTC legs.',
        secret: false,
        applyMode: 'restart-stream',
        readOnly: false,
      },
    ],
  },
};

export const srtConfigResponse = {
  config: { enabled: false, port: 8890, passphraseSet: true, receiverLatencyMs: 120 },
  defaults: { enabled: false, port: 8890, passphrase: '', receiverLatencyMs: 120 },
  pendingRestart: { pending: false, fields: [] },
  listener: {
    enabled: false,
    state: 'stopped',
    listening: false,
    port: 0,
    boundAddress: '',
    lastError: '',
    pendingRestart: false,
    activeSession: null,
  },
};

export const webrtcConfigResponse = {
  config: { enabled: false, domain: 'live.example.com', keyframeIntervalSeconds: 1 },
  defaults: { enabled: false, domain: '', keyframeIntervalSeconds: 1 },
  pendingRestart: { pending: false, fields: [] },
  service: {
    enabled: false,
    listeningAddress: '',
    activeSessions: 0,
    capacity: { used: 0, limit: 100 },
    warnings: [],
  },
};

export const liveStatus = {
  ...makeEmptyLiveStatus(),
  state: 'live',
  inputProtocol: 'srt',
  streamKeyID: 'Default stream key',
  startTime: '2026-11-09T03:00:00Z',
  uptimeSeconds: 3725,
  source: {
    width: 1920,
    height: 1080,
    fps: 30,
    videoCodec: 'h264',
    videoProfile: 'high',
    audioCodec: 'aac',
    videoBitrateKbps: 2500,
    audioBitrateKbps: 128,
  },
  ingest: {
    bitrateKbps: 2510,
    packetLossPct: 0.2,
    rttMs: 12,
    retransmittedPackets: 4,
    droppedPackets: 0,
    detail: { flowWindow: 25600 },
  },
  viewers: { total: 3, webrtc: 1, hls: 2 },
  ffmpeg: {
    processModel: 'shared',
    processes: [
      {
        id: 'main',
        pid: 4242,
        uptimeSeconds: 3725,
        restartCount: 0,
        speed: 1,
        droppedFrames: 0,
        duplicateFrames: 0,
        cpuPercent: 12.5,
        rssBytes: 104857600,
      },
    ],
  },
  webrtc: { enabled: true, capacity: { used: 1, limit: 100 }, warnings: [] },
  warnings: [],
  geoip: { configured: false, databaseType: '', buildDate: '' },
};

export const viewersResponse = {
  viewers: [
    {
      clientID: 'client-1',
      ipAddress: '203.0.113.7',
      country: 'US',
      region: 'CA',
      locationApproximate: true,
      protocol: 'webrtc',
      bitrateKbps: 2450,
      resolution: '1280x720',
      fps: 30,
      packetLossPct: 0.4,
      jitterMs: 4,
      connectedAt: '2026-11-09T03:10:00Z',
      durationSeconds: 300,
      source: 'client',
    },
    {
      clientID: 'client-2',
      ipAddress: '198.51.100.20',
      protocol: 'hls',
      bitrateKbps: null,
      resolution: '1920x1080',
      fps: null,
      packetLossPct: null,
      jitterMs: null,
      connectedAt: '2026-11-09T03:00:00Z',
      durationSeconds: 900,
      source: 'server',
    },
  ],
  total: 2,
  page: 1,
  pageSize: 25,
};

export const ffmpegPanelResponse = {
  processModel: 'shared',
  processes: [
    {
      id: 'main',
      purpose: ['hls', 'webrtc'],
      pid: 4242,
      startedAt: '2026-11-09T03:00:00Z',
      uptimeSeconds: 3725,
      restartCount: 0,
      sections: {
        input: ['-i', 'pipe:0'],
        video: ['-c:v:0', 'libx264'],
        audio: ['-c:a:0', 'copy'],
        output: ['-f', 'hls'],
        environment: ['PATH=***'],
      },
      argv: ['ffmpeg', '-hide_banner', '-i', 'pipe:0', '-c:v:0', 'libx264'],
      lastLogLines: ['frame= 1000 fps=30 q=24.0 speed=1x'],
    },
  ],
};

export const preflightResponse = {
  ffmpeg: {
    path: '/usr/local/bin/ffmpeg',
    version: '8.1.3',
    h264Encoders: ['libx264', 'h264_nvenc'],
    opusEncoder: true,
    srtProtocol: true,
    warnings: [],
  },
  ports: [
    { protocol: 'udp', port: 8890, available: true },
    { protocol: 'udp', port: 8189, available: true },
  ],
  warnings: [{ code: 'no_hw_encoder', message: 'Hardware encoders unavailable' }],
};
