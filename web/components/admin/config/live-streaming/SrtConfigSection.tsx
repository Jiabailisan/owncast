import { FC, useCallback, useEffect, useState } from 'react';
import { Alert, Button, Skeleton } from 'antd';

import AdminSettingsService from '../../../../services/admin-settings-service';
import { APIError } from '../../../../utils/apis';
import { fieldErrorsToMap, SettingsMetadata } from '../../../../interfaces/settings-metadata.model';
import { SRTConfig, SRTConfigResponse } from '../../../../interfaces/srt-config.model';
import { Localization } from '../../../../types/localization';
import { Translation } from '../../../ui/Translation/Translation';
import { SettingsForm } from './SettingsForm';
import styles from './live-streaming-sections.module.scss';

export type SrtConfigSectionProps = {
  metadata: SettingsMetadata | null;
};

const asValues = (value: unknown): Record<string, unknown> => value as Record<string, unknown>;

// SRT Configuration tab: listener status plus the metadata-driven settings
// form. The form owns the patch/validation UX; this section owns the API
// lifecycle (GET on open, PUT on save, field errors and pending restart).
export const SrtConfigSection: FC<SrtConfigSectionProps> = ({ metadata }) => {
  const [response, setResponse] = useState<SRTConfigResponse | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setLoadError(null);
    try {
      const result = await AdminSettingsService.getSRTConfig({ signal });
      if (signal?.aborted) return;
      setResponse(result);
    } catch (error) {
      if (signal?.aborted) return;
      setLoadError((error as Error)?.message || 'Unable to load the SRT configuration');
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const handleSave = async (patch: Record<string, unknown>) => {
    setSaveError(null);
    setFieldErrors({});
    try {
      const result = await AdminSettingsService.saveSRTConfig(patch as Partial<SRTConfig>);
      setResponse(previous =>
        previous
          ? {
              ...previous,
              config: result.config ?? previous.config,
              pendingRestart: result.pendingRestart ?? previous.pendingRestart,
            }
          : previous,
      );
    } catch (error) {
      const apiError = error as APIError;
      setFieldErrors(fieldErrorsToMap(apiError.fieldErrors));
      setSaveError(apiError.message);
      throw error;
    }
  };

  if (loading && !response) {
    return <Skeleton active paragraph={{ rows: 8 }} />;
  }

  if (loadError) {
    return (
      <Alert
        type="error"
        showIcon
        message={loadError}
        action={
          <Button size="small" onClick={() => void load()}>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Settings.retry}
              defaultText="Retry"
            />
          </Button>
        }
      />
    );
  }

  if (!metadata || !response) {
    return <Skeleton active paragraph={{ rows: 8 }} />;
  }

  const { listener } = response;

  return (
    <div className={styles.section}>
      <Alert
        type={listener.enabled ? (listener.listening ? 'success' : 'warning') : 'info'}
        showIcon
        className={styles.listenerAlert}
        message={
          listener.enabled && listener.listening ? (
            <>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Srt.listening}
                defaultText="The SRT listener is active."
              />
              {listener.port > 0 && (
                <span className={styles.listenerPort}>
                  {' '}
                  UDP {listener.port}
                  {listener.boundAddress ? ' on ' + listener.boundAddress : ''}
                </span>
              )}
              {listener.activeSession && (
                <span className={styles.listenerPort}>
                  {' '}
                  - {listener.activeSession.remoteAddr} ({listener.activeSession.streamKeyID})
                </span>
              )}
            </>
          ) : listener.enabled ? (
            <>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Srt.notListening}
                defaultText="The SRT listener is enabled but not accepting connections."
              />
              {listener.state && <span className={styles.listenerPort}> [{listener.state}]</span>}
              {listener.lastError && (
                <span className={styles.listenerPort}> {listener.lastError}</span>
              )}
            </>
          ) : (
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Srt.disabled}
              defaultText="SRT ingest is disabled. Existing RTMP behavior is unchanged."
            />
          )
        }
      />

      {saveError && (
        <Alert type="error" showIcon className={styles.sectionError} message={saveError} />
      )}

      <SettingsForm
        fields={metadata.srt.fields}
        groups={metadata.groups}
        values={asValues(response.config)}
        defaults={asValues(response.defaults)}
        pendingRestart={response.pendingRestart}
        fieldErrors={fieldErrors}
        onSave={handleSave}
      />
    </div>
  );
};

export default SrtConfigSection;
