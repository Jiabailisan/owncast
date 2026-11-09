import { FC, useState } from 'react';
import { Alert, Button, Card, Collapse, Empty, Space, Tag, Typography, message } from 'antd';

import {
  FFMPEG_SECTION_ORDER,
  FfmpegPanel as FfmpegPanelModel,
  FfmpegPanelProcess,
} from '../../../interfaces/live-status.model';
import { Localization, LocalizationKey } from '../../../types/localization';
import {
  formatBytes,
  formatMetricNumber,
  formatMetricPercent,
  parseSecondsToDurationString,
} from '../../../utils/format';
import { Translation } from '../../ui/Translation/Translation';
import styles from './live.module.scss';

const { Text, Paragraph } = Typography;

const SAFE_ARG = /^[a-zA-Z0-9_./:=@%+,-]+$/;

// Quote one argv element for display/copy. The argv itself is already
// redacted server-side; this only makes the copy usable in a shell.
export function quoteArgForShell(arg: string): string {
  if (arg === '') return "''";
  if (SAFE_ARG.test(arg)) return arg;
  return "'" + arg.replace(/'/g, "'\\''") + "'";
}

export function formatArgvForCopy(argv: string[]): string {
  return argv.map(quoteArgForShell).join(' ');
}

const SECTION_LABELS: Record<string, { key: LocalizationKey; defaultText: string }> = {
  common: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.common,
    defaultText: 'Common',
  },
  hls: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.hls,
    defaultText: 'HLS',
  },
  webrtc: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.webrtc,
    defaultText: 'WebRTC',
  },
  // Legacy section ids remain readable for older API payloads.
  input: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.input,
    defaultText: 'Input',
  },
  video: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.video,
    defaultText: 'Video encode',
  },
  audio: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.audio,
    defaultText: 'Audio encode',
  },
  output: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.output,
    defaultText: 'Output',
  },
  other: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.other,
    defaultText: 'Other',
  },
  environment: {
    key: Localization.Admin.LiveStreaming.Ffmpeg.environment,
    defaultText: 'Environment (redacted)',
  },
};

type FfmpegProcessCardProps = {
  process: FfmpegPanelProcess;
};

