import { FC, ReactNode } from 'react';
import { Card, Space, Table, Tag, Typography } from 'antd';
import { ColumnsType } from 'antd/lib/table';
import { useTranslation } from 'next-export-i18n';

import {
  LiveHLSOutboundStatus,
  LiveOutboundStream,
  LiveWebRTCStatus,
  WebRTCLegSource,
  WebRTCLegStatus,
} from '../../../interfaces/live-status.model';
import { Localization, LocalizationKey } from '../../../types/localization';
import { formatMetricKbps, formatMetricNumber } from '../../../utils/format';
import { Translation } from '../../ui/Translation/Translation';
import { MetricValue } from './MetricValue';
import styles from './live.module.scss';

const { Text } = Typography;

type Translate = (key: string, vars?: Record<string, any>) => string;

export type OutboundStreamsTableProps = {
  streams: LiveOutboundStream[];
  // Optional WebRTC status merged into the same per-variant rows so operators
  // see HLS and WebRTC decisions, video/audio bitrates, and diagnostics in one
  // unified table instead of two overlapping cards.
  webrtc?: LiveWebRTCStatus;
};

interface UnifiedOutboundRow {
  variantIndex: number;
  name?: string;
  stream?: LiveOutboundStream;
  leg?: WebRTCLegStatus;
}

// Encoders come from the stream plan: "copy" means passthrough (no transcode).
function renderEncoder(encoder?: string) {
  if (!encoder) {
    return <MetricValue value={null} />;
  }
  if (encoder.trim().toLowerCase() === 'copy') {
    return (
      <Translation
        translationKey={Localization.Admin.LiveStreaming.Status.copyPassthrough}
        defaultText="copy (passthrough)"
      />
    );
  }
  return (
    <Text code className={styles.outboundEncoder}>
      {encoder}
    </Text>
  );
}

function requestedLabelKey(requestedMode: string): LocalizationKey {
  switch (requestedMode) {
    case 'inherit':
      return Localization.Admin.VideoVariantForm.modeInherit;
    case 'transcode':
      return Localization.Admin.VideoVariantForm.modeTranscode;
    case 'copy':
      return Localization.Admin.VideoVariantForm.modeCopy;
    default:
      return Localization.Admin.LiveStreaming.Status.webrtcNotEnabled;
  }
}

function inheritedLabelKey(inheritedFrom?: string): LocalizationKey | null {
  switch (inheritedFrom) {
    case 'hls-copy':
      return Localization.Admin.LiveStreaming.Status.webrtcInheritedHlsCopy;
    case 'hls-transcode':
      return Localization.Admin.LiveStreaming.Status.webrtcInheritedHlsTranscode;
    case 'no-hls':
      return Localization.Admin.LiveStreaming.Status.webrtcInheritedNoHls;
    default:
      return null;
  }
}

function appliedLabelKey(mode: string): LocalizationKey {
  if (mode === 'copy') return Localization.Admin.LiveStreaming.Status.webrtcAppliedCopy;
  if (mode === 'transcode') return Localization.Admin.LiveStreaming.Status.webrtcAppliedTranscode;
  return Localization.Admin.LiveStreaming.Status.webrtcAppliedWithdrawn;
}

function formatFps(value: number | null | undefined): string | null {
  if (value === null || value === undefined || !Number.isFinite(value)) return null;
  return Number.isInteger(value) ? String(value) : value.toFixed(2);
}

function bFramesLabel(hasBFrames: boolean | null | undefined, t: Translate): string {
  if (hasBFrames === true) return t(Localization.Admin.LiveStreaming.Status.webrtcBFramesYes);
  if (hasBFrames === false) return t(Localization.Admin.LiveStreaming.Status.webrtcBFramesNo);
  return t(Localization.Admin.LiveStreaming.Status.webrtcBFramesUnknown);
}

function describeSource(source: WebRTCLegSource, t: Translate): string {
  const parts: string[] = [];
  const codec = [
    source.videoCodec,
    source.videoProfile,
    source.videoLevel ? 'L' + source.videoLevel : '',
  ]
    .filter(Boolean)
    .join(' ');
  if (codec) parts.push(codec);
  if (source.width && source.height) parts.push(source.width + 'x' + source.height);
  const fps = formatFps(source.fps);
  if (fps) parts.push(t(Localization.Admin.LiveStreaming.Status.webrtcSourceFps, { fps }));
  parts.push(
    t(Localization.Admin.LiveStreaming.Status.webrtcSourceBFrames, {
      value: bFramesLabel(source.hasBFrames, t),
    }),
  );
  return parts.join(', ');
}

