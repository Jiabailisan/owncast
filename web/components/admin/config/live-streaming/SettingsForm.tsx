import { FC, useMemo, useState } from 'react';
import { Alert, Button, Space, Typography } from 'antd';

import {
  FieldMeta,
  PendingRestart,
  SettingsGroupMeta,
  groupSettingsFields,
  humanizeFieldKey,
} from '../../../../interfaces/settings-metadata.model';
import { Localization } from '../../../../types/localization';
import { Translation } from '../../../ui/Translation/Translation';
import { SettingField } from './SettingField';
import styles from './settings-form.module.scss';

const { Title, Text } = Typography;

export type SettingsFormProps = {
  fields: FieldMeta[];
  groups?: SettingsGroupMeta[];
  // Current saved values and the server-provided defaults for this section.
  values: Record<string, unknown>;
  defaults: Record<string, unknown>;
  pendingRestart?: PendingRestart | null;
  fieldErrors?: Record<string, string>;
  // Server/config errors that prevented loading the section.
  loadError?: string | null;
  // Why the section is read-only (e.g. custom frontend overrides appearance).
  disabledReason?: string;
  onSave: (patch: Record<string, unknown>, resetKeys: string[]) => Promise<unknown> | void;
  onRetry?: () => void;
};

// stableStringify renders a value with sorted object keys so two structurally equal
// values compare equal. String() was used before and turned every object into
// "[object Object]", which made an edited TURN server look unchanged and dropped
// the edit before it reached the server.
function stableStringify(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map(item => stableStringify(item)).join(',')}]`;
  }

  if (value && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>).sort(([left], [right]) =>
      left.localeCompare(right),
    );

    return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${stableStringify(item)}`).join(',')}}`;
  }

  return JSON.stringify(value) ?? 'undefined';
}

function valuesEqual(a: unknown, b: unknown): boolean {
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((item, index) => valuesEqual(item, b[index]));
  }

  if (a && b && typeof a === 'object' && typeof b === 'object') {
    return stableStringify(a) === stableStringify(b);
  }

  return a === b;
}

// Metadata-driven configuration form. Renders exactly the fields the server
// describes (single source of truth), tracks a partial patch of only the
// operator's changes, and supports per-field reset-to-default and validation
// errors returned by the server.
export const SettingsForm: FC<SettingsFormProps> = ({
  fields,
  groups = [],
  values,
  defaults,
  pendingRestart = null,
  fieldErrors = {},
  loadError = null,
  disabledReason,
  onSave,
  onRetry,
}) => {
  const [draft, setDraft] = useState<Record<string, unknown>>({});
  const [resetKeys, setResetKeys] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);

  const sections = useMemo(() => groupSettingsFields(fields, groups), [fields, groups]);

  const isModified = (key: string) =>
    Object.prototype.hasOwnProperty.call(draft, key) || resetKeys.includes(key);

  const displayValue = (meta: FieldMeta): unknown => {
    if (resetKeys.includes(meta.key)) return defaults[meta.key];
    if (Object.prototype.hasOwnProperty.call(draft, meta.key)) return draft[meta.key];
    return values[meta.key];
  };

  const pendingKeys = [
    ...Object.keys(draft),
    ...resetKeys.filter(key => !Object.keys(draft).includes(key)),
  ];

  const handleChange = (key: string, value: unknown) => {
    setResetKeys(current => current.filter(item => item !== key));
    setDraft(current => {
      const next = { ...current };
      if (valuesEqual(value, values[key])) {
        delete next[key];
      } else {
        next[key] = value;
      }
      return next;
    });
  };

  const handleReset = (key: string) => {
    setDraft(current => {
      const next = { ...current };
      delete next[key];
      return next;
    });
    setResetKeys(current => (current.includes(key) ? current : [...current, key]));
  };

  const handleSave = async () => {
    if (pendingKeys.length === 0) return;
    const patch: Record<string, unknown> = {};
    Object.keys(draft).forEach(key => {
      patch[key] = draft[key];
    });
    const resets: string[] = [];
    resetKeys.forEach(key => {
      const meta = fields.find(field => field.key === key);
      if (!meta) return;
      // Resetting a secret to its default means clearing the stored value.
      patch[key] = meta.secret ? '' : defaults[key];
      resets.push(key);
    });

    setSaving(true);
    try {
      await onSave(patch, resets);
      setDraft({});
      setResetKeys([]);
    } catch {
      // The parent surfaces the APIError (including field errors); keep the
      // operator's draft so a partial failure never discards their edits.
    } finally {
      setSaving(false);
    }
  };

  if (loadError) {
    return (
      <Alert
        type="error"
        showIcon
        message={loadError}
        description={
          onRetry ? (
            <Button size="small" onClick={onRetry}>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.retry}
                defaultText="Retry"
              />
            </Button>
          ) : undefined
        }
      />
    );
  }

  return (
    <div className={styles.settingsForm}>
      {pendingRestart?.pending && (
        <Alert
          type="warning"
          showIcon
          className={styles.pendingBanner}
          message={
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Settings.pendingRestart}
              defaultText="Restart pending"
            />
          }
          description={
            <>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.pendingRestartDescription}
                defaultText="The server is still running with the previous values for these settings. Restart the affected service or start a new stream for the change to take effect."
              />{' '}
              <Text code>{pendingRestart.fields.map(key => humanizeFieldKey(key)).join(', ')}</Text>
            </>
          }
        />
      )}

      {sections.map(section => (
        <section key={section.group.id} className={styles.section}>
          {section.group.label && <Title level={4}>{section.group.label}</Title>}
          {section.fields.map(meta => (
            <SettingField
              key={meta.key}
              meta={meta}
              value={displayValue(meta)}
              defaultValue={defaults[meta.key]}
              modified={isModified(meta.key)}
              disabled={Boolean(disabledReason)}
              disabledReason={disabledReason}
              error={fieldErrors[meta.key]}
              secretSet={Boolean(values[meta.key + 'Set'] ?? values.passphraseSet ?? false)}
              pendingRestart={Boolean(pendingRestart?.fields?.includes(meta.key))}
              onChange={handleChange}
              onReset={handleReset}
            />
          ))}
        </section>
      ))}

      <div className={styles.saveBar}>
        <Space wrap>
          <Button
            type="primary"
            loading={saving}
            disabled={pendingKeys.length === 0}
            onClick={handleSave}
          >
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Settings.save}
              defaultText="Save changes"
            />
          </Button>
          <Button
            disabled={pendingKeys.length === 0 || saving}
            onClick={() => {
              setDraft({});
              setResetKeys([]);
            }}
          >
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Settings.resetAll}
              defaultText="Cancel changes"
            />
          </Button>
          <Text type="secondary">
            <Translation
              translationKey={Localization.Admin.LiveStreaming.Settings.modified}
              defaultText="Modified"
            />
            {': '}
            {pendingKeys.length}
          </Text>
        </Space>
      </div>
    </div>
  );
};

export default SettingsForm;
