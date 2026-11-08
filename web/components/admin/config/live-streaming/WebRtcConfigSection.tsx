import { FC, useCallback, useEffect, useState } from 'react';
import { Alert, Button, Card, Skeleton, Space, Tag, Typography } from 'antd';

import AdminSettingsService from '../../../../services/admin-settings-service';
import { APIError } from '../../../../utils/apis';
import { fieldErrorsToMap, SettingsMetadata } from '../../../../interfaces/settings-metadata.model';
import {
  PreflightResponse,
  WebRTCConfig,
  WebRTCConfigResponse,
  WebRTCDomainTestResponse,
} from '../../../../interfaces/webrtc-config.model';
import { Localization } from '../../../../types/localization';
import { Translation } from '../../../ui/Translation/Translation';
import { SettingsForm } from './SettingsForm';
import styles from './live-streaming-sections.module.scss';

const { Text } = Typography;

export type WebRtcConfigSectionProps = {
  metadata: SettingsMetadata | null;
};

const asValues = (value: unknown): Record<string, unknown> => value as Record<string, unknown>;

// WebRTC Configuration tab: metadata-driven settings plus the two diagnostics
// actions (domain test and FFmpeg/port preflight) the operator needs before
// enabling delivery.
export const WebRtcConfigSection: FC<WebRtcConfigSectionProps> = ({ metadata }) => {
  const [response, setResponse] = useState<WebRTCConfigResponse | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [preflight, setPreflight] = useState<PreflightResponse | null>(null);
  const [preflightLoading, setPreflightLoading] = useState(false);
  const [domainResult, setDomainResult] = useState<WebRTCDomainTestResponse | null>(null);
  const [domainLoading, setDomainLoading] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setLoadError(null);
    try {
      const result = await AdminSettingsService.getWebRTCConfig({ signal });
      if (signal?.aborted) return;
      setResponse(result);
    } catch (error) {
      if (signal?.aborted) return;
      setLoadError((error as Error)?.message || 'Unable to load the WebRTC configuration');
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
      const result = await AdminSettingsService.saveWebRTCConfig(patch as Partial<WebRTCConfig>);
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

  const runPreflight = async () => {
    setPreflightLoading(true);
    setActionError(null);
    try {
      setPreflight(await AdminSettingsService.runPreflight('webrtc'));
    } catch (error) {
      setActionError((error as Error)?.message || 'Preflight failed');
    } finally {
      setPreflightLoading(false);
    }
  };

  const testDomain = async () => {
    if (!response?.config.domain) return;
    setDomainLoading(true);
    setActionError(null);
    try {
      setDomainResult(await AdminSettingsService.testWebRTCDomain(response.config.domain));
    } catch (error) {
      setActionError((error as Error)?.message || 'Domain test failed');
    } finally {
      setDomainLoading(false);
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

  return (
    <div className={styles.section}>
      <Card
        size="small"
        className={styles.diagnosticsCard}
        title={
          <Translation
            translationKey={Localization.Admin.LiveStreaming.WebRtc.ffmpegCaps}
            defaultText="FFmpeg and port preflight"
          />
        }
      >
        <Space wrap>
          <Button onClick={runPreflight} loading={preflightLoading}>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.WebRtc.preflight}
              defaultText="Run preflight"
            />
          </Button>
          <Button onClick={testDomain} loading={domainLoading} disabled={!response.config.domain}>
            <Translation
              translationKey={Localization.Admin.LiveStreaming.WebRtc.testDomain}
              defaultText="Test domain"
            />
          </Button>
          {!response.config.domain && (
            <Text type="secondary">
              <Translation
                translationKey={Localization.Admin.LiveStreaming.WebRtc.noDomain}
                defaultText="Set a domain (or public IPs) before testing."
              />
            </Text>
          )}
        </Space>

        {actionError && (
          <Alert type="error" showIcon className={styles.sectionError} message={actionError} />
        )}

        {preflight && (
          <div className={styles.preflightResults}>
            <div>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.WebRtc.ffmpegVersion}
                defaultText="FFmpeg"
              />
              {': '}
              <Text code>{preflight.ffmpeg.version || 'unknown'}</Text>
              {preflight.ffmpeg.path && <Text type="secondary"> ({preflight.ffmpeg.path})</Text>}
            </div>
            <Space wrap>
              <Tag color={preflight.ffmpeg.opusEncoder ? 'green' : 'red'}>
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.WebRtc.opusEncoder}
                  defaultText="libopus"
                />
              </Tag>
              <Tag color={preflight.ffmpeg.srtProtocol ? 'green' : 'red'}>
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.WebRtc.srtProtocol}
                  defaultText="SRT protocol"
                />
              </Tag>
              {preflight.ffmpeg.h264Encoders.map(encoder => (
                <Tag key={encoder} color="blue">
                  {encoder}
                </Tag>
              ))}
            </Space>
            <div className={styles.portList}>
              {preflight.ports.map(port => (
                <Tag key={port.protocol + port.port} color={port.available ? 'green' : 'red'}>
                  {port.protocol}:{port.port}{' '}
                  {port.available ? (
                    <Translation
                      translationKey={Localization.Admin.LiveStreaming.WebRtc.portAvailable}
                      defaultText="available"
                    />
                  ) : (
                    <Translation
                      translationKey={Localization.Admin.LiveStreaming.WebRtc.portUnavailable}
                      defaultText="in use"
                    />
                  )}
                </Tag>
              ))}
            </div>
            {preflight.warnings.map(warning => (
              <Alert
                key={warning.code + warning.message}
                type="warning"
                showIcon
                message={warning.message}
              />
            ))}
          </div>
        )}

        {domainResult && (
          <div className={styles.preflightResults}>
            <div>
              <Text strong>{domainResult.domain}</Text>
              {domainResult.error && <Text type="danger"> {domainResult.error}</Text>}
            </div>
            <Space wrap>
              <Tag color="blue">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.WebRtc.resolvedIps}
                  defaultText="Resolved"
                />
                : {domainResult.resolvedIPs.join(', ') || 'N/A'}
              </Tag>
              <Tag color="blue">
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.WebRtc.advertisedIps}
                  defaultText="Advertised"
                />
                : {domainResult.advertisedIPs.join(', ') || 'N/A'}
              </Tag>
              <Tag color={domainResult.strictDomain ? 'orange' : 'default'}>
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.WebRtc.strictDomainLabel}
                  defaultText="Strict domain"
                />
                : {domainResult.strictDomain ? 'on' : 'off'}
              </Tag>
            </Space>
          </div>
        )}
      </Card>

      {saveError && (
        <Alert type="error" showIcon className={styles.sectionError} message={saveError} />
      )}

      <SettingsForm
        fields={metadata.webrtc.fields}
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

export default WebRtcConfigSection;
