import { FC, useEffect, useRef, useState } from 'react';
import { Alert, Card, Input, Select, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/lib/table';
import { formatDistanceToNow } from 'date-fns';

import LiveStatusService from '../../../services/live-status-service';
import {
  LiveViewer,
  LiveViewersQuery,
  LiveViewersResponse,
} from '../../../interfaces/live-status.model';
import { Localization } from '../../../types/localization';
import {
  formatMetricKbps,
  formatMetricNumber,
  formatMetricPercent,
  parseSecondsToDurationString,
} from '../../../utils/format';
import { MOBILE_BREAKPOINT_QUERY, useMediaQuery } from '../../../hooks/useMediaQuery';
import { Translation } from '../../ui/Translation/Translation';
import { VIEWER_REFRESH_MS } from '../../../utils/viewer-report';
import styles from './live.module.scss';

const { Text } = Typography;

const PAGE_SIZE = 25;

// Placeholders that must never be joined into the location string. GeoIP reports
// "N/A" for viewers without a public location (viewers on the server's own
// network are labelled "Local"), so joining naively produced "Local, N/A".
const LOCATION_PLACEHOLDERS = new Set(['n/a', 'unknown', '']);

// viewerLocationText renders the location column/card value, or null when the
// viewer has no usable location information.
export function viewerLocationText(viewer: LiveViewer): string | null {
  const parts = [viewer.city, viewer.region, viewer.country]
    .map(part => (typeof part === 'string' ? part.trim() : ''))
    .filter(part => !LOCATION_PLACEHOLDERS.has(part.toLowerCase()));

  return parts.length > 0 ? parts.join(', ') : null;
}

export type LiveViewerTableProps = {
  enabled: boolean;
};

const SORT_FIELD_MAP: Record<string, LiveViewersQuery['sort']> = {
  durationSeconds: 'duration',
  bitrateKbps: 'bitrate',
  protocol: 'protocol',
  connectedAt: 'connectedAt',
};

const DEFAULT_QUERY: LiveViewersQuery = {
  page: 1,
  pageSize: PAGE_SIZE,
  sort: 'duration',
  order: 'desc',
  protocol: 'all',
  q: '',
};

const na = (tooltip: string, value: string | null) => (
  <Tooltip title={value === null ? tooltip : undefined}>
    <span>{value === null ? 'N/A' : value}</span>
  </Tooltip>
);

// WebRTC viewers report the ICE transport that was actually selected: UDP is
// the low-latency path, TCP the fallback. Unknown transports keep the plain
// WebRTC label.
export const renderViewerProtocol = (viewer: LiveViewer) => {
  if (viewer.protocol !== 'webrtc') {
    return <Tag color="purple">HLS</Tag>;
  }
  if (viewer.iceTransport === 'udp') {
    return <Tag color="geekblue">WebRTC-U</Tag>;
  }
  if (viewer.iceTransport === 'tcp') {
    return <Tag color="geekblue">WebRTC-T</Tag>;
  }
  return <Tag color="geekblue">WebRTC</Tag>;
};

// One metric cell with consistent provenance: player-reported values are only
// shown when the backend validated them; otherwise the value is server-measured
// or honestly N/A. The tooltip always says where the number came from.
export const renderViewerMetric = (
  viewer: LiveViewer,
  value: string | null,
  missingReason: string,
) => {
  const sourceText =
    viewer.source === 'client'
      ? 'Reported by the player and validated by the server.'
      : 'Measured by the server.';
  return (
    <Tooltip title={value === null ? missingReason : sourceText}>
      <span>{value === null ? 'N/A' : value}</span>
    </Tooltip>
  );
};

// Per-viewer table for the admin Live Stream Status section. Server-paged and
// sortable/filterable; switches to a card list on narrow viewports. Only
// server-known values are shown, or client-reported ones when the player sends
// them; unknown is always N/A.
export const LiveViewerTable: FC<LiveViewerTableProps> = ({ enabled }) => {
  const isMobile = useMediaQuery(MOBILE_BREAKPOINT_QUERY);
  const [query, setQuery] = useState<LiveViewersQuery>({ ...DEFAULT_QUERY });
  const [data, setData] = useState<LiveViewersResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshToken, setRefreshToken] = useState(0);
  const warnedRef = useRef(false);

  useEffect(() => {
    if (!enabled) {
      setData(null);
      setError(null);
      return undefined;
    }
    const controller = new AbortController();
    let cancelled = false;

    LiveStatusService.getViewers(query, { signal: controller.signal })
      .then(result => {
        if (cancelled) return;
        warnedRef.current = false;
        setData(result);
        setError(null);
      })
      .catch((viewersError: Error) => {
        if (cancelled || controller.signal.aborted) return;
        if (!warnedRef.current) {
          warnedRef.current = true;
          console.warn('Live viewers request failed:', viewersError?.message);
        }
        setError(viewersError?.message || 'Unable to load viewers');
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [enabled, query, refreshToken]);

  useEffect(() => {
    if (!enabled) return undefined;
    const timer = setInterval(() => {
      if (document.visibilityState === 'hidden') return;
      setRefreshToken(token => token + 1);
    }, VIEWER_REFRESH_MS);
    return () => clearInterval(timer);
  }, [enabled]);

  const columns: ColumnsType<LiveViewer> = [
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.username}
          defaultText="Viewer"
        />
      ),
      dataIndex: 'username',
      key: 'username',
      render: (username: string | undefined) =>
        username ? <Text strong>{username}</Text> : na('This viewer has no chat identity.', null),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.ip}
          defaultText="IP"
        />
      ),
      dataIndex: 'ipAddress',
      key: 'ipAddress',
      render: (ipAddress: string) => ipAddress || 'N/A',
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.location}
          defaultText="Location"
        />
      ),
      key: 'location',
      render: (_, viewer) => {
        const location = viewerLocationText(viewer);
        if (location === null) return na('GeoIP lookup unavailable for this viewer.', null);
        return (
          <Tooltip title={viewer.locationApproximate ? 'Location is approximate.' : undefined}>
            <span>{location}</span>
          </Tooltip>
        );
      },
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.protocol}
          defaultText="Protocol"
        />
      ),
      dataIndex: 'protocol',
      key: 'protocol',
      render: (_protocol: LiveViewer['protocol'], viewer) => renderViewerProtocol(viewer),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.quality}
          defaultText="Quality"
        />
      ),
      dataIndex: 'quality',
      key: 'quality',
      render: (quality: string | undefined) =>
        quality ? quality : na('The player did not report which quality it is playing.', null),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.resolution}
          defaultText="Resolution"
        />
      ),
      dataIndex: 'resolution',
      key: 'resolution',
      render: (value: string | null, viewer) =>
        renderViewerMetric(
          viewer,
          value,
          'No resolution measurement is available for this viewer yet.',
        ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.fps}
          defaultText="FPS"
        />
      ),
      dataIndex: 'fps',
      key: 'fps',
      render: (value: number | null, viewer) =>
        renderViewerMetric(
          viewer,
          value === null ? null : formatMetricNumber(value, 1),
          'No FPS measurement is available for this viewer yet.',
        ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.bitrate}
          defaultText="Bitrate"
        />
      ),
      dataIndex: 'bitrateKbps',
      key: 'bitrateKbps',
      sorter: true,
      render: (value: number | null, viewer) =>
        renderViewerMetric(
          viewer,
          value === null ? null : formatMetricKbps(value),
          'No bitrate measurement is available for this viewer yet.',
        ),
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.latency}
          defaultText="Latency"
        />
      ),
      dataIndex: 'latencyMs',
      key: 'latencyMs',
      render: (latencyMs: number | null) => {
        if (latencyMs === null || latencyMs === undefined) {
          return na('The player did not report a latency measurement.', null);
        }

        return `${formatMetricNumber(latencyMs / 1000, 2)} s`;
      },
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.packetLoss}
          defaultText="Packet loss"
        />
      ),
      dataIndex: 'packetLossPct',
      key: 'packetLossPct',
      render: (value: number | null, viewer) => {
        const hlsMissing =
          viewer.protocol === 'hls'
            ? 'HTTP/TCP playback has no packet loss to measure.'
            : 'No packet-loss measurement is available for this viewer yet.';
        return renderViewerMetric(
          viewer,
          value === null ? null : formatMetricPercent(value, 2),
          hlsMissing,
        );
      },
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.connected}
          defaultText="Connected"
        />
      ),
      dataIndex: 'connectedAt',
      key: 'connectedAt',
      sorter: true,
      render: (value: string) =>
        value ? formatDistanceToNow(new Date(value), { addSuffix: true }) : 'N/A',
    },
    {
      title: (
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Viewers.duration}
          defaultText="Duration"
        />
      ),
      dataIndex: 'durationSeconds',
      key: 'durationSeconds',
      sorter: true,
      render: (value: number | null) =>
        value === null ? 'N/A' : parseSecondsToDurationString(value),
    },
    {
      title: '',
      key: 'source',
      render: (_, viewer) => (
        <Tag color={viewer.source === 'client' ? 'blue' : 'default'}>
          <Translation
            translationKey={
              viewer.source === 'client'
                ? Localization.Admin.LiveStreaming.Viewers.clientReported
                : Localization.Admin.LiveStreaming.Viewers.serverMeasured
            }
            defaultText={viewer.source === 'client' ? 'Player reported' : 'Server measured'}
          />
        </Tag>
      ),
    },
  ];

  const mobileCards = (
    <div className={styles.viewerCards}>
      {(data?.viewers || []).map(viewer => {
        const locationText = viewerLocationText(viewer) || 'N/A';
        return (
          <Card key={viewer.clientID} size="small" className={styles.viewerCard}>
            <div className={styles.viewerCardHeader}>
              <Text strong>{viewer.ipAddress || 'N/A'}</Text>
              <Space size={4} wrap>
                {renderViewerProtocol(viewer)}
                <Tag color={viewer.source === 'client' ? 'blue' : 'default'}>
                  <Translation
                    translationKey={
                      viewer.source === 'client'
                        ? Localization.Admin.LiveStreaming.Viewers.clientReported
                        : Localization.Admin.LiveStreaming.Viewers.serverMeasured
                    }
                    defaultText={viewer.source === 'client' ? 'Player reported' : 'Server measured'}
                  />
                </Tag>
              </Space>
            </div>
            <div className={styles.viewerCardRow}>
              <Text type="secondary">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Viewers.location}
                  defaultText="Location"
                />
              </Text>
              <span>{locationText}</span>
            </div>
            <div className={styles.viewerCardRow}>
              <Text type="secondary">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Viewers.bitrate}
                  defaultText="Bitrate"
                />
              </Text>
              {renderViewerMetric(
                viewer,
                viewer.bitrateKbps === null ? null : formatMetricKbps(viewer.bitrateKbps),
                'No bitrate measurement is available for this viewer yet.',
              )}
            </div>
            <div className={styles.viewerCardRow}>
              <Text type="secondary">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Viewers.resolution}
                  defaultText="Resolution"
                />
              </Text>
              {renderViewerMetric(
                viewer,
                viewer.resolution || null,
                'No resolution measurement is available for this viewer yet.',
              )}
            </div>
            <div className={styles.viewerCardRow}>
              <Text type="secondary">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Viewers.fps}
                  defaultText="FPS"
                />
              </Text>
              {renderViewerMetric(
                viewer,
                viewer.fps === null ? null : formatMetricNumber(viewer.fps, 1),
                'No FPS measurement is available for this viewer yet.',
              )}
            </div>
            <div className={styles.viewerCardRow}>
              <Text type="secondary">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Viewers.packetLoss}
                  defaultText="Packet loss"
                />
              </Text>
              {renderViewerMetric(
                viewer,
                viewer.packetLossPct === null ? null : formatMetricPercent(viewer.packetLossPct, 2),
                viewer.protocol === 'hls'
                  ? 'HTTP/TCP playback has no packet loss to measure.'
                  : 'No packet-loss measurement is available for this viewer yet.',
              )}
            </div>
            <div className={styles.viewerCardRow}>
              <Text type="secondary">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Viewers.duration}
                  defaultText="Duration"
                />
              </Text>
              <span>
                {viewer.durationSeconds === null
                  ? 'N/A'
                  : parseSecondsToDurationString(viewer.durationSeconds)}
              </span>
            </div>
          </Card>
        );
      })}
    </div>
  );

  return (
    <div className={styles.viewerTable}>
      <Space wrap className={styles.viewerFilters}>
        <Select
          aria-label="Filter by protocol"
          value={query.protocol}
          style={{ minWidth: 140 }}
          onChange={value =>
            setQuery(current => ({
              ...current,
              page: 1,
              protocol: value as LiveViewersQuery['protocol'],
            }))
          }
          options={[
            { value: 'all', label: 'All protocols' },
            { value: 'webrtc', label: 'WebRTC' },
            { value: 'hls', label: 'HLS' },
          ]}
        />
        <Input.Search
          aria-label="Search viewers"
          allowClear
          style={{ maxWidth: 280 }}
          placeholder="Search IP or location"
          onSearch={value => setQuery(current => ({ ...current, page: 1, q: value }))}
        />
      </Space>

      {error && (
        <Alert
          type="warning"
          showIcon
          className={styles.viewerError}
          message={error}
          action={
            <Space>
              <a onClick={() => setRefreshToken(token => token + 1)}>Retry</a>
            </Space>
          }
        />
      )}

      {isMobile ? (
        mobileCards
      ) : (
        <Table<LiveViewer>
          size="small"
          rowKey="clientID"
          columns={columns}
          dataSource={data?.viewers || []}
          loading={!data && !error}
          pagination={{
            current: data?.page || query.page,
            pageSize: data?.pageSize || PAGE_SIZE,
            total: data?.total || 0,
            showSizeChanger: false,
          }}
          onChange={(pagination, _filters, sorter) => {
            const activeSorter = Array.isArray(sorter) ? sorter[0] : sorter;
            const mappedSort = activeSorter?.field
              ? SORT_FIELD_MAP[String(activeSorter.field)]
              : undefined;
            setQuery(current => ({
              ...current,
              page: pagination.current || 1,
              pageSize: pagination.pageSize || PAGE_SIZE,
              sort: mappedSort || current.sort,
              order:
                activeSorter?.order === 'ascend'
                  ? 'asc'
                  : activeSorter?.order === 'descend'
                    ? 'desc'
                    : current.order,
            }));
          }}
        />
      )}
      {!isMobile && data && data.viewers.length === 0 && (
        <Text type="secondary" className={styles.viewerEmpty}>
          <Translation
            translationKey={Localization.Admin.LiveStreaming.Viewers.empty}
            defaultText="No viewers are connected right now."
          />
        </Text>
      )}
    </div>
  );
};

export default LiveViewerTable;
