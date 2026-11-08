import { FC } from 'react';
import CheckCircleFilled from '@ant-design/icons/CheckCircleFilled';
import ExclamationCircleFilled from '@ant-design/icons/ExclamationCircleFilled';
import ClockCircleFilled from '@ant-design/icons/ClockCircleFilled';
import CloseCircleFilled from '@ant-design/icons/CloseCircleFilled';
import MinusCircleFilled from '@ant-design/icons/MinusCircleFilled';

import { LiveState } from '../../../interfaces/live-status.model';
import { Localization, LocalizationKey } from '../../../types/localization';
import { Translation } from '../../ui/Translation/Translation';
import styles from './live.module.scss';

type StateConfig = {
  labelKey: LocalizationKey;
  labelDefault: string;
  className: string;
  icon: JSX.Element;
};

const STATE_CONFIG: Record<LiveState, StateConfig> = {
  live: {
    labelKey: Localization.Admin.LiveStreaming.Status.stateLive,
    labelDefault: 'Live',
    className: 'stateLive',
    icon: <CheckCircleFilled />,
  },
  degraded: {
    labelKey: Localization.Admin.LiveStreaming.Status.stateDegraded,
    labelDefault: 'Degraded',
    className: 'stateDegraded',
    icon: <ExclamationCircleFilled />,
  },
  waiting: {
    labelKey: Localization.Admin.LiveStreaming.Status.stateWaiting,
    labelDefault: 'Waiting for connection',
    className: 'stateWaiting',
    icon: <ClockCircleFilled />,
  },
  error: {
    labelKey: Localization.Admin.LiveStreaming.Status.stateError,
    labelDefault: 'Error',
    className: 'stateError',
    icon: <CloseCircleFilled />,
  },
  offline: {
    labelKey: Localization.Admin.LiveStreaming.Status.stateOffline,
    labelDefault: 'Offline',
    className: 'stateOffline',
    icon: <MinusCircleFilled />,
  },
};

export type LiveStateBadgeProps = {
  state: LiveState;
};

// The stream state is conveyed with text, icon and color: never color alone.
export const LiveStateBadge: FC<LiveStateBadgeProps> = ({ state }) => {
  const config = STATE_CONFIG[state] || STATE_CONFIG.offline;
  return (
    <span className={styles.stateBadge + ' ' + styles[config.className]} role="status">
      <span className={styles.stateIcon} aria-hidden="true">
        {config.icon}
      </span>
      <Translation translationKey={config.labelKey} defaultText={config.labelDefault} />
    </span>
  );
};

export default LiveStateBadge;
