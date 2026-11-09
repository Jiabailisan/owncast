import { render, screen } from '@testing-library/react';

import { LiveStateBadge } from '../components/admin/live/LiveStateBadge';
import { ProtocolBadge } from '../components/admin/live/ProtocolBadge';
import {
  StreamStatusCards,
  describeAudioCodec,
  describeAudioSampleRate,
} from '../components/admin/live/StreamStatusCards';
import { FfmpegPanel, formatArgvForCopy } from '../components/admin/live/FfmpegPanel';
import { OutboundStreamsTable } from '../components/admin/live/OutboundStreamsTable';
import { makeEmptyLiveStatus } from '../interfaces/live-status.model';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const makeStatus = (overrides: Record<string, unknown> = {}) => ({
  ...makeEmptyLiveStatus(),
  state: 'live' as const,
  inputProtocol: 'srt' as const,
  streamKeyID: 'Default stream key',
  startTime: '2026-11-09T03:00:00Z',
  uptimeSeconds: 120,
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
    retransmittedPackets: 1,
    droppedPackets: 0,
    detail: {},
  },
  viewers: { total: 3, webrtc: 1, hls: 2 },
  ...overrides,
});

describe('live status primitives', () => {
  test('LiveStateBadge uses text and an icon, not color alone', () => {
    render(<LiveStateBadge state="live" />);
    const badge = screen.getByRole('status');
    expect(badge).toHaveTextContent('Live');
    expect(badge.querySelector('svg, .anticon')).not.toBeNull();
  });

  test('ProtocolBadge renders the ingest protocol', () => {
    const { rerender } = render(<ProtocolBadge protocol="srt" />);
    expect(screen.getByText('SRT')).toBeInTheDocument();
    rerender(<ProtocolBadge protocol="rtmp" />);
    expect(screen.getByText('RTMP')).toBeInTheDocument();
    rerender(<ProtocolBadge protocol="" />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });
});

describe('StreamStatusCards', () => {
  test('renders SRT ingest stats and viewer breakdown', () => {
    render(<StreamStatusCards status={makeStatus()} />);
    expect(screen.getByText('SRT')).toBeInTheDocument();
    expect(screen.getByText('1920 x 1080')).toBeInTheDocument();
    expect(screen.getByText('3 (1 WebRTC / 2 HLS)')).toBeInTheDocument();
    expect(screen.getByText('2510 kbps')).toBeInTheDocument();
    expect(screen.getByText('0.20%')).toBeInTheDocument();
  });

  test('renders N/A for RTMP TCP metrics it cannot measure', () => {
    render(
      <StreamStatusCards
        status={makeStatus({
          inputProtocol: 'rtmp',
          ingest: {
            bitrateKbps: 2510,
            packetLossPct: null,
            rttMs: null,
            retransmittedPackets: null,
            droppedPackets: null,
            detail: {},
          },
        })}
      />,
    );
    expect(screen.getByText('RTMP')).toBeInTheDocument();
    expect(screen.getAllByText('N/A').length).toBeGreaterThan(0);
  });

  test('shows warnings and the missing-GeoIP note', () => {
    render(
      <StreamStatusCards
        status={makeStatus({
          warnings: [{ code: 'copy_fallback', message: 'Falling back to transcode' }],
        })}
      />,
    );
    expect(screen.getByText('Falling back to transcode')).toBeInTheDocument();
    expect(screen.getByText(/No GeoIP database is configured/i)).toBeInTheDocument();
  });
});

describe('SRT audio codec details', () => {
  test('formats the AAC profile, sample rate and channel layout', () => {
    const source = { ...makeEmptyLiveStatus().source, audioCodec: 'aac', audioProfile: 'LC' };
    expect(describeAudioCodec(source)).toBe('AAC-LC');
    expect(describeAudioCodec({ ...source, audioProfile: 'HE-AAC' })).toBe('AAC-HE-AAC');
    expect(describeAudioCodec({ ...source, audioCodec: 'mp3', audioProfile: '' })).toBe('mp3');
    expect(describeAudioCodec(makeEmptyLiveStatus().source)).toBeNull();

    expect(describeAudioSampleRate(48000)).toBe('48 kHz');
    expect(describeAudioSampleRate(44100)).toBe('44.1 kHz');
    expect(describeAudioSampleRate(22050)).toBe('22.05 kHz');
    expect(describeAudioSampleRate(0)).toBeNull();
    expect(describeAudioSampleRate(undefined)).toBeNull();
  });

  test('renders the combined audio detail for SRT sources', () => {
    render(
      <StreamStatusCards
        status={makeStatus({
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
        })}
      />,
    );
    expect(screen.getByTestId('source-audio-detail')).toHaveTextContent('AAC-LC');
    expect(screen.getByTestId('source-audio-detail')).toHaveTextContent('48 kHz');
    expect(screen.getByTestId('source-audio-detail')).toHaveTextContent('stereo');
  });

  test('keeps the codec-only rendering when the audio details are absent (RTMP)', () => {
    render(<StreamStatusCards status={makeStatus({ inputProtocol: 'rtmp' })} />);
    expect(screen.getByText('aac')).toBeInTheDocument();
    expect(screen.queryByText(/kHz/)).not.toBeInTheDocument();
    expect(screen.queryByText('stereo')).not.toBeInTheDocument();
  });

  test('renders mono for single-channel sources', () => {
    render(
      <StreamStatusCards
        status={makeStatus({
          source: {
            width: 1920,
            height: 1080,
            fps: 30,
            videoCodec: 'h264',
            audioCodec: 'aac',
            audioProfile: 'LC',
            audioSampleRate: 48000,
            audioChannels: 1,
            videoBitrateKbps: 2500,
            audioBitrateKbps: 64,
          },
        })}
      />,
    );
    expect(screen.getByTestId('source-audio-detail')).toHaveTextContent('mono');
  });
});

describe('GeoIP dual sources', () => {
  const geoip = {
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
  };

  test('renders the primary and secondary database lines with the merge explanation', () => {
    render(<StreamStatusCards status={makeStatus({ geoip })} />);
    expect(screen.getByTestId('geoip-sources')).toBeInTheDocument();
    expect(screen.getByTestId('geoip-source-primary')).toHaveTextContent('GeoLite2-City');
    expect(screen.getByTestId('geoip-source-primary')).toHaveTextContent('data/GeoLite2-City.mmdb');
    expect(screen.getByTestId('geoip-source-secondary')).toHaveTextContent('DBIP-City-Lite');
    expect(screen.getByTestId('geoip-source-secondary')).toHaveTextContent(
      'data/dbip-city-lite.mmdb',
    );
    expect(screen.getByText(/missing city data is filled in/i)).toBeInTheDocument();
    // Configured GeoIP means no missing-database note.
    expect(screen.queryByText(/No GeoIP database is configured/i)).not.toBeInTheDocument();
  });

  test('falls back to the missing-GeoIP note when there are no sources', () => {
    render(<StreamStatusCards status={makeStatus({ geoip: { configured: false } })} />);
    expect(screen.queryByTestId('geoip-sources')).not.toBeInTheDocument();
    expect(screen.getByText(/No GeoIP database is configured/i)).toBeInTheDocument();
  });
});

describe('OutboundStreamsTable', () => {
  const streams = [
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
      protocols: ['hls'],
    },
  ];

  test('renders per-variant encoders, bitrates and protocol badges', () => {
    render(<OutboundStreamsTable streams={streams} />);
    expect(screen.getByTestId('outbound-streams')).toBeInTheDocument();
    expect(screen.getByText('#1')).toBeInTheDocument();
    expect(screen.getByText('#2')).toBeInTheDocument();
    expect(screen.getByText('720p @1200 kbps')).toBeInTheDocument();
    expect(screen.getByText('libx264')).toBeInTheDocument();
    expect(screen.getByText('1200 kbps')).toBeInTheDocument();
    expect(screen.getByText('128 kbps')).toBeInTheDocument();
    expect(screen.getAllByText('HLS').length).toBe(2);
    expect(screen.getByText('WebRTC')).toBeInTheDocument();
    expect(screen.getAllByText('copy (passthrough)').length).toBe(2);
  });

  test('renders N/A (never 0) for unknown planned bitrates', () => {
    render(<OutboundStreamsTable streams={streams} />);
    expect(screen.getAllByText('N/A').length).toBe(2);
  });

  test('renders nothing when the plan has no outbound streams', () => {
    const { container } = render(<OutboundStreamsTable streams={[]} />);
    expect(container).toBeEmptyDOMElement();
    render(<OutboundStreamsTable streams={undefined as never} />);
  });
});