function describeWebRTCEncoder(leg: WebRTCLegStatus, t: Translate): string | null {
  const parts: string[] = [];
  if (leg.videoBitrateKbps) {
    const peak = leg.bitratePeakKbps ?? 0;
    if (peak > leg.videoBitrateKbps) {
      parts.push(
        t(Localization.Admin.LiveStreaming.Status.webrtcEncoderWindow, {
          target: formatMetricNumber(leg.videoBitrateKbps),
          peak: formatMetricNumber(peak),
        }),
      );
    } else {
      parts.push(
        t(Localization.Admin.VideoVariantForm.bitrateValueKbps, {
          bitrate: formatMetricNumber(leg.videoBitrateKbps),
        }),
      );
    }
  }
  if (leg.scaledWidth && leg.scaledHeight) {
    parts.push(leg.scaledWidth + 'x' + leg.scaledHeight);
  }
  if (leg.gopFrames) {
    parts.push(t(Localization.Admin.LiveStreaming.Status.webrtcEncoderGop, { gop: leg.gopFrames }));
  }
  const fps = formatFps(leg.framerate);
  if (fps) parts.push(t(Localization.Admin.LiveStreaming.Status.webrtcSourceFps, { fps }));
  if (leg.h264Profile) parts.push(leg.h264Profile);
  return parts.length > 0 ? parts.join(', ') : null;
}

function describeHLSDetails(hls: LiveHLSOutboundStatus, t: Translate): string | null {
  const parts: string[] = [];
  if (hls.scaledWidth && hls.scaledHeight) {
    parts.push(hls.scaledWidth + 'x' + hls.scaledHeight);
  }
  if (hls.gopFrames) {
    parts.push(t(Localization.Admin.LiveStreaming.Status.webrtcEncoderGop, { gop: hls.gopFrames }));
  }
  const fps = formatFps(hls.framerate);
  if (fps) parts.push(t(Localization.Admin.LiveStreaming.Status.webrtcSourceFps, { fps }));
  if (hls.segmentSeconds) {
    parts.push(hls.segmentSeconds + 's seg');
  }
  return parts.length > 0 ? parts.join(', ') : null;
}

function renderNote(note: string): ReactNode {
  const separator = note.indexOf(': ');
  if (separator === -1) {
    return (
      <Text type="secondary" className={styles.webrtcNote}>
        {note}
      </Text>
    );
  }
  return (
    <Text type="secondary" className={styles.webrtcNote}>
      <Text code className={styles.webrtcNoteCode}>
        {note.slice(0, separator)}
      </Text>{' '}
      {note.slice(separator + 1)}
    </Text>
  );
}

