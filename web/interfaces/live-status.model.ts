// Admin Live Stream Status payloads.
//
// Shape source of truth: models/liveStatus.go (backend implementation) and
// docs/architecture/api-contract.md section 4. Every nullable metric is null
// when the source cannot measure it; the UI renders "N/A" and must never
// estimate a value.

export type LiveState = 'live' | 'offline' | 'waiting' | 'error' | 'degraded';

export type IngestProtocol = 'rtmp' | 'srt' | '';

export interface LiveSource {
  width: number | null;
  height: number | null;
  fps: number | null;
  videoCodec?: string;
  videoProfile?: string;
  videoLevel?: string;
  audioCodec?: string;
  videoBitrateKbps: number | null;
  audioBitrateKbps: number | null;
  hasBFrames?: boolean | null;

  // Additive audio codec details (SRT via the MPEG-TS/AAC inspector). They are
  // omitted by protocols that do not carry them, keeping the RTMP payload
  // byte-identical.
  audioProfile?: string;
  audioSampleRate?: number;
  audioChannels?: number;
}

export interface LiveIngest {
  sessionID?: string;
  remoteAddr?: string;
  bitrateKbps: number | null;
  packetLossPct: number | null;
  rttMs: number | null;
  retransmittedPackets: number | null;
  droppedPackets: number | null;
  detail?: Record<string, number>;
}

export interface LiveViewerCounts {
  total: number;
  webrtc: number;
  hls: number;
}

// One supervised FFmpeg process as summarized in the status snapshot.
export interface LiveFfmpegProcess {
  id: string;
  purpose?: string[];
  pid: number;
  startedAt?: string;
  uptimeSeconds: number;
  restartCount: number;
  speed: number | null;
  droppedFrames: number | null;
  duplicateFrames: number | null;
  cpuPercent: number | null;
  rssBytes: number | null;
  // Best-effort GPU metrics for hardware encoders (null when not measurable).
  gpuPercent?: number | null;
  gpuMemoryBytes?: number | null;
  gpuName?: string;
  // Populated by the command inspector; secrets are redacted with ***.
  argv?: string[];
  sections?: Record<string, string[]>;
}

export interface LiveFfmpegSummary {
  processModel: string;
  processes: LiveFfmpegProcess[];
}

export interface LiveWarning {
  code: string;
  message: string;
}

export interface LiveWebRTCStatus {
  enabled: boolean;
  capacity: { used: number; limit: number };
  warnings: LiveWarning[];
}

// One loaded (or attempted) GeoIP database file. Sources are ordered primary
// first; the secondary database fills in missing city data.
export interface LiveGeoIPSource {
  role: string; // 'primary' | 'secondary'
  configured: boolean;
  path?: string;
  databaseType?: string;
  buildDate?: string;
  hasCity?: boolean;
  error?: string;
}

export interface LiveGeoIPStatus {
  configured: boolean;
  databaseType?: string;
  buildDate?: string;
  sources?: LiveGeoIPSource[];
}

// One planned outbound stream (one output variant). Bitrates are the
// configured/planned targets, not measured throughput; null means the plan or
// source does not provide a value and must render N/A (never 0/Unknown).
export interface LiveOutboundStream {
  variantIndex: number;
  name?: string;
  videoEncoder?: string;
  videoBitrateKbps: number | null;
  audioEncoder?: string;
  audioBitrateKbps: number | null;
  protocols: string[];
}

export interface LiveOutboundStatus {
  streams: LiveOutboundStream[];
}

// GET /api/admin/live/status
export interface LiveStatus {
  state: LiveState;
  inputProtocol: IngestProtocol;
  streamKeyID?: string;
  startTime?: string | null;
  uptimeSeconds: number;
  source: LiveSource;
  ingest: LiveIngest;
  viewers: LiveViewerCounts;
  ffmpeg: LiveFfmpegSummary;
  webrtc: LiveWebRTCStatus;
  warnings: LiveWarning[];
  geoip: LiveGeoIPStatus;
  // Additive: planned outbound streams for the active plan. Optional for
  // compatibility; the UI hides the table when streams is empty/absent.
  outbound?: LiveOutboundStatus;
}

// GET /api/admin/live/viewers
export type LiveViewerMetricSource = 'server' | 'client';

export interface LiveViewer {
  clientID: string;
  ipAddress: string;
  userAgent?: string;
  // Geo fields are flat in the backend payload (models.LiveViewer).
  country?: string;
  region?: string;
  city?: string;
  locationApproximate?: boolean;
  protocol: 'hls' | 'webrtc';
  // WebRTC only: ICE transport selected by the client. Omitted when unknown.
  iceTransport?: 'udp' | 'tcp';
  bitrateKbps: number | null;
  resolution?: string;
  fps: number | null;
  packetLossPct: number | null;
  jitterMs: number | null;
  connectedAt: string;
  durationSeconds: number;
  source: LiveViewerMetricSource;
}

export type LiveViewersSort = 'duration' | 'bitrate' | 'protocol' | 'connectedAt';

export interface LiveViewersQuery {
  page?: number;
  pageSize?: number;
  sort?: LiveViewersSort;
  order?: 'asc' | 'desc';
  protocol?: 'all' | 'hls' | 'webrtc';
  q?: string;
}

// GET /api/admin/live/viewers
export interface LiveViewersResponse {
  viewers: LiveViewer[];
  total: number;
  page: number;
  pageSize: number;
}

// GET /api/admin/live/ffmpeg. Section keys come from the command inspector;
// the known groups are listed below, unknown groups are still renderable.
export type FfmpegArgSections = Record<string, string[]>;

export const FFMPEG_SECTION_ORDER = ['common', 'hls', 'webrtc', 'environment'] as const;

export interface FfmpegPanelProcess {
  id: string;
  purpose: string[];
  pid: number;
  startedAt: string;
  uptimeSeconds: number;
  restartCount: number;
  speed?: number | null;
  cpuPercent?: number | null;
  rssBytes?: number | null;
  gpuPercent?: number | null;
  gpuMemoryBytes?: number | null;
  gpuName?: string;
  sections: FfmpegArgSections;
  argv: string[];
  lastLogLines: string[];
}

export interface FfmpegPanel {
  processModel: string;
  processes: FfmpegPanelProcess[];
}

export function isLiveState(state: LiveState | undefined): boolean {
  return state === 'live' || state === 'degraded';
}

export function makeEmptyLiveStatus(): LiveStatus {
  return {
    state: 'offline',
    inputProtocol: '',
    streamKeyID: '',
    startTime: null,
    uptimeSeconds: 0,
    source: {
      width: null,
      height: null,
      fps: null,
      videoCodec: '',
      videoProfile: '',
      audioCodec: '',
      videoBitrateKbps: null,
      audioBitrateKbps: null,
      hasBFrames: null,
    },
    ingest: {
      bitrateKbps: null,
      packetLossPct: null,
      rttMs: null,
      retransmittedPackets: null,
      droppedPackets: null,
      detail: {},
    },
    viewers: { total: 0, webrtc: 0, hls: 0 },
    ffmpeg: { processModel: '', processes: [] },
    webrtc: { enabled: false, capacity: { used: 0, limit: 0 }, warnings: [] },
    warnings: [],
    geoip: { configured: false, databaseType: '', buildDate: '' },
    outbound: { streams: [] },
  };
}
