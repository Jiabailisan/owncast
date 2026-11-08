import {
  defaultsFromMetadata,
  fieldErrorsToMap,
  groupSettingsFields,
  humanizeFieldKey,
  FieldMeta,
} from '../interfaces/settings-metadata.model';

const field = (overrides: Partial<FieldMeta>): FieldMeta => ({
  key: 'enabled',
  group: 'srt.listener',
  type: 'bool',
  default: false,
  description: '',
  secret: false,
  applyMode: 'live',
  readOnly: false,
  ...overrides,
});

describe('settings metadata helpers', () => {
  test('humanizeFieldKey produces readable labels', () => {
    expect(humanizeFieldKey('enabled')).toBe('Enabled');
    expect(humanizeFieldKey('receiverLatencyMs')).toBe('Receiver Latency (ms)');
    expect(humanizeFieldKey('streamIdFormats')).toBe('Stream ID Formats');
    expect(humanizeFieldKey('udpPortMin')).toBe('UDP Port Min');
  });

  test('defaultsFromMetadata maps keys to their defaults', () => {
    expect(
      defaultsFromMetadata([
        field({ key: 'port', default: 8890 }),
        field({ key: 'enabled', default: false }),
      ]),
    ).toEqual({ port: 8890, enabled: false });
  });

  test('groupSettingsFields follows the declared group order and appends unknowns', () => {
    const grouped = groupSettingsFields(
      [
        field({ key: 'port', group: 'srt.listener' }),
        field({ key: 'passphrase', group: 'srt.auth' }),
        field({ key: 'mystery', group: 'not.declared' }),
      ],
      [
        { id: 'srt.listener', label: 'Listener' },
        { id: 'srt.auth', label: 'Authentication' },
      ],
    );
    expect(grouped.map(section => section.group.id)).toEqual([
      'srt.listener',
      'srt.auth',
      '_ungrouped',
    ]);
  });

  test('fieldErrorsToMap converts the API list into per-field messages', () => {
    expect(
      fieldErrorsToMap([
        { field: 'port', message: 'must not collide with the web port' },
        { field: 'passphrase', message: 'too short' },
      ]),
    ).toEqual({
      port: 'must not collide with the web port',
      passphrase: 'too short',
    });
    expect(fieldErrorsToMap(null)).toEqual({});
  });
});