// Unified per-variant outbound streams table: merges HLS plan/status, WebRTC
// leg decisions, video & audio encoders/bitrates, measured output bitrates, and
// diagnostics into one card so operators never have to reconcile two tables.
export const OutboundStreamsTable: FC<OutboundStreamsTableProps> = ({ streams, webrtc }) => {
  const { t } = useTranslation();
  const safeStreams = streams ?? [];
  const legs = webrtc?.legs ?? [];

  if (safeStreams.length === 0 && legs.length === 0) {
    return null;
  }

  const legByVariant = new Map<number, WebRTCLegStatus>();
  legs.forEach(leg => legByVariant.set(leg.variantIndex, leg));

  const seen = new Set<number>();
  const rows: UnifiedOutboundRow[] = [];
  safeStreams.forEach(stream => {
    seen.add(stream.variantIndex);
    rows.push({
      variantIndex: stream.variantIndex,
      name: stream.name,
      stream,
      leg: legByVariant.get(stream.variantIndex),
    });
  });
  legs.forEach(leg => {
    if (seen.has(leg.variantIndex)) return;
    rows.push({
      variantIndex: leg.variantIndex,
      name: leg.name,
      leg,
    });
  });

  const hasWebRTC = legs.length > 0;
  const hasAnyNotes = rows.some(
    row => (row.leg?.ineligibilityReasons?.length ?? 0) + (row.leg?.warnings?.length ?? 0) > 0,
  );
  const hasAnyMeasured = rows.some(
    row => row.stream?.hls?.measuredBitrateKbps !== undefined || row.leg !== undefined,
  );

  const columns: ColumnsType<UnifiedOutboundRow> = [
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.outboundVariant}
          defaultText="Variant"
        />
      ),
      key: 'variant',
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Space size={6}>
            <Text strong>{'#' + (row.variantIndex + 1)}</Text>
            {row.name && <Text strong>{row.name}</Text>}
          </Space>
          {row.leg?.source && (
            <Text
              type="secondary"
              className={styles.webrtcSource}
              data-testid={'webrtc-source-' + row.variantIndex}
            >
              {describeSource(row.leg.source, t)}
            </Text>
          )}
        </Space>
      ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.outboundVideo}
          defaultText="Video"
        />
      ),
      key: 'video',
      render: (_, row) => {
        const hls = row.stream?.hls;
        const hlsDetails = hls && hls.enabled ? describeHLSDetails(hls, t) : null;
        const webrtcEncoder =
          row.leg && row.leg.mode === 'transcode' ? describeWebRTCEncoder(row.leg, t) : null;
        return (
          <Space direction="vertical" size={2}>
            {row.stream && (
              <Space direction="vertical" size={0}>
                {renderEncoder(row.stream.videoEncoder)}
                <MetricValue value={formatMetricKbps(row.stream.videoBitrateKbps)} />
                {hlsDetails && <Text type="secondary">{hlsDetails}</Text>}
              </Space>
            )}
            {row.leg && row.leg.mode === 'copy' && (
              <Text type="secondary">
                {'WebRTC: '}
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Status.webrtcEncoderCopy}
                  defaultText="source bitstream (no re-encode)"
                />
              </Text>
            )}
            {webrtcEncoder && (
              <Text type="secondary" className={styles.webrtcEncoder}>
                {'WebRTC: ' + webrtcEncoder}
              </Text>
            )}
          </Space>
        );
      },
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.outboundAudio}
          defaultText="Audio"
        />
      ),
      key: 'audio',
      render: (_, row) => {
        const leg = row.leg;
        const hasWebRTCAudio = Boolean(leg && leg.mode !== 'disabled');
        return (
          <Space direction="vertical" size={2}>
            {row.stream && (
              <Space direction="vertical" size={0}>
                {renderEncoder(row.stream.audioEncoder)}
                <MetricValue value={formatMetricKbps(row.stream.audioBitrateKbps)} />
              </Space>
            )}
            {hasWebRTCAudio && leg && (
              <Space direction="vertical" size={0}>
                <Text type="secondary">
                  {'WebRTC: '}
                  <Text code className={styles.outboundEncoder}>
                    {leg.audioEncoder || 'libopus'}
                  </Text>
                </Text>
                {leg.audioBitrateKbps ? (
                  <Text type="secondary">{formatMetricKbps(leg.audioBitrateKbps)}</Text>
                ) : null}
              </Space>
            )}
          </Space>
        );
      },
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.outboundProtocols}
          defaultText="Protocols"
        />
      ),
      key: 'protocols',
      render: (_, row) => {
        const protocols = row.stream?.protocols ?? [];
        const hls = row.stream?.hls;
        const leg = row.leg;
        const hlsActive = hls ? hls.enabled : protocols.includes('hls');
        const inheritedKey =
          leg && leg.requestedMode === 'inherit' ? inheritedLabelKey(leg.inheritedFrom) : null;

        return (
          <Space direction="vertical" size={4}>
            {(hlsActive || hls) && (
              <Space wrap size={4}>
                <Tag color={hlsActive ? 'green' : 'default'}>HLS</Tag>
                {hls && (
                  <Tag
                    color={
                      hls.mode === 'copy' ? 'green' : hls.mode === 'transcode' ? 'blue' : 'default'
                    }
                  >
                    <Translation translationKey={appliedLabelKey(hls.mode)} />
                  </Tag>
                )}
              </Space>
            )}
            {leg ? (
              <Space direction="vertical" size={0}>
                <Space wrap size={4}>
                  <Tag color={leg.mode === 'disabled' ? 'default' : 'blue'}>WebRTC</Tag>
                  <Tag
                    color={
                      leg.mode === 'copy' ? 'green' : leg.mode === 'transcode' ? 'blue' : 'default'
                    }
                  >
                    <Translation translationKey={appliedLabelKey(leg.mode)} />
                  </Tag>
                </Space>
                <Text type="secondary">
                  <Translation translationKey={requestedLabelKey(leg.requestedMode)} />
                  {inheritedKey && (
                    <>
                      {' ('}
                      <Translation translationKey={inheritedKey} />
                      {' -> '}
                      <Translation translationKey={appliedLabelKey(leg.mode)} />
                      {')'}
                    </>
                  )}
                </Text>
              </Space>
            ) : (
              protocols
                .filter(protocol => protocol !== 'hls')
                .map(protocol => (
                  <Tag key={protocol} color={protocol === 'webrtc' ? 'blue' : 'green'}>
                    {protocol === 'webrtc' ? 'WebRTC' : protocol.toUpperCase()}
                  </Tag>
                ))
            )}
          </Space>
        );
      },
    },
  ];

  if (hasAnyMeasured) {
    columns.push({
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcMeasured}
          defaultText="Measured"
        />
      ),
      key: 'measured',
      render: (_, row) => {
        const hlsMeasured = row.stream?.hls?.measuredBitrateKbps;
        const webrtcVideo = row.leg?.outputBitrateKbps;
        const webrtcAudio = row.leg?.outputAudioBitrateKbps;
        return (
          <Space direction="vertical" size={2}>
            {row.stream?.hls?.enabled && (
              <div>
                <Text type="secondary">{'HLS: '}</Text>
                <MetricValue value={formatMetricKbps(hlsMeasured)} />
              </div>
            )}
            {row.leg && row.leg.mode !== 'disabled' && (
              <>
                <div>
                  <Text type="secondary">{'WebRTC V: '}</Text>
                  <MetricValue
                    value={
                      webrtcVideo === null || webrtcVideo === undefined
                        ? null
                        : formatMetricKbps(webrtcVideo)
                    }
                  />
                </div>
                <div>
                  <Text type="secondary">{'WebRTC A: '}</Text>
                  <MetricValue
                    value={
                      webrtcAudio === null || webrtcAudio === undefined
                        ? null
                        : formatMetricKbps(webrtcAudio)
                    }
                  />
                </div>
              </>
            )}
          </Space>
        );
      },
    });
  }

  if (hasAnyNotes) {
    columns.push({
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcNotes}
          defaultText="Notes"
        />
      ),
      key: 'notes',
      render: (_, row) => {
        const notes = [...(row.leg?.ineligibilityReasons ?? []), ...(row.leg?.warnings ?? [])];
        if (notes.length === 0) return null;
        return (
          <Space direction="vertical" size={0} className={styles.webrtcNotes}>
            {notes.map(note => (
              <div key={note}>{renderNote(note)}</div>
            ))}
          </Space>
        );
      },
    });
  }

  const advertised = webrtc?.advertised;

  return (
    <Card size="small" type="inner" className={styles.outboundCard} data-testid="outbound-streams">
      {hasWebRTC && (
        <div className={styles.webrtcExplanation}>
          <Translation
            translationKey={Localization.Admin.LiveStreaming.Status.webrtcExplanation}
            defaultText="Three H.264 profile and level values matter here. The source profile and level come from the ingest bitstream. The encoder profile is used when a WebRTC rendition is transcoded. Copy (passthrough) legs negotiate per-leg against the source bitstream, while transcoded legs advertise the configured WHEP profile-level-id."
          />
          {advertised && (
            <div className={styles.webrtcAdvertised} data-testid="webrtc-advertised">
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Status.webrtcAdvertised}
                vars={{ profileLevelId: advertised.profileLevelId, level: advertised.level }}
                defaultText="Default transcoded WHEP answer: {{profileLevelId}} (level {{level}}; copy legs negotiate per source)"
              />
            </div>
          )}
        </div>
      )}
      <Table<UnifiedOutboundRow>
        className={styles.outboundTable}
        size="small"
        tableLayout="auto"
        pagination={false}
        rowKey={row => row.variantIndex}
        columns={columns}
        dataSource={rows}
        scroll={{ x: 'max-content' }}
      />
    </Card>
  );
};

