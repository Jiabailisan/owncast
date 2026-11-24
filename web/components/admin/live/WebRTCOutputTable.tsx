import { FC, ReactNode } from 'react';
import { Card, Space, Table, Tag, Typography } from 'antd';
import { ColumnsType } from 'antd/lib/table';
import { useTranslation } from 'next-export-i18n';

import {
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

export type WebRTCOutputTableProps = {
  // The WebRTC block of the live status payload: one leg per exposed variant
  // plus the H.264 format the WHEP answer advertises.
  webrtc: LiveWebRTCStatus;
};

// Requested mode as the operator configured it; an empty value means the
// variant is not exposed over WebRTC at all.
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

// What an "inherit" leg resolved against. "no-hls" means the variant has no
// HLS output to follow, so the inheritance had nothing to copy from.
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

// The mode that was actually applied; anything that is not copy or transcode
// means the rendition was withdrawn from delivery.
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

// "<codec> <profile> L<level>, <W>x<H>, <fps> fps, B-frames: yes/no/unknown".
// Unknown parts are omitted rather than guessed; B-frames always renders so an
// operator can tell "no B-frames" from "not inspected".
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

// Transcode settings only: "<target>-<peak> kbps, GOP <n>, <fps> fps, <profile>".
// The window is shown as a range because the encoder is allowed to use it; a
// single value means the leg is pinned to its target.
// Copy legs re-encode nothing, so they show the source bitstream instead.
function describeEncoder(leg: WebRTCLegStatus, t: Translate): string | null {
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
  if (leg.gopFrames) {
    parts.push(t(Localization.Admin.LiveStreaming.Status.webrtcEncoderGop, { gop: leg.gopFrames }));
  }
  const fps = formatFps(leg.framerate);
  if (fps) parts.push(t(Localization.Admin.LiveStreaming.Status.webrtcSourceFps, { fps }));
  if (leg.h264Profile) parts.push(leg.h264Profile);
  return parts.length > 0 ? parts.join(', ') : null;
}

// Ineligibility reasons and warnings carry a stable code prefix followed by an
// operator-facing sentence ("code: message"). Keep the code visible: it is what
// an operator greps for and what a bug report should quote.
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

// Per-variant WebRTC delivery decisions for the current stream: what was
// requested, what was applied, the source properties the decision used, the
// encoder settings a transcode leg will use, the measured output bitrate and
// every reason a copy request could not be honored.
export const WebRTCOutputTable: FC<WebRTCOutputTableProps> = ({ webrtc }) => {
  const { t } = useTranslation();
  const legs = webrtc?.legs ?? [];

  if (legs.length === 0) {
    return null;
  }

  const advertised = webrtc.advertised;

  const columns: ColumnsType<WebRTCLegStatus> = [
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcVariant}
          defaultText="Variant"
        />
      ),
      key: 'variant',
      render: (_, leg) => (
        <Space direction="vertical" size={0}>
          <Text strong>
            {leg.name ||
              t(Localization.Admin.LiveStreaming.Status.webrtcVariantLabel, {
                index: leg.variantIndex + 1,
              })}
          </Text>
          <Text type="secondary">{'#' + (leg.variantIndex + 1)}</Text>
        </Space>
      ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcRequested}
          defaultText="Requested"
        />
      ),
      key: 'requested',
      render: (_, leg) => {
        const inheritedKey =
          leg.requestedMode === 'inherit' ? inheritedLabelKey(leg.inheritedFrom) : null;
        return (
          <Space direction="vertical" size={0}>
            <Translation translationKey={requestedLabelKey(leg.requestedMode)} />
            {inheritedKey && (
              <Text type="secondary">
                <Translation translationKey={inheritedKey} />
                {' → '}
                <Translation translationKey={appliedLabelKey(leg.mode)} />
              </Text>
            )}
          </Space>
        );
      },
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcApplied}
          defaultText="Applied"
        />
      ),
      key: 'applied',
      render: (_, leg) => (
        <Tag color={leg.mode === 'copy' ? 'green' : leg.mode === 'transcode' ? 'blue' : 'default'}>
          <Translation translationKey={appliedLabelKey(leg.mode)} />
        </Tag>
      ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcSourceColumn}
          defaultText="Source"
        />
      ),
      key: 'source',
      render: (_, leg) => (
        <span className={styles.webrtcSource} data-testid={'webrtc-source-' + leg.variantIndex}>
          {describeSource(leg.source, t)}
        </span>
      ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcEncoder}
          defaultText="Encoder"
        />
      ),
      key: 'encoder',
      render: (_, leg) => {
        if (leg.mode === 'copy') {
          return (
            <Text type="secondary">
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Status.webrtcEncoderCopy}
                defaultText="source bitstream (no re-encode)"
              />
            </Text>
          );
        }
        const encoder = describeEncoder(leg, t);
        if (!encoder) return <MetricValue value={null} />;
        return <span className={styles.webrtcEncoder}>{encoder}</span>;
      },
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcMeasured}
          defaultText="Measured"
        />
      ),
      key: 'measured',
      render: (_, leg) => (
        <MetricValue
          value={
            leg.outputBitrateKbps === null || leg.outputBitrateKbps === undefined
              ? null
              : formatMetricKbps(leg.outputBitrateKbps)
          }
        />
      ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcNotes}
          defaultText="Notes"
        />
      ),
      key: 'notes',
      render: (_, leg) => {
        const notes = [...(leg.ineligibilityReasons ?? []), ...(leg.warnings ?? [])];
        if (notes.length === 0) return null;
        return (
          <Space direction="vertical" size={0} className={styles.webrtcNotes}>
            {notes.map(note => (
              <div key={note}>{renderNote(note)}</div>
            ))}
          </Space>
        );
      },
    },
  ];

  return (
    <Card size="small" type="inner" className={styles.webrtcCard} data-testid="webrtc-outputs">
      <div className={styles.webrtcExplanation}>
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.webrtcExplanation}
          defaultText="Three H.264 profile and level values matter here. The source profile and level come from the ingest bitstream, so they describe what the publisher sent. The encoder profile is what Owncast encodes with when a WebRTC rendition is transcoded. The advertised profile-level-id is what the browser negotiates in the WHEP answer, and it can differ from both of the other values."
        />
        {advertised && (
          <div className={styles.webrtcAdvertised} data-testid="webrtc-advertised">
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Status.webrtcAdvertised}
              vars={{ profileLevelId: advertised.profileLevelId, level: advertised.level }}
              defaultText="Advertised in the WHEP answer: {{profileLevelId}} (level {{level}})"
            />
          </div>
        )}
      </div>
      <Table<WebRTCLegStatus>
        className={styles.webrtcTable}
        size="small"
        tableLayout="auto"
        pagination={false}
        rowKey={leg => leg.variantIndex}
        columns={columns}
        dataSource={legs}
        scroll={{ x: 'max-content' }}
      />
    </Card>
  );
};

export default WebRTCOutputTable;
