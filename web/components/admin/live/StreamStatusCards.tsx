import { FC, ReactNode } from 'react';
import { Alert, Card, Col, Row, Typography } from 'antd';

import { LiveStatus } from '../../../interfaces/live-status.model';
import { Localization, LocalizationKey } from '../../../types/localization';
import {
  formatBytes,
  formatDisplayDate,
  formatMetricKbps,
  formatMetricNumber,
  formatMetricPercent,
  parseSecondsToDurationString,
} from '../../../utils/format';
import { Translation } from '../../ui/Translation/Translation';
import { LiveStateBadge } from './LiveStateBadge';
import { MetricValue } from './MetricValue';
import { ProtocolBadge } from './ProtocolBadge';
import styles from './live.module.scss';

const { Text } = Typography;

const NA_TOOLTIP_SOURCE =
  'This value is not reported by the source or the player, so it is unknown.';
const NA_TOOLTIP_TCP =
  'Not measurable for RTMP: the ingest transport is TCP, so there is no packet loss or RTT.';

export type StreamStatusCardsProps = {
  status: LiveStatus;
};

type StatCardProps = {
  titleKey: LocalizationKey;
  defaultTitle: string;
  children: ReactNode;
  span?: number;
};

const StatCard: FC<StatCardProps> = ({ titleKey, defaultTitle, children, span = 8 }) => (
  <Col xs={24} sm={12} lg={span}>
    <Card size="small" type="inner" className={styles.statCard}>
      <Text type="secondary" className={styles.statTitle}>
        <Translation translationKey={titleKey} defaultText={defaultTitle} />
      </Text>
      <div className={styles.statValue}>{children}</div>
    </Card>
  </Col>
);

