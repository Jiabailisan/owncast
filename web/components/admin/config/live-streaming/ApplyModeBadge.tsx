import { FC } from 'react';
import { Tag, Tooltip } from 'antd';

import { ApplyMode } from '../../../../interfaces/settings-metadata.model';
import { Localization, LocalizationKey } from '../../../../types/localization';
import { Translation } from '../../../ui/Translation/Translation';
import styles from './settings-form.module.scss';

type BadgeConfig = {
  labelKey: LocalizationKey;
  labelDefault: string;
  descriptionKey: LocalizationKey;
  descriptionDefault: string;
  color: string;
};

// One badge per apply mode, so an operator can tell at a glance whether a
// change is instant, needs a service restart, or applies on the next stream.
const APPLY_MODE_CONFIG: Record<ApplyMode, BadgeConfig> = {
  live: {
    labelKey: Localization.Admin.LiveStreaming.Settings.applyLive,
    labelDefault: 'Applies immediately',
    descriptionKey: Localization.Admin.LiveStreaming.Settings.applyLive,
    descriptionDefault: 'This setting takes effect as soon as it is saved.',
    color: 'green',
  },
  'restart-service': {
    labelKey: Localization.Admin.LiveStreaming.Settings.applyRestartService,
    labelDefault: 'Needs service restart',
    descriptionKey: Localization.Admin.LiveStreaming.Settings.applyRestartService,
    descriptionDefault:
      'This setting takes effect after the SRT or WebRTC service is restarted.',
    color: 'orange',
  },
  'restart-stream': {
    labelKey: Localization.Admin.LiveStreaming.Settings.applyRestartStream,
    labelDefault: 'Next stream',
    descriptionKey: Localization.Admin.LiveStreaming.Settings.applyRestartStream,
    descriptionDefault:
      'This setting takes effect the next time a stream starts.',
    color: 'blue',
  },
  'restart-server': {
    labelKey: Localization.Admin.LiveStreaming.Settings.applyRestartServer,
    labelDefault: 'Server restart',
    descriptionKey: Localization.Admin.LiveStreaming.Settings.applyRestartServer,
    descriptionDefault: 'This setting takes effect after the Owncast server restarts.',
    color: 'red',
  },
  'n/a': {
    labelKey: Localization.Admin.LiveStreaming.Settings.fixed,
    labelDefault: 'Fixed',
    descriptionKey: Localization.Admin.LiveStreaming.Settings.fixed,
    descriptionDefault:
      'This option is fixed by the underlying library or browser requirements and cannot be changed.',
    color: 'default',
  },
};

export type ApplyModeBadgeProps = {
  applyMode: ApplyMode;
  pending?: boolean;
};

export const ApplyModeBadge: FC<ApplyModeBadgeProps> = ({ applyMode, pending = false }) => {
  const config = APPLY_MODE_CONFIG[applyMode] || APPLY_MODE_CONFIG.live;
  return (
    <Tooltip
      title={<Translation translationKey={config.descriptionKey} defaultText={config.descriptionDefault} />}
    >
      <Tag color={pending ? 'warning' : config.color} className={styles.applyBadge}>
        <Translation translationKey={config.labelKey} defaultText={config.labelDefault} />
        {pending && (
          <span className={styles.pendingMarker}>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Settings.pendingRestart}
              defaultText="Pending restart"
            />
          </span>
        )}
      </Tag>
    </Tooltip>
  );
};

export default ApplyModeBadge;