describe('StreamStatusCards process metrics', () => {
  test('shows speed as N/A (never "N/Ax") when the backend has no value', () => {
    render(
      <StreamStatusCards
        status={makeStatus({
          ffmpeg: {
            processModel: 'shared',
            processes: [
              {
                id: 'main',
                pid: 4242,
                uptimeSeconds: 10,
                restartCount: 0,
                speed: null,
                droppedFrames: null,
                duplicateFrames: null,
                cpuPercent: 12.5,
                rssBytes: 104857600,
              },
            ],
          },
        })}
      />,
    );
    expect(screen.queryByText('N/Ax')).not.toBeInTheDocument();
  });

  test('includes GPU usage in the Processes summary when provided', () => {
    render(
      <StreamStatusCards
        status={makeStatus({
          ffmpeg: {
            processModel: 'shared',
            processes: [
              {
                id: 'main',
                pid: 4242,
                uptimeSeconds: 10,
                restartCount: 0,
                speed: 0.98,
                droppedFrames: 0,
                duplicateFrames: 0,
                cpuPercent: 112.5,
                rssBytes: 104857600,
                gpuPercent: 12,
                gpuMemoryBytes: 335544320,
              },
            ],
          },
        })}
      />,
    );
    expect(screen.getByText(/GPU 12%/)).toBeInTheDocument();
    expect(screen.getByText(/320\.0 MB/)).toBeInTheDocument();
  });
});