// Stream-level status cards shared by the Live Streaming page and (where
// sensible) the admin Home page. One component, no protocol branches other
// than the ProtocolBadge: RTMP and SRT render identical fields, and unknown
// metrics always render N/A with an explanation.
export const StreamStatusCards: FC<StreamStatusCardsProps> = ({ status }) => {
  const { source, ingest, viewers, ffmpeg, webrtc, warnings, geoip } = status;
  const isTcpIngest = status.inputProtocol === 'rtmp';
  const ingestNaTooltip = isTcpIngest ? NA_TOOLTIP_TCP : NA_TOOLTIP_SOURCE;
  const totalDroppedFrames = ffmpeg.processes.reduce(
    (sum, process) => sum + (process.droppedFrames ?? 0),
    0,
  );
  const totalDuplicateFrames = ffmpeg.processes.reduce(
    (sum, process) => sum + (process.duplicateFrames ?? 0),
    0,
  );
  const maxCpuPercent = ffmpeg.processes.reduce(
    (max, process) => Math.max(max, process.cpuPercent ?? 0),
    0,
  );
  const totalRssBytes = ffmpeg.processes.reduce((sum, process) => sum + (process.rssBytes ?? 0), 0);

  return (
    <div className={styles.streamStatus}>
      <Card size="small" className={styles.overviewCard}>
        <div className={styles.overviewRow}>
          <LiveStateBadge state={status.state} />
          <ProtocolBadge protocol={status.inputProtocol} />
          {status.streamKeyID && (
            <Text type="secondary">
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Status.streamKey}
                defaultText="Key"
              />
              {': '}
              {status.streamKeyID}
            </Text>
          )}
          <Text type="secondary">
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Status.uptime}
              defaultText="Uptime"
            />
            {': '}
            <MetricValue
              value={
                status.uptimeSeconds > 0
                  ? parseSecondsToDurationString(status.uptimeSeconds)
                  : null
              }
              tooltip={NA_TOOLTIP_SOURCE}
            />
          </Text>
          <Text type="secondary">
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Status.started}
              defaultText="Started"
            />
            {': '}
            <MetricValue
              value={status.startTime ? formatDisplayDate(status.startTime) : null}
              tooltip={NA_TOOLTIP_SOURCE}
            />
          </Text>
        </div>
      </Card>

      {warnings.length > 0 && (
        <div className={styles.warnings}>
          {warnings.map(warning => (
            <Alert
              key={warning.code + warning.message}
              type="warning"
              showIcon
              message={warning.message}
              description={<Text code>{warning.code}</Text>}
            />
          ))}
        </div>
      )}

      {!geoip.configured && (
        <Alert
          type="info"
          showIcon
          className={styles.geoipNote}
          message={
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Status.geoipMissing}
              defaultText="No GeoIP database is configured: viewer locations are unavailable."
            />
          }
        />
      )}

      <Row gutter={[12, 12]}>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.resolution}
          defaultTitle="Resolution"
          span={8}
        >
          <MetricValue
            value={
              source.width !== null && source.height !== null
                ? source.width + ' x ' + source.height
                : null
            }
            tooltip={NA_TOOLTIP_SOURCE}
          />
        </StatCard>
        <StatCard titleKey={Localization.Admin.LiveStreaming.Status.fps} defaultTitle="FPS">
          <MetricValue value={formatMetricNumber(source.fps, 2)} tooltip={NA_TOOLTIP_SOURCE} />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.videoCodec}
          defaultTitle="Video codec"
        >
          <MetricValue
            value={
              source.videoCodec
                ? source.videoCodec +
                  (source.videoProfile ? ' (' + source.videoProfile + ')' : '') +
                  (source.videoLevel ? ' L' + source.videoLevel : '')
                : null
            }
            tooltip={NA_TOOLTIP_SOURCE}
          />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.audioCodec}
          defaultTitle="Audio codec"
        >
          <MetricValue value={source.audioCodec} tooltip={NA_TOOLTIP_SOURCE} />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.videoBitrate}
          defaultTitle="Video bitrate"
        >
          <MetricValue
            value={formatMetricKbps(source.videoBitrateKbps)}
            tooltip={NA_TOOLTIP_SOURCE}
          />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.audioBitrate}
          defaultTitle="Audio bitrate"
        >
          <MetricValue
            value={formatMetricKbps(source.audioBitrateKbps)}
            tooltip={NA_TOOLTIP_SOURCE}
          />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.ingestBitrate}
          defaultTitle="Ingest bitrate"
        >
          <MetricValue value={formatMetricKbps(ingest.bitrateKbps)} tooltip={NA_TOOLTIP_SOURCE} />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.packetLoss}
          defaultTitle="Packet loss"
        >
          <MetricValue
            value={formatMetricPercent(ingest.packetLossPct, 2)}
            tooltip={ingestNaTooltip}
          />
        </StatCard>
        <StatCard titleKey={Localization.Admin.LiveStreaming.Status.rtt} defaultTitle="RTT">
          <MetricValue
            value={ingest.rttMs === null ? null : formatMetricNumber(ingest.rttMs, 1) + ' ms'}
            tooltip={ingestNaTooltip}
          />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.viewersTotal}
          defaultTitle="Viewers"
        >
          <MetricValue
            value={
              formatMetricNumber(viewers.total) +
              ' (' +
              formatMetricNumber(viewers.webrtc) +
              ' WebRTC / ' +
              formatMetricNumber(viewers.hls) +
              ' HLS)'
            }
          />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.webrtcCapacity}
          defaultTitle="WebRTC capacity"
        >
          <MetricValue
            value={
              webrtc.enabled
                ? formatMetricNumber(webrtc.capacity.used) +
                  ' / ' +
                  formatMetricNumber(webrtc.capacity.limit)
                : 'Disabled'
            }
          />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.ffmpegSpeed}
          defaultTitle="FFmpeg speed"
        >
          <MetricValue
            value={
              ffmpeg.processes.length > 0
                ? formatMetricNumber(ffmpeg.processes[0].speed, 2) + 'x'
                : null
            }
            tooltip={NA_TOOLTIP_SOURCE}
          />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.droppedFrames}
          defaultTitle="Dropped frames"
        >
          <MetricValue value={formatMetricNumber(totalDroppedFrames)} />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.duplicateFrames}
          defaultTitle="Duplicate frames"
        >
          <MetricValue value={formatMetricNumber(totalDuplicateFrames)} />
        </StatCard>
        <StatCard
          titleKey={Localization.Admin.LiveStreaming.Status.ffmpegSpeed}
          defaultTitle="Processes"
        >
          <MetricValue
            value={
              ffmpeg.processes.length +
              ' (' +
              (ffmpeg.processModel || 'unknown') +
              '), CPU ' +
              formatMetricPercent(maxCpuPercent, 1) +
              ', RSS ' +
              formatBytes(totalRssBytes)
            }
          />
        </StatCard>
      </Row>
    </div>
  );
};

export default StreamStatusCards;
