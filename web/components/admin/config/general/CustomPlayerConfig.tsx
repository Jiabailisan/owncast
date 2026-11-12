import { FC, useCallback, useContext, useEffect, useState } from 'react';
import { Alert, Button, Col, Input, Row, Space, Tag, Typography } from 'antd';
import { useTranslation } from 'next-export-i18n';
import { format } from 'date-fns';

import AppearanceService from '../../../../services/appearance-service';
import { ActiveFrontend, AppearanceAdminReport } from '../../../../interfaces/appearance.model';
import { Localization } from '../../../../types/localization';
import { ServerStatusContext } from '../../../../utils/server-status-context';
import styles from './custom-player-config.module.scss';

const { Title, Paragraph, Text } = Typography;

type FieldKind = 'player' | 'frontend';

const ACTIVE_FRONTEND_TAG: Record<
  ActiveFrontend,
  { color: string; key: string; defaultText: string }
> = {
  default: {
    color: 'green',
    key: Localization.Admin.Appearance.activeFrontendDefault,
    defaultText: 'Default viewer',
  },
  'custom-player': {
    color: 'blue',
    key: Localization.Admin.Appearance.activeFrontendCustomPlayer,
    defaultText: 'Custom player',
  },
  'custom-frontend': {
    color: 'purple',
    key: Localization.Admin.Appearance.activeFrontendCustomFrontend,
    defaultText: 'Custom frontend',
  },
};

// Appearance -> Custom player / custom frontend (master spec 4.6/7.3/7.4).
// The precedence decision and path validation are computed by the backend
// (AppearanceAdminReport); this component renders the result, the two editable
// paths, their validation errors and the explanations for ignored fields.
// formatLoadErrorTime renders the report timestamp, falling back to a dash when the
// payload is missing or unparsable: date-fns throws RangeError on an invalid date,
// which used to take the whole Appearance page down.
function formatLoadErrorTime(raw?: string): string {
  if (!raw) return '-';

  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return '-';

  return format(parsed, 'MMM d, yyyy HH:mm');
}