describe('FfmpegPanel', () => {
  const panel = {
    processModel: 'shared',
    processes: [
      {
        id: 'main',
        purpose: ['hls'],
        pid: 4242,
        startedAt: '2026-11-09T03:00:00Z',
        uptimeSeconds: 120,
        restartCount: 2,
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
        argv: ['ffmpeg', '-hide_banner'],
        lastLogLines: ['frame= 100'],
      },
    ],
  };

  test('formats argv for shell-safe copying', () => {
    expect(formatArgvForCopy(['ffmpeg', '-i', 'pipe:0'])).toBe('ffmpeg -i pipe:0');
    expect(formatArgvForCopy(['-metadata', 'title=my stream'])).toBe("-metadata 'title=my stream'");
    expect(formatArgvForCopy(["a'b"])).toBe("'a'\\''b'");
  });

  test('renders the process, model and copy action', () => {
    render(<FfmpegPanel panel={panel} />);
    expect(screen.getByText('main')).toBeInTheDocument();
    expect(screen.getByText('PID 4242')).toBeInTheDocument();
    expect(screen.getByText('Copy command')).toBeInTheDocument();
    expect(screen.getByText(/shared/i)).toBeInTheDocument();
  });

  test('renders the simplified section groups and process metrics', () => {
    render(<FfmpegPanel panel={panel} />);
    // New backend section keys replace input/video/audio/output/other.
    expect(screen.getByText('Common')).toBeInTheDocument();
    expect(screen.getByText('HLS')).toBeInTheDocument();
    expect(screen.getByText('WebRTC')).toBeInTheDocument();
    expect(screen.getByText(/Environment \(redacted\)/i)).toBeInTheDocument();
    expect(screen.queryByText('Input')).not.toBeInTheDocument();
    // Per-process Speed/CPU/RSS/GPU values.
    expect(screen.getByTestId('ffmpeg-process-speed')).toHaveTextContent(/0\.98x/);
    expect(screen.getByTestId('ffmpeg-process-cpu')).toHaveTextContent(/112\.5%/);
    expect(screen.getByTestId('ffmpeg-process-rss')).toHaveTextContent(/100\.0 MB/);
    expect(screen.getByTestId('ffmpeg-process-gpu')).toHaveTextContent(/GPU 12%/);
    expect(screen.getByTestId('ffmpeg-process-gpu')).toHaveTextContent(/320\.0 MB/);
  });

  test('renders the empty state when no process runs', () => {
    render(<FfmpegPanel panel={{ processModel: 'shared', processes: [] }} />);
    expect(screen.getByText(/No FFmpeg process is running/i)).toBeInTheDocument();
  });
});
