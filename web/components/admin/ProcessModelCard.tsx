import { FC, useCallback, useEffect, useState } from 'react';
import { Alert, Button, Card, Select, Space, Typography } from 'antd';
import { useTranslation } from 'next-export-i18n';

import OutputConfigService, {
  OutputConfigResponse,
  OutputConfigSaveResponse,
} from '../../services/output-config-service';
import { APIError } from '../../utils/apis';
import { OutputProcessModel } from '../../types/config-section';
import { PendingRestart } from '../../interfaces/settings-metadata.model';
import { Localization } from '../../types/localization';
import { ApplyModeBadge } from './config/live-streaming/ApplyModeBadge';
import styles from './process-model-card.module.scss';

const { Paragraph, Text } = Typography;

const defaultLoader = () => OutputConfigService.getOutputConfig();
const defaultSaver = (processModel: OutputProcessModel) =>
  OutputConfigService.saveOutputConfig(processModel);

export type ProcessModelCardProps = {
  // Test/Storybook overrides. When provided, no initial network request is made.
  initialModel?: OutputProcessModel;
  loader?: () => Promise<OutputConfigResponse>;
  saver?: (processModel: OutputProcessModel) => Promise<OutputConfigSaveResponse>;
};

// FFmpeg output process model control (master spec 3.4). Shared uses one
// FFmpeg process for all HLS and WebRTC legs; isolated uses separate HLS and
// WebRTC processes for independent failure domains at the cost of a second
// decode. The setting applies at the next stream start.
export const ProcessModelCard: FC<ProcessModelCardProps> = ({ initialModel, loader, saver }) => {
  const { t } = useTranslation();
  const [model, setModel] = useState<OutputProcessModel | undefined>(initialModel);
  const [savedModel, setSavedModel] = useState<OutputProcessModel | undefined>(initialModel);
  const [pendingRestart, setPendingRestart] = useState<PendingRestart | null>(null);
  const [loading, setLoading] = useState(!initialModel);
  const [saving, setSaving] = useState(false);
  const [savedVisible, setSavedVisible] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const applyResponse = useCallback((response: OutputConfigResponse) => {
    const value = response.config?.processModel;
    setModel(value);
    setSavedModel(value);
    setPendingRestart(response.pendingRestart || null);
  }, []);

  useEffect(() => {
    if (initialModel) {
      return undefined;
    }
    let cancelled = false;
    const load = loader || defaultLoader;
    setLoading(true);
    setError(null);
    load()
      .then(response => {
        if (!cancelled) applyResponse(response);
      })
      .catch(loadError => {
        if (!cancelled) {
          setError((loadError as Error)?.message || t(Localization.Admin.ProcessModel.loadError));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // initialModel/loader are stable for a mounted production card.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialModel]);

  const handleSave = async () => {
    if (!model) return;
    setSaving(true);
    setError(null);
    try {
      const response = await (saver || defaultSaver)(model);
      const savedValue = response?.config?.processModel ?? model;
      setSavedModel(savedValue);
      setPendingRestart(response?.pendingRestart || null);
      setSavedVisible(true);
      setTimeout(() => setSavedVisible(false), 3000);
    } catch (saveError) {
      const apiError = saveError as APIError;
      const fieldMessage = apiError?.fieldErrors?.length ? apiError.fieldErrors[0].message : null;
      setError(fieldMessage || apiError?.message || 'Unable to save the process model');
    } finally {
      setSaving(false);
    }
  };

  const dirty = model !== undefined && model !== savedModel;

  const modelOptions = [
    {
      value: 'shared' as OutputProcessModel,
      label: t(Localization.Admin.ProcessModel.sharedLabel),
    },
    {
      value: 'isolated' as OutputProcessModel,
      label: t(Localization.Admin.ProcessModel.isolatedLabel),
    },
  ];

  return (
    <Card
      size="small"
      className={styles.card}
      data-testid="process-model-card"
      title={
        <Space wrap>
          <span>{t(Localization.Admin.ProcessModel.title)}</span>
          <ApplyModeBadge applyMode="restart-stream" />
        </Space>
      }
    >
      <Paragraph type="secondary" className={styles.description}>
        {t(Localization.Admin.ProcessModel.description)}
      </Paragraph>

      {error && <Alert type="error" showIcon className={styles.alert} message={error} />}
      {pendingRestart?.pending && (
        <Alert
          type="warning"
          showIcon
          className={styles.alert}
          message={t(Localization.Admin.ProcessModel.pendingRestartTitle)}
          description={t(Localization.Admin.ProcessModel.pendingRestartDescription)}
        />
      )}

      <Space wrap align="center">
        <Select
          aria-label={t(Localization.Admin.ProcessModel.title)}
          data-testid="process-model-select"
          className={styles.select}
          loading={loading}
          value={model}
          options={modelOptions}
          onChange={value => setModel(value)}
        />
        <Button
          type="primary"
          data-testid="process-model-save"
          loading={saving}
          disabled={!dirty}
          onClick={() => void handleSave()}
        >
          {t(Localization.Admin.ProcessModel.save)}
        </Button>
        {savedVisible && <Text type="success">{t(Localization.Admin.ProcessModel.saved)}</Text>}
      </Space>

      <div className={styles.modelNotes}>
        <Paragraph type="secondary" className={styles.modelNote}>
          <Text strong>{t(Localization.Admin.ProcessModel.sharedLabel)}</Text>
          {': '}
          {t(Localization.Admin.ProcessModel.sharedDescription)}
        </Paragraph>
        <Paragraph type="secondary" className={styles.modelNote}>
          <Text strong>{t(Localization.Admin.ProcessModel.isolatedLabel)}</Text>
          {': '}
          {t(Localization.Admin.ProcessModel.isolatedDescription)}
        </Paragraph>
      </div>
    </Card>
  );
};

export default ProcessModelCard;
