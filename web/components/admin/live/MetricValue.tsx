import { FC, ReactNode } from 'react';
import { Tooltip } from 'antd';

import styles from './live.module.scss';

export const NOT_AVAILABLE = 'N/A';

export type MetricValueProps = {
  value: ReactNode;
  // When the metric is unavailable, explain why (e.g. "RTMP: not measurable on TCP").
  tooltip?: string;
};

// Renders one live metric. Unknown values (null/undefined/empty) render as N/A
// with a tooltip explaining why; the UI must never estimate a value.
export const MetricValue: FC<MetricValueProps> = ({ value, tooltip }) => {
  const missing = value === null || value === undefined || value === '';
  const display = missing ? NOT_AVAILABLE : value;
  const content = <span className={missing ? styles.metricMissing : styles.metric}>{display}</span>;
  return tooltip ? <Tooltip title={tooltip}>{content}</Tooltip> : content;
};

export default MetricValue;
