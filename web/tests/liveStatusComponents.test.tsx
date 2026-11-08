import { render, screen } from '@testing-library/react';

import { LiveStateBadge } from '../components/admin/live/LiveStateBadge';
import { ProtocolBadge } from '../components/admin/live/ProtocolBadge';
import { StreamStatusCards } from '../components/admin/live/StreamStatusCards';
import { FfmpegPanel, formatArgvForCopy } from '../components/admin/live/FfmpegPanel';
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
        sections: {
          input: ['-i', 'pipe:0'],
          video: ['-c:v:0', 'libx264'],
          audio: ['-c:a:0', 'copy'],
          output: ['-f', 'hls'],
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

  test('renders the empty state when no process runs', () => {
    render(<FfmpegPanel panel={{ processModel: 'shared', processes: [] }} />);
    expect(screen.getByText(/No FFmpeg process is running/i)).toBeInTheDocument();
  });
});
