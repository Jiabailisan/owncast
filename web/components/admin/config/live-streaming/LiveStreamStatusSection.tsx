import { FC, useState } from 'react';
import { Alert, Button, Select, Skeleton, Space, Typography } from 'antd';
import { format } from 'date-fns';

import { useLiveStatusPolling } from '../../../../hooks/useLiveStatusPolling';
import { Localization } from '../../../../types/localization';
import { Translation } from '../../../ui/Translation/Translation';
import { FfmpegPanel } from '../../live/FfmpegPanel';
import { OutboundStreamsTable } from '../../live/OutboundStreamsTable';
import { StreamStatusCards } from '../../live/StreamStatusCards';
import styles from './live-streaming-sections.module.scss';

const { Title } = Typography;

const POLL_INTERVAL_STORAGE_KEY = 'owncast_live_status_poll_ms';
const DEFAULT_POLL_INTERVAL_MS = 2000;
const POLL_OPTIONS = [
  { value: 1000, label: '1s' },
  { value: 2000, label: '2s' },
  { value: 5000, label: '5s' },
  { value: 10000, label: '10s' },
  { value: 30000, label: '30s' },
];

function readSavedInterval(): number {
  try {
    const saved = Number(localStorage.getItem(POLL_INTERVAL_STORAGE_KEY));
    if (POLL_OPTIONS.some(option => option.value === saved)) return saved;
  } catch {
    // localStorage can be unavailable; fall back to the default.
  }
  return DEFAULT_POLL_INTERVAL_MS;
}

// Live Stream Status tab: polling snapshot (paused while hidden), stream-level
// cards, per-viewer table and the exact FFmpeg process/command panel. One
// component tree renders both RTMP and SRT sessions; only the protocol badge
// and nullable-field rendering differ.
export const LiveStreamStatusSection: FC = () => {
  const [pollIntervalMs, setPollIntervalMs] = useState<number>(() => readSavedInterval());
  const { status, ffmpeg, error, lastUpdated, loading, refresh } = useLiveStatusPolling({
    intervalMs: pollIntervalMs,
  });

  const updateInterval = (value: number) => {
    setPollIntervalMs(value);
    try {
      localStorage.setItem(POLL_INTERVAL_STORAGE_KEY, String(value));
    } catch {
      // Non-persistent preference is acceptable.
    }
  };

  const controls = (
    <Space wrap className={styles.statusControls}>
      <Space>
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.pollingInterval}
          defaultText="Refresh every"
        />
        <Select
          aria-label="Refresh interval"
          className={styles.pollingSelect}
          size="small"
          value={pollIntervalMs}
          options={POLL_OPTIONS}
          popupMatchSelectWidth={false}
          onChange={updateInterval}
        />
      </Space>
      <Button size="small" onClick={refresh}>
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.refresh}
          defaultText="Refresh now"
        />
      </Button>
      {lastUpdated !== null && (
        <span className={styles.lastUpdated}>
          <Translation
            translationKey={Localization.Admin.LiveStreaming.Status.lastUpdated}
            defaultText="Last updated"
          />
          {': '}
          {format(new Date(lastUpdated), 'HH:mm:ss')}
        </span>
      )}
    </Space>
  );

  if (!status && loading) {
    return (
      <div className={styles.section}>
        {controls}
        <Skeleton active paragraph={{ rows: 10 }} />
      </div>
    );
  }

  if (!status) {
    return (
      <div className={styles.section}>
        {controls}
        <Alert
          type="error"
          showIcon
          message={
            error || (
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Status.loadError}
                defaultText="Unable to load the live stream status."
              />
            )
          }
          action={
            <Button size="small" onClick={refresh}>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.retry}
                defaultText="Retry"
              />
            </Button>
          }
        />
      </div>
    );
  }

  return (
    <div className={styles.section}>
      {controls}
      {error && (
        <Alert
          type="warning"
          showIcon
          className={styles.stale}
          message={
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Status.stale}
              defaultText="Showing the last known status: the server could not be reached for the latest snapshot."
            />
          }
          description={error}
        />
      )}

      <StreamStatusCards status={status} />

      {(status.outbound?.streams?.length ?? 0) > 0 && (
        <>
          <Title level={4} className={styles.sectionHeading}>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Status.outboundSection}
              defaultText="Outbound streams"
            />
          </Title>
          <OutboundStreamsTable streams={status.outbound?.streams ?? []} />
        </>
      )}

      {/* The viewer table lives on the Viewers page; showing it twice made
          operators wonder which one was authoritative. */}

      <Title level={4} className={styles.sectionHeading}>
        <Translation
          translationKey={Localization.Admin.LiveStreaming.Status.ffmpegSection}
          defaultText="FFmpeg processes"
        />
      </Title>
      <FfmpegPanel panel={ffmpeg} />
    </div>
  );
};

export default LiveStreamStatusSection;