export const CustomPlayerConfig: FC = () => {
  const { t } = useTranslation();
  const serverStatusData = useContext(ServerStatusContext);
  const serverConfig = serverStatusData?.serverConfig;

  const initialReport = serverConfig?.appearance;
  const [report, setReport] = useState<AppearanceAdminReport | undefined>(initialReport);
  const [playerPath, setPlayerPath] = useState(initialReport?.customPlayerPath || '');
  const [frontendDir, setFrontendDir] = useState(initialReport?.customFrontendDir || '');
  const [savingField, setSavingField] = useState<FieldKind | null>(null);
  const [savedField, setSavedField] = useState<FieldKind | null>(null);
  const [fieldErrors, setFieldErrors] = useState<{ player?: string; frontend?: string }>({});
  const [loadError, setLoadError] = useState<string | null>(null);

  const applyReport = useCallback((next: AppearanceAdminReport | undefined) => {
    if (!next) return;
    setReport(next);
    setPlayerPath(next.customPlayerPath || '');
    setFrontendDir(next.customFrontendDir || '');
  }, []);

  const refresh = useCallback(async () => {
    try {
      applyReport(await AppearanceService.getReport());
    } catch (error) {
      setLoadError((error as Error)?.message || 'Unable to load appearance information');
    }
  }, [applyReport]);

  useEffect(() => {
    if (initialReport) {
      applyReport(initialReport);
      return;
    }
    void refresh();
  }, [initialReport, applyReport, refresh]);

  useEffect(() => {
    if (!loadError) return undefined;
    const timer = setTimeout(() => setLoadError(null), 5000);
    return () => clearTimeout(timer);
  }, [loadError]);

  const save = async (kind: FieldKind) => {
    setSavingField(kind);
    setSavedField(null);
    setLoadError(null);
    setFieldErrors(current => ({ ...current, [kind]: undefined }));
    try {
      if (kind === 'player') {
        await AppearanceService.saveCustomPlayerPath(playerPath.trim());
      } else {
        await AppearanceService.saveCustomFrontendDir(frontendDir.trim());
      }
      await refresh();
      setSavedField(kind);
      setTimeout(() => setSavedField(current => (current === kind ? null : current)), 3000);
    } catch (error) {
      setFieldErrors(current => ({
        ...current,
        [kind]: (error as Error)?.message || 'Unable to save the setting',
      }));
    } finally {
      setSavingField(null);
    }
  };

  const activeTagConfig = ACTIVE_FRONTEND_TAG[report?.activeFrontend || 'default'];
  const customFrontendActive = report?.activeFrontend === 'custom-frontend';
  const customPlayerActive = report?.activeFrontend === 'custom-player';
  const loadErrorReport = report?.customPlayerLoadError;

  return (
    <div className={styles.customPlayerConfig} data-testid="custom-player-config">
      <Space wrap align="center">
        <Title level={4} className={styles.title}>
          {t(Localization.Admin.Appearance.customPlayerTitle)}
        </Title>
        <Tag color={activeTagConfig.color} data-testid="active-frontend-badge">
          {t(Localization.Admin.Appearance.activeFrontendLabel)}
          {': '}
          {t(activeTagConfig.key)}
        </Tag>
      </Space>
      <Paragraph type="secondary" className={styles.description}>
        {t(Localization.Admin.Appearance.customPlayerDescription)}
      </Paragraph>

      {loadError && <Alert type="error" showIcon message={loadError} className={styles.alert} />}

      {loadErrorReport && (
        <Alert
          type="warning"
          showIcon
          className={styles.alert}
          message={t(Localization.Admin.Appearance.customPlayerLoadErrorTitle)}
          description={t(Localization.Admin.Appearance.customPlayerLoadErrorDescription, {
            time: formatLoadErrorTime(loadErrorReport.at),

            message: loadErrorReport.message,
          })}
        />
      )}

      <Row gutter={[24, 16]}>
        <Col xs={24} lg={12}>
          <label className={styles.label} htmlFor="custom-player-path">
            {t(Localization.Admin.Appearance.customPlayerPathLabel)}
          </label>
          <Input
            id="custom-player-path"
            data-testid="custom-player-path-input"
            value={playerPath}
            placeholder={t(Localization.Admin.Appearance.customPlayerPathPlaceholder)}
            onChange={event => setPlayerPath(event.target.value)}
            disabled={customFrontendActive || savingField !== null}
            status={fieldErrors.player || report?.customPlayerError ? 'error' : undefined}
          />
          <Paragraph type="secondary" className={styles.help}>
            {t(Localization.Admin.Appearance.customPlayerPathHelp)}
          </Paragraph>

          {report?.customPlayerError && (
            <Alert
              type="error"
              showIcon
              className={styles.fieldError}
              message={report.customPlayerError}
            />
          )}
          {fieldErrors.player && (
            <Alert
              type="error"
              showIcon
              className={styles.fieldError}
              message={fieldErrors.player}
            />
          )}
          {customPlayerActive && (
            <Text type="success" className={styles.activeHint}>
              <Tag color="blue">{t(Localization.Admin.Appearance.activeFrontendCustomPlayer)}</Tag>
            </Text>
          )}
          {customFrontendActive && (
            <Alert
              type="info"
              showIcon
              className={styles.fieldError}
              message={t(Localization.Admin.Appearance.ignoredCustomPlayer)}
            />
          )}

          <Space className={styles.actions}>
            <Button
              type="primary"
              loading={savingField === 'player'}
              disabled={customFrontendActive || savingField !== null}
              onClick={() => void save('player')}
            >
              {t(Localization.Admin.Appearance.saveButton)}
            </Button>
            {savedField === 'player' && (
              <Text type="success">{t(Localization.Admin.Appearance.savedMessage)}</Text>
            )}
          </Space>
        </Col>

        <Col xs={24} lg={12}>
          <label className={styles.label} htmlFor="custom-frontend-dir">
            {t(Localization.Admin.Appearance.customFrontendDirLabel)}
          </label>
          <Input
            id="custom-frontend-dir"
            data-testid="custom-frontend-dir-input"
            value={frontendDir}
            placeholder={t(Localization.Admin.Appearance.customFrontendDirPlaceholder)}
            onChange={event => setFrontendDir(event.target.value)}
            disabled={savingField !== null}
            status={fieldErrors.frontend || report?.customFrontendError ? 'error' : undefined}
          />
          <Paragraph type="secondary" className={styles.help}>
            {t(Localization.Admin.Appearance.customFrontendDirHelp)}
          </Paragraph>

          {report?.customFrontendError && (
            <Alert
              type="error"
              showIcon
              className={styles.fieldError}
              message={report.customFrontendError}
            />
          )}
          {fieldErrors.frontend && (
            <Alert
              type="error"
              showIcon
              className={styles.fieldError}
              message={fieldErrors.frontend}
            />
          )}
          {customFrontendActive && (
            <Alert
              type="warning"
              showIcon
              className={styles.fieldError}
              message={t(Localization.Admin.Appearance.customFrontendOverridesCss)}
            />
          )}

          <Space className={styles.actions}>
            <Button
              type="primary"
              loading={savingField === 'frontend'}
              disabled={savingField !== null}
              onClick={() => void save('frontend')}
            >
              {t(Localization.Admin.Appearance.saveButton)}
            </Button>
            {savedField === 'frontend' && (
              <Text type="success">{t(Localization.Admin.Appearance.savedMessage)}</Text>
            )}
          </Space>
        </Col>
      </Row>

      <Alert
        type="info"
        showIcon
        className={styles.precedence}
        message={t(Localization.Admin.Appearance.precedenceTitle)}
        description={
          <ul className={styles.precedenceList}>
            <li>{t(Localization.Admin.Appearance.precedenceDefault)}</li>
            <li>{t(Localization.Admin.Appearance.precedenceCustomPlayer)}</li>
            <li>{t(Localization.Admin.Appearance.precedenceCustomFrontend)}</li>
          </ul>
        }
      />
    </div>
  );
};

export default CustomPlayerConfig;
