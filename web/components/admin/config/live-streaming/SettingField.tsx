import { FC, useState } from 'react';
import { Alert, Button, Input, InputNumber, Select, Switch, Tag, Tooltip, Typography } from 'antd';
import { useTranslation } from 'next-export-i18n';
import { Translation } from '../../../ui/Translation/Translation';
import { Localization } from '../../../../types/localization';
import { FieldMeta, humanizeFieldKey } from '../../../../interfaces/settings-metadata.model';
import { ApplyModeBadge } from './ApplyModeBadge';
import styles from './settings-form.module.scss';

const { Text } = Typography;

export type SettingFieldProps = {
  meta: FieldMeta;
  // Current effective value (draft beats saved beats default).
  value: unknown;
  defaultValue: unknown;
  modified: boolean;
  disabled?: boolean;
  disabledReason?: string;
  error?: string;
  // Only meaningful for secret fields: whether the server has a stored secret.
  secretSet?: boolean;
  pendingRestart?: boolean;
  onChange: (key: string, value: unknown) => void;
  onReset: (key: string) => void;
};

// One row in the TURN-server list editor. Secrets are write-only: the draft
// only carries a value when the operator typed a new one; otherwise the
// existing stored secret is kept by omitting the field on save.
type TURNServerDraft = {
  url: string;
  username: string;
  credential: string;
  staticSecret: string;
  credentialSet: boolean;
  staticSecretSet: boolean;
};

function asStringList(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(item => String(item));
  if (typeof value === 'string' && value.trim() !== '') return [value];
  return [];
}