const FfmpegProcessCard: FC<FfmpegProcessCardProps> = ({ process }) => {
  const [copied, setCopied] = useState(false);

  // Per-process metrics shown next to the process id. Values are omitted when
  // the backend cannot measure them (old payloads / no progress yet).
  const speedValue =
    process.speed === null || process.speed === undefined
      ? null
      : formatMetricNumber(process.speed, 2) + 'x';
  const cpuValue =
    process.cpuPercent === null || process.cpuPercent === undefined
      ? null
      : formatMetricPercent(process.cpuPercent, 1);
  const rssValue =
    process.rssBytes === null || process.rssBytes === undefined
      ? null
      : formatBytes(process.rssBytes);
  const gpuParts: string[] = [];
  if (process.gpuPercent !== null && process.gpuPercent !== undefined) {
    gpuParts.push('GPU ' + formatMetricPercent(process.gpuPercent, 0));
  }
  if (process.gpuMemoryBytes !== null && process.gpuMemoryBytes !== undefined) {
    gpuParts.push(formatBytes(process.gpuMemoryBytes));
  }
  const gpuValue = gpuParts.length > 0 ? gpuParts.join(', ') : null;

  const handleCopy = async () => {
    const command = formatArgvForCopy(process.argv);
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(command);
      } else {
        throw new Error('Clipboard API unavailable');
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (copyError) {
      console.warn('Unable to copy the FFmpeg command:', copyError);
      message.warning('Copying to the clipboard is not available in this browser.');
    }
  };

  const sectionIds = [
    ...FFMPEG_SECTION_ORDER,
    ...Object.keys(process.sections || {}).filter(
      sectionId => !(FFMPEG_SECTION_ORDER as readonly string[]).includes(sectionId),
    ),
  ];
  const collapseItems = sectionIds.map(sectionId => {
    const lines = process.sections?.[sectionId] || [];
    const labels = SECTION_LABELS[sectionId] || {
      key: Localization.Admin.LiveStreaming.Ffmpeg.other,
      defaultText: sectionId,
    };
    return {
      key: sectionId,
      label: <Translation translationKey={labels.key} defaultText={labels.defaultText} />,
      children: (
        <Paragraph className={styles.ffmpegSection}>
          <pre>
            {lines.length > 0 ? (
              lines.map(line => (
                <code key={line} className={styles.ffmpegCodeLine}>
                  {line}
                  {'\n'}
                </code>
              ))
            ) : (
              <Text type="secondary">(empty)</Text>
            )}
          </pre>
        </Paragraph>
      ),
    };
  });

  return (
    <Card
      size="small"
      type="inner"
      className={styles.ffmpegProcess}
      title={
        <Space wrap>
          <Text strong>{process.id}</Text>
          <Tag>PID {process.pid}</Tag>
          <Tag>{parseSecondsToDurationString(process.uptimeSeconds)}</Tag>
          <Tag color={process.restartCount > 0 ? 'orange' : 'default'}>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Ffmpeg.restarts}
              defaultText="Restarts"
            />
            {': '}
            {process.restartCount}
          </Tag>
          {(process.purpose || []).map(purpose => (
            <Tag key={purpose} color="blue">
              {purpose}
            </Tag>
          ))}
          {speedValue && (
            <Tag data-testid="ffmpeg-process-speed">
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Ffmpeg.speed}
                defaultText="Speed"
              />
              {': '}
              {speedValue}
            </Tag>
          )}
          {cpuValue && (
            <Tag data-testid="ffmpeg-process-cpu">
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Ffmpeg.cpu}
                defaultText="CPU"
              />
              {': '}
              {cpuValue}
            </Tag>
          )}
          {rssValue && (
            <Tag data-testid="ffmpeg-process-rss">
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Ffmpeg.rss}
                defaultText="RSS"
              />
              {': '}
              {rssValue}
            </Tag>
          )}
          {gpuValue && (
            <Tag color="geekblue" data-testid="ffmpeg-process-gpu" title={process.gpuName}>
              {gpuValue}
            </Tag>
          )}
        </Space>
      }
      extra={
        <Button size="small" onClick={handleCopy}>
          {copied ? (
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Ffmpeg.copied}
              defaultText="Copied"
            />
          ) : (
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Ffmpeg.copy}
              defaultText="Copy command"
            />
          )}
        </Button>
      }
    >
      <Collapse size="small" items={collapseItems} />
      {process.lastLogLines && process.lastLogLines.length > 0 && (
        <details className={styles.ffmpegLogs}>
          <summary>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Ffmpeg.lastLogLines}
              defaultText="Recent log output"
            />
          </summary>
          <pre>{process.lastLogLines.join('\n')}</pre>
        </details>
      )}
    </Card>
  );
};

export type FfmpegPanelProps = {
  panel: FfmpegPanelModel | null;
};

// Admin FFmpeg command panel: every process, its exact (server-redacted) argv
// and structured argument sections. The displayed/copied argv comes from the
// command inspector, never recomputed here.
export const FfmpegPanel: FC<FfmpegPanelProps> = ({ panel }) => {
  if (!panel || panel.processes.length === 0) {
    return (
      <Card size="small" className={styles.ffmpegPanel}>
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Ffmpeg.noProcesses}
              defaultText="No FFmpeg process is running."
            />
          }
        />
      </Card>
    );
  }

  const isIsolated = panel.processModel === 'isolated';
  return (
    <div className={styles.ffmpegPanel}>
      <Alert
        type="info"
        showIcon
        className={styles.ffmpegModel}
        message={
          <>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Ffmpeg.processModel}
              defaultText="Process model"
            />
            {': '}
            {isIsolated ? (
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Ffmpeg.modelIsolated}
                defaultText="isolated (separate process per protocol)"
              />
            ) : (
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Ffmpeg.modelShared}
                defaultText="shared (one process serves all outputs)"
              />
            )}
          </>
        }
      />
      {panel.processes.map(process => (
        <FfmpegProcessCard key={process.id} process={process} />
      ))}
    </div>
  );
};

export default FfmpegPanel;
