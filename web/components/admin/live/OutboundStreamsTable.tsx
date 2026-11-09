import { FC } from 'react';
import { Card, Space, Table, Tag, Typography } from 'antd';
import { ColumnsType } from 'antd/lib/table';

import { LiveOutboundStream } from '../../../interfaces/live-status.model';
import { Localization } from '../../../types/localization';
import { formatMetricKbps } from '../../../utils/format';
import { Translation } from '../../ui/Translation/Translation';
import { MetricValue } from './MetricValue';
import styles from './live.module.scss';

const { Text } = Typography;

export type OutboundStreamsTableProps = {
  streams: LiveOutboundStream[];
};

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

// Planned outbound streams for the active stream plan: one row per output
// variant with its planned encoders/bitrates/protocols. Null bitrates render
// N/A; the UI never estimates or shows 0/Unknown for a missing value.
export const OutboundStreamsTable: FC<OutboundStreamsTableProps> = ({ streams }) => {
  if (!streams || streams.length === 0) {
    return null;
  }

  const columns: ColumnsType<LiveOutboundStream> = [
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.outboundVariant}
          defaultText="Variant"
        />
      ),
      key: 'variant',
      render: (_, stream) => (
        <Space direction="vertical" size={0}>
          <Text strong>{'#' + (stream.variantIndex + 1)}</Text>
          {stream.name && <Text type="secondary">{stream.name}</Text>}
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
      render: (_, stream) => (
        <Space direction="vertical" size={0}>
          {renderEncoder(stream.videoEncoder)}
          <MetricValue value={formatMetricKbps(stream.videoBitrateKbps)} />
        </Space>
      ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.outboundAudio}
          defaultText="Audio"
        />
      ),
      key: 'audio',
      render: (_, stream) => (
        <Space direction="vertical" size={0}>
          {renderEncoder(stream.audioEncoder)}
          <MetricValue value={formatMetricKbps(stream.audioBitrateKbps)} />
        </Space>
      ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.outboundProtocols}
          defaultText="Protocols"
        />
      ),
      key: 'protocols',
      render: (_, stream) => (
        <Space wrap size={4}>
          {stream.protocols.map(protocol => (
            <Tag key={protocol} color={protocol === 'webrtc' ? 'blue' : 'green'}>
              {protocol === 'webrtc' ? 'WebRTC' : protocol.toUpperCase()}
            </Tag>
          ))}
        </Space>
      ),
    },
  ];

  return (
    <Card size="small" type="inner" className={styles.outboundCard} data-testid="outbound-streams">
      <Table<LiveOutboundStream>
        className={styles.outboundTable}
        size="small"
        tableLayout="auto"
        pagination={false}
        rowKey={stream => stream.variantIndex}
        columns={columns}
        dataSource={streams}
        scroll={{ x: 'max-content' }}
      />
    </Card>
  );
};

export default OutboundStreamsTable;