function asInputNumberValue(value: unknown): number | null {
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

function toTURNList(value: unknown): TURNServerDraft[] {
  if (!Array.isArray(value)) return [];
  return value.map(entry => {
    const server = (entry || {}) as Record<string, unknown>;
    return {
      url: typeof server.url === 'string' ? server.url : '',
      username: typeof server.username === 'string' ? server.username : '',
      credential: '',
      staticSecret: '',
      credentialSet: Boolean(server.credentialSet),
      staticSecretSet: Boolean(server.staticSecretSet),
    };
  });
}

function turnDraftToPatch(rows: TURNServerDraft[]): unknown[] {
  return rows.map(row => ({
    url: row.url,
    username: row.username,
    ...(row.credential !== '' ? { credential: row.credential } : {}),
    ...(row.staticSecret !== '' ? { staticSecret: row.staticSecret } : {}),
  }));
}

function fixedValueLabel(value: unknown): string {
  if (Array.isArray(value)) return value.length > 0 ? value.join(', ') : '(empty)';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  return String(value ?? '(fixed)');
}

// One metadata-described setting row. The widget is chosen from FieldMeta.type;
// the form never invents validation rules: server-side validation is
// authoritative and surfaces through the per-field error text.
export const SettingField: FC<SettingFieldProps> = ({
  meta,
  value,
  defaultValue,
  modified,
  disabled = false,
  disabledReason,
  error,
  secretSet = false,
  pendingRestart = false,
  onChange,
  onReset,
}) => {
  const { t } = useTranslation();
  const [secretDraft, setSecretDraft] = useState<string>('');
  const [turnDraft, setTurnDraft] = useState<TURNServerDraft[] | null>(null);
  const label = meta.label || humanizeFieldKey(meta.key);
  const localize = (key: string, fallback: string): string => {
    const translated = t(key);
    return translated === key ? fallback : translated;
  };
  const inputId = 'setting-' + meta.key.replace(/[^a-zA-Z0-9_-]/g, '-');
  const readOnly = meta.readOnly || disabled;
  // List-style fields (tags selects and the TURN editor) need the whole form
  // column: their controls otherwise shrink to their empty content inside the
  // flex row and the dropdown anchors to a tiny box.
  const isListField =
    meta.type === 'stringList' || meta.type === 'cidrList' || meta.type === 'turnServerList';
  const settingInputClassName = isListField
    ? `${styles.settingInput} ${styles.listInput}`
    : styles.settingInput;

  const handleSecretChange = (next: string) => {
    setSecretDraft(next);
    // Push the draft upward so the save bar knows exactly which secret fields
    // the operator changed. Empty string explicitly clears a stored secret.
    onChange(meta.key, next);
  };

  const handleResetClick = () => {
    setTurnDraft(null);
    setSecretDraft('');
    onReset(meta.key);
  };

  const turnServers = turnDraft ?? toTURNList(value);
  const emitTURNList = (rows: TURNServerDraft[]) => {
    setTurnDraft(rows);
    onChange(meta.key, turnDraftToPatch(rows));
  };
  const updateTURNRow = (index: number, patch: Partial<TURNServerDraft>) => {
    emitTURNList(
      turnServers.map((row, rowIndex) => (rowIndex === index ? { ...row, ...patch } : row)),
    );
  };

  const renderTURNEditor = () => (
    <div className={styles.turnList}>
      {turnServers.map((row, index) => (
        <div key={inputId + '-turn-' + index} className={styles.turnRow}>
          <Input
            aria-label={localize(
              Localization.Admin.LiveStreaming.Settings.turnServerUrl,
              'TURN server URL',
            )}
            placeholder="turn:turn.example.org:3478"
            value={row.url}
            disabled={readOnly}
            onChange={event => updateTURNRow(index, { url: event.target.value })}
          />
          <Input
            aria-label={localize(
              Localization.Admin.LiveStreaming.Settings.turnServerUsername,
              'TURN username',
            )}
            placeholder="username"
            value={row.username}
            disabled={readOnly}
            onChange={event => updateTURNRow(index, { username: event.target.value })}
          />
          <Input.Password
            aria-label={localize(
              Localization.Admin.LiveStreaming.Settings.turnServerCredential,
              'TURN credential',
            )}
            placeholder={
              row.credentialSet
                ? localize(
                    Localization.Admin.LiveStreaming.Settings.secretKeepPlaceholder,
                    'Leave blank to keep the current secret',
                  )
                : localize(
                    Localization.Admin.LiveStreaming.Settings.secretNotSetPlaceholder,
                    'No secret is set',
                  )
            }
            value={row.credential}
            disabled={readOnly}
            onChange={event => updateTURNRow(index, { credential: event.target.value })}
          />
          <Input.Password
            aria-label={localize(
              Localization.Admin.LiveStreaming.Settings.turnServerStaticSecret,
              'TURN static secret',
            )}
            placeholder={
              row.staticSecretSet
                ? localize(
                    Localization.Admin.LiveStreaming.Settings.secretKeepPlaceholder,
                    'Leave blank to keep the current secret',
                  )
                : localize(
                    Localization.Admin.LiveStreaming.Settings.secretNotSetPlaceholder,
                    'No secret is set',
                  )
            }
            value={row.staticSecret}
            disabled={readOnly}
            onChange={event => updateTURNRow(index, { staticSecret: event.target.value })}
          />
          <Button
            size="small"
            danger
            disabled={readOnly}
            onClick={() => emitTURNList(turnServers.filter((_, rowIndex) => rowIndex !== index))}
          >
            {localize(Localization.Admin.LiveStreaming.Settings.turnServerRemove, 'Remove')}
          </Button>
        </div>
      ))}
      <Button
        size="small"
        disabled={readOnly}
        onClick={() =>
          emitTURNList([
            ...turnServers,
            {
              url: '',
              username: '',
              credential: '',
              staticSecret: '',
              credentialSet: false,
              staticSecretSet: false,
            },
          ])
        }
      >
        {localize(Localization.Admin.LiveStreaming.Settings.turnServerAdd, 'Add TURN server')}
      </Button>
    </div>
  );

  const renderInput = () => {
    switch (meta.type) {
      case 'bool':
        return (
          <Switch
            id={inputId}
            checked={Boolean(value)}
            disabled={readOnly}
            aria-label={label}
            onChange={checked => onChange(meta.key, checked)}
          />
        );
      case 'int':
      case 'float':
        return (
          <span className={styles.numberField}>
            <InputNumber
              id={inputId}
              value={asInputNumberValue(value)}
              min={meta.min ?? undefined}
              max={meta.max ?? undefined}
              step={meta.type === 'float' ? 0.1 : 1}
              disabled={readOnly}
              aria-label={label}
              onChange={next =>
                // A cleared number restores the field's default. Emitting undefined
                // used to be dropped by JSON.stringify, so the save "succeeded" and the
                // old value silently came back.
                onChange(
                  meta.key,
                  next === null || next === undefined ? meta.default : Number(next),
                )
              }
            />
            {meta.unit && <Text type="secondary">{meta.unit}</Text>}
          </span>
        );
      case 'enum':
        return (
          <Select
            id={inputId}
            className={styles.enumSelect}
            data-testid={`setting-enum-${meta.key}`}
            value={(value as string) ?? undefined}
            disabled={readOnly}
            aria-label={label}
            popupMatchSelectWidth={false}
            options={(meta.enum || []).map(option => ({ value: option, label: option }))}
            onChange={next => onChange(meta.key, next)}
          />
        );
      case 'stringList':
      case 'cidrList':
        return (
          <Select
            id={inputId}
            mode="tags"
            className={styles.listSelect}
            data-testid={`setting-list-${meta.key}`}
            value={asStringList(value)}
            disabled={readOnly}
            aria-label={label}
            tokenSeparators={[',']}
            popupMatchSelectWidth
            placeholder={
              meta.type === 'cidrList'
                ? t(Localization.Admin.LiveStreaming.Settings.addCidr)
                : t(Localization.Admin.LiveStreaming.Settings.addValue)
            }
            notFoundContent={
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.noListValues}
                defaultText="Type a value and press Enter"
              />
            }
            classNames={{ popup: { root: styles.listSelectPopup } }}
            onChange={next => onChange(meta.key, next)}
          />
        );
      case 'turnServerList':
        return renderTURNEditor();
      case 'secret':
        return (
          <span className={styles.secretField}>
            <Input.Password
              id={inputId}
              value={secretDraft}
              disabled={readOnly}
              aria-label={label}
              placeholder={
                secretSet
                  ? localize(
                      Localization.Admin.LiveStreaming.Settings.secretKeepPlaceholder,
                      'Leave blank to keep the current secret',
                    )
                  : localize(
                      Localization.Admin.LiveStreaming.Settings.secretNotSetPlaceholder,
                      'No secret is set',
                    )
              }
              onChange={event => handleSecretChange(event.target.value)}
            />
            <span className={styles.secretState}>
              <Tag color={secretSet ? 'green' : 'default'}>
                {secretSet ? (
                  <Translation
                    translationKey={Localization.Admin.LiveStreaming.Settings.secretSet}
                    defaultText="Set"
                  />
                ) : (
                  <Translation
                    translationKey={Localization.Admin.LiveStreaming.Settings.secretNotSet}
                    defaultText="Not set"
                  />
                )}
              </Tag>
              {secretSet && (
                <Button
                  size="small"
                  type="link"
                  disabled={readOnly}
                  onClick={() => handleSecretChange('')}
                >
                  <Translation
                    translationKey={Localization.Admin.LiveStreaming.Settings.secretClear}
                    defaultText="Clear on save"
                  />
                </Button>
              )}
            </span>
          </span>
        );
      default:
        return (
          <Input
            id={inputId}
            value={value === undefined || value === null ? '' : String(value)}
            disabled={readOnly}
            aria-label={label}
            onChange={event => onChange(meta.key, event.target.value)}
          />
        );
    }
  };

  // Read-only/fixed entries are metadata-only: describe the fixed value, no
  // controls and no reset. Fixed options are how the UI documents constraints
  // inherited from the SRT/WebRTC libraries.
  if (meta.readOnly) {
    return (
      <div className={styles.settingField} data-field-key={meta.key}>
        <div className={styles.settingHeader}>
          <span className={styles.settingLabel}>{label}</span>
          <span className={styles.settingBadges}>
            <Tag>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.fixed}
                defaultText="Fixed"
              />
            </Tag>
            <ApplyModeBadge applyMode={meta.applyMode} />
          </span>
        </div>
        <div className={settingInputClassName}>
          <Text>{fixedValueLabel(meta.default)}</Text>
        </div>
        <p className={styles.settingDescription}>{meta.description}</p>
      </div>
    );
  }

  return (
    <div className={styles.settingField} data-field-key={meta.key}>
      <div className={styles.settingHeader}>
        <label className={styles.settingLabel} htmlFor={inputId}>
          {label}
        </label>
        <span className={styles.settingBadges}>
          {modified && (
            <Tag color="blue" className={styles.modifiedTag}>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.modified}
                defaultText="Modified"
              />
            </Tag>
          )}
          <ApplyModeBadge applyMode={meta.applyMode} pending={pendingRestart} />
          <Tooltip
            title={
              <span>
                <Translation
                  translationKey={Localization.Admin.LiveStreaming.Settings.defaultValue}
                  defaultText="Default:"
                />{' '}
                {Array.isArray(defaultValue)
                  ? defaultValue.join(', ') || '(empty)'
                  : String(defaultValue ?? '(empty)')}
              </span>
            }
          >
            <Button
              size="small"
              type="link"
              disabled={readOnly || !modified}
              onClick={handleResetClick}
            >
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.resetToDefault}
                defaultText="Reset"
              />
            </Button>
          </Tooltip>
        </span>
      </div>
      <div className={settingInputClassName} data-list-input={isListField ? 'true' : undefined}>
        {renderInput()}
      </div>
      <p className={styles.settingDescription}>{meta.description}</p>
      {disabled && disabledReason && <Alert type="info" showIcon message={disabledReason} />}
      {error && <Alert type="error" showIcon className={styles.settingError} message={error} />}
    </div>
  );
};

export default SettingField;
