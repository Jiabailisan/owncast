import { FC } from 'react';
import { Alert, Select, Space, Switch, Tag, Typography } from 'antd';
import { useTranslation } from 'next-export-i18n';

import { Localization } from '../../types/localization';
import { VariantOutputs } from '../../types/config-section';
import {
  VariantOutputDraft,
  buildVariantOutputsFromDraft,
  extractVariantOutputDraft,
} from '../../utils/variant-outputs';
import styles from './variant-output-settings.module.scss';

const { Title, Paragraph, Text } = Typography;

export type VariantOutputSettingsProps = {
  // Current stored mapping for the variant being edited (undefined = legacy).
  value?: VariantOutputs;
  // Emits the full mapping, or undefined to omit the additive "outputs" block.
  onChange: (outputs: VariantOutputs | undefined) => void;
  disabled?: boolean;
};

// Per-variant delivery mapping editor (master spec 4.4): HLS status is derived
// from the variant's legacy behavior, WebRTC can be explicitly enabled with a
// transcode/copy mode and a copy fallback. All controls are controlled by the
// existing mapping so saving preserves unrelated fields.
export const VariantOutputSettings: FC<VariantOutputSettingsProps> = ({
  value,
  onChange,
  disabled = false,
}) => {
  const { t } = useTranslation();
  const draft = extractVariantOutputDraft(value);
  const hlsEnabled = value?.hls?.enabled !== false;

  const update = (patch: Partial<VariantOutputDraft>) => {
    onChange(buildVariantOutputsFromDraft(value, { ...draft, ...patch }));
  };

  const modeOptions = [
    { value: 'inherit', label: t(Localization.Admin.VideoVariantForm.modeInherit) },
    { value: 'transcode', label: t(Localization.Admin.VideoVariantForm.modeTranscode) },
    { value: 'copy', label: t(Localization.Admin.VideoVariantForm.modeCopy) },
  ];

  const fallbackOptions = [
    { value: 'transcode', label: t(Localization.Admin.VideoVariantForm.fallbackTranscode) },
    { value: 'disable', label: t(Localization.Admin.VideoVariantForm.fallbackDisable) },
  ];

  return (
    <div className={styles.container} data-testid="variant-output-settings">
      <Title level={4}>{t(Localization.Admin.VideoVariantForm.outputsTitle)}</Title>
      <Paragraph type="secondary">
        {t(Localization.Admin.VideoVariantForm.outputsDescription)}
      </Paragraph>

      <Space wrap align="center" className={styles.row}>
        <Tag color={hlsEnabled ? 'green' : 'default'} data-testid="hls-output-status">
          {t(Localization.Admin.VideoVariantForm.hlsEnabledStatus)}
        </Tag>
        <Text strong>{t(Localization.Admin.VideoVariantForm.webrtcEnabled)}</Text>
        <Switch
          data-testid="webrtc-output-enabled"
          checked={draft.webrtcEnabled}
          disabled={disabled}
          aria-label={t(Localization.Admin.VideoVariantForm.webrtcEnabled)}
          onChange={checked => update({ webrtcEnabled: checked })}
        />
      </Space>

      {draft.webrtcEnabled && (
        <div className={styles.mappingControls}>
          <div className={styles.fieldRow}>
            <Text strong>{t(Localization.Admin.VideoVariantForm.webrtcMode)}</Text>
            <Select
              data-testid="webrtc-output-mode"
              className={styles.select}
              aria-label={t(Localization.Admin.VideoVariantForm.webrtcMode)}
              value={draft.webrtcMode}
              disabled={disabled}
              options={modeOptions}
              onChange={mode => update({ webrtcMode: mode })}
            />
          </div>

          {draft.webrtcMode === 'copy' && (
            <div className={styles.fieldRow}>
              <Text strong>{t(Localization.Admin.VideoVariantForm.copyFallback)}</Text>
              <Select
                data-testid="webrtc-copy-fallback"
                className={styles.select}
                aria-label={t(Localization.Admin.VideoVariantForm.copyFallback)}
                value={draft.copyFallback}
                disabled={disabled}
                options={fallbackOptions}
                onChange={fallback => update({ copyFallback: fallback })}
              />
            </div>
          )}

          {draft.webrtcMode === 'copy' && (
            <Alert
              type="info"
              showIcon
              className={styles.note}
              message={t(Localization.Admin.VideoVariantForm.copyNote)}
            />
          )}
        </div>
      )}

      <Alert
        type="info"
        showIcon
        className={styles.note}
        message={t(Localization.Admin.VideoVariantForm.implicitEnableNote)}
      />
    </div>
  );
};

export default VariantOutputSettings;
