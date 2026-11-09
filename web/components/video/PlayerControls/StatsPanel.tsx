import { FC } from 'react';

import { StatsSnapshot, SparklineState } from './types';
import { buildStatsRows, formatStatsBitrate, sparklinePoints } from './stats';
import styles from './PlayerControls.module.scss';

export type StatsPanelProps = {
  stats: StatsSnapshot;
  sparklines: SparklineState;
  onClose: () => void;
};

const CHART_WIDTH = 180;
const CHART_HEIGHT = 30;

const Chart: FC<{ label: string; color: string; values: number[]; current: number | null }> = ({
  label,
  color,
  values,
  current,
}) => {
  const points = sparklinePoints(values, CHART_WIDTH, CHART_HEIGHT);
  const fillPoints = '0,' + CHART_HEIGHT + ' ' + points + ' ' + CHART_WIDTH + ',' + CHART_HEIGHT;
  return (
    <div className={styles.statsChartRow}>
      <span className={styles.statsChartLabel}>{label}</span>
      <svg
        className={styles.bitrateSparkline}
        viewBox={'0 0 ' + CHART_WIDTH + ' ' + CHART_HEIGHT}
        preserveAspectRatio="none"
        role="img"
        aria-label={label + ' bitrate chart'}
      >
        {values.length > 0 && (
          <>
            <polygon points={fillPoints} fill={color} fillOpacity="0.16" stroke="none" />
            <polyline
              points={points}
              fill="none"
              stroke={color}
              strokeWidth="1.5"
              strokeLinejoin="round"
              strokeLinecap="round"
            />
          </>
        )}
      </svg>
      <span className={styles.statsChartValue}>{formatStatsBitrate(current)}</span>
    </div>
  );
};

// YouTube-style Stats for nerds panel. Everything is honest "—" when the
// transport cannot measure it; "Collecting..." shows until the first sample.
export const StatsPanel: FC<StatsPanelProps> = ({ stats, sparklines, onClose }) => {
  const rows = buildStatsRows(stats);
  return (
    <div
      className={styles.statsPanel}
      data-player-control="true"
      role="dialog"
      aria-label="Stats for nerds"
    >
      <div className={styles.statsHeader}>
        <span>Stats for nerds</span>
        <button
          type="button"
          className={styles.statsClose}
          aria-label="Close stats"
          data-player-control="true"
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <div className={styles.statsCharts}>
        <Chart
          label={stats.isWebRTC ? 'Video' : 'Video'}
          color="#5eead4"
          values={sparklines.video}
          current={stats.videoBitrateKbps}
        />
        <Chart
          label={stats.isWebRTC ? 'Audio' : 'Audio (ingest)'}
          color="#f9a8d4"
          values={sparklines.audio}
          current={stats.audioBitrateKbps}
        />
      </div>
      {stats.collecting ? (
        <div className={styles.statsEmpty}>Collecting...</div>
      ) : (
        <div className={styles.statsRows}>
          {rows.map(row => (
            <div className={styles.statsRow} key={row.label}>
              <span className={styles.statsLabel}>{row.label}</span>
              <span className={styles.statsValue}>{row.value}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default StatsPanel;
