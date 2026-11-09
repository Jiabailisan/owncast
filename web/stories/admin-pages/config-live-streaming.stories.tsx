import { Meta, StoryObj } from '@storybook/nextjs';
import { http, HttpResponse } from 'msw';

import ConfigLiveStreaming from '../../pages/admin/config/live-streaming';
import { makeEmptyLiveStatus } from '../../interfaces/live-status.model';

const metadata = {
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

const srtConfig = {
  config: { enabled: false, port: 8890, passphraseSet: true },
  defaults: { enabled: false, port: 8890, passphrase: '' },
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

const webrtcConfig = {
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

const liveStatus = {
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
    audioProfile: 'LC',
    audioSampleRate: 48000,
    audioChannels: 2,
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
        speed: 0.98,
        droppedFrames: 0,
        duplicateFrames: 0,
        cpuPercent: 112.5,
        rssBytes: 104857600,
        gpuPercent: 12,
        gpuMemoryBytes: 335544320,
        gpuName: 'NVIDIA Quadro P600',
      },
    ],
  },
  webrtc: { enabled: true, capacity: { used: 1, limit: 100 }, warnings: [] },
  warnings: [],
  geoip: {
    configured: true,
    databaseType: 'GeoLite2-City',
    buildDate: '2026-01-01',
    sources: [
      {
        role: 'primary',
        configured: true,
        path: 'data/GeoLite2-City.mmdb',
        databaseType: 'GeoLite2-City',
        buildDate: '2026-01-01',
        hasCity: true,
      },
      {
        role: 'secondary',
        configured: true,
        path: 'data/dbip-city-lite.mmdb',
        databaseType: 'DBIP-City-Lite',
        buildDate: '2026-02-01',
        hasCity: true,
      },
    ],
  },
  outbound: {
    streams: [
      {
        variantIndex: 0,
        name: '720p @1200 kbps',
        videoEncoder: 'libx264',
        videoBitrateKbps: 1200,
        audioEncoder: 'aac',
        audioBitrateKbps: 128,
        protocols: ['hls', 'webrtc'],
      },
      {
        variantIndex: 1,
        name: 'Source',
        videoEncoder: 'copy',
        videoBitrateKbps: null,
        audioEncoder: 'copy',
        audioBitrateKbps: null,
        protocols: ['hls', 'webrtc'],
      },
    ],
  },
};

const viewers = {
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
  ],
  total: 1,
  page: 1,
  pageSize: 25,
};

const ffmpegPanel = {
  processModel: 'shared',
  processes: [
    {
      id: 'main',
      purpose: ['hls', 'webrtc'],
      pid: 4242,
      startedAt: '2026-11-09T03:00:00Z',
      uptimeSeconds: 3725,
      restartCount: 0,
      speed: 0.98,
      cpuPercent: 112.5,
      rssBytes: 104857600,
      gpuPercent: 12,
      gpuMemoryBytes: 335544320,
      gpuName: 'NVIDIA Quadro P600',
      sections: {
        common: ['-hide_banner', '-i', 'pipe:0'],
        hls: ['-c:v:0', 'libx264', '-f', 'hls'],
        webrtc: ['-c:v:1', 'libx264', '-f', 'rtp', 'rtp://127.0.0.1:9000'],
        environment: ['PATH=***'],
      },
      argv: ['ffmpeg', '-hide_banner', '-i', 'pipe:0', '-c:v:0', 'libx264'],
      lastLogLines: ['frame= 1000 fps=30 q=24.0 size=N/A time=00:00:33.33 bitrate=N/A speed=1x'],
    },
  ],
};

const handlers = [
  http.get('*/api/admin/config/settings-metadata', () => HttpResponse.json(metadata)),
  http.get('*/api/admin/config/srt', () => HttpResponse.json(srtConfig)),
  http.put('*/api/admin/config/srt', () =>
    HttpResponse.json({
      success: true,
      config: srtConfig.config,
      pendingRestart: { pending: false, fields: [] },
    }),
  ),
  http.get('*/api/admin/config/webrtc', () => HttpResponse.json(webrtcConfig)),
  http.put('*/api/admin/config/webrtc', () =>
    HttpResponse.json({
      success: true,
      config: webrtcConfig.config,
      pendingRestart: { pending: false, fields: [] },
    }),
  ),
  http.get('*/api/admin/live/status', () => HttpResponse.json(liveStatus)),
  http.get('*/api/admin/live/viewers*', () => HttpResponse.json(viewers)),
  http.get('*/api/admin/live/ffmpeg', () => HttpResponse.json(ffmpegPanel)),
];

const meta = {
  title: 'owncast/Admin/Pages/Config Live Streaming',
  component: ConfigLiveStreaming,
  parameters: {
    msw: { handlers },
  },
} satisfies Meta<typeof ConfigLiveStreaming>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
