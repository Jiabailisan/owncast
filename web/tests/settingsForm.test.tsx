import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';

import { SettingsForm } from '../components/admin/config/live-streaming/SettingsForm';
import { FieldMeta } from '../interfaces/settings-metadata.model';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const groups = [
  { id: 'srt.listener', label: 'Listener' },
  { id: 'srt.encryption', label: 'Encryption' },
];

const fields: FieldMeta[] = [
  {
    key: 'enabled',
    group: 'srt.listener',
    type: 'bool',
    default: false,
    description: 'Enable the SRT listener.',
    secret: false,
    applyMode: 'restart-service',
    readOnly: false,
  },
  {
    key: 'port',
    group: 'srt.listener',
    type: 'int',
    default: 8890,
    min: 1024,
    max: 65535,
    unit: 'port',
    description: 'UDP port for the SRT listener.',
    secret: false,
    applyMode: 'restart-service',
    readOnly: false,
  },
  {
    key: 'passphrase',
    group: 'srt.encryption',
    type: 'secret',
    default: '',
    description: 'Link encryption passphrase.',
    secret: true,
    applyMode: 'restart-service',
    readOnly: false,
  },
];

const values = { enabled: false, port: 8890, passphraseSet: false };
const defaults = { enabled: false, port: 8890, passphrase: '' };

const renderForm = (overrides: Record<string, unknown> = {}, onSave = jest.fn()) =>
  render(
    <SettingsForm
      fields={fields}
      groups={groups}
      values={{ ...values }}
      defaults={{ ...defaults }}
      onSave={onSave}
      {...overrides}
    />,
  );

describe('SettingsForm', () => {
  test('renders fields from metadata with humanized labels and descriptions', () => {
    renderForm();
    expect(screen.getByText('Enabled')).toBeInTheDocument();
    expect(screen.getByText('Port')).toBeInTheDocument();
    expect(screen.getByText('Passphrase')).toBeInTheDocument();
    expect(screen.getByText('UDP port for the SRT listener.')).toBeInTheDocument();
    expect(screen.getByText('Listener')).toBeInTheDocument();
    expect(screen.getByText('Encryption')).toBeInTheDocument();
  });

  test('saves only the changed fields as a partial patch', async () => {
    const onSave = jest.fn().mockResolvedValue(undefined);
    renderForm({}, onSave);

    fireEvent.click(screen.getByRole('switch'));
    await waitFor(() => expect(screen.getByText('Save changes')).toBeInTheDocument());
    fireEvent.click(screen.getByText('Save changes'));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ enabled: true });
  });

  test('reset to default sends the metadata default value', async () => {
    const onSave = jest.fn().mockResolvedValue(undefined);
    renderForm({}, onSave);

    const portRow = document.querySelector('[data-field-key="port"]') as HTMLElement;
    const portInput = screen.getByLabelText('Port');
    fireEvent.change(portInput, { target: { value: '9000' } });
    fireEvent.blur(portInput);

    const resetButton = await within(portRow).findByText('Reset');
    fireEvent.click(resetButton);
    fireEvent.click(screen.getByText('Save changes'));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ port: 8890 });
    expect(onSave.mock.calls[0][1]).toEqual(['port']);
  });

  test('shows server-side field errors under the field', () => {
    renderForm({ fieldErrors: { port: 'must not collide with the web port' } });
    expect(screen.getByText('must not collide with the web port')).toBeInTheDocument();
  });

  test('shows the pending restart banner with affected fields', () => {
    renderForm({
      pendingRestart: { pending: true, fields: ['port'] },
    });
    expect(screen.getByText('Restart pending')).toBeInTheDocument();
    expect(screen.getAllByText(/Port/).length).toBeGreaterThan(0);
  });

  test('sends a new secret value and reports a set secret', async () => {
    const onSave = jest.fn().mockResolvedValue(undefined);
    renderForm({ values: { ...values, passphraseSet: true } }, onSave);

    const secretInput = screen.getByLabelText('Passphrase');
    fireEvent.change(secretInput, { target: { value: 'super-secret-passphrase' } });
    fireEvent.click(screen.getByText('Save changes'));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ passphrase: 'super-secret-passphrase' });
  });

  test('shows a load error with a retry action', () => {
    const onRetry = jest.fn();
    renderForm({ loadError: 'Could not reach the server', onRetry });
    expect(screen.getByText('Could not reach the server')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Retry'));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  test('resetting a turnServerList sends an array default instead of an empty string and Cancel clears secret drafts', async () => {
    const onSave = jest.fn().mockResolvedValue(undefined);
    const turnFields: FieldMeta[] = [
      ...fields,
      {
        key: 'turnServers',
        group: 'srt.listener',
        type: 'turnServerList',
        default: [],
        description: 'TURN servers.',
        secret: true,
        applyMode: 'restart-service',
        readOnly: false,
      },
    ];
    renderForm(
      {
        fields: turnFields,
        values: {
          ...values,
          port: 9001,
          turnServers: [{ url: 'turn:example.com:3478', username: 'u', credentialSet: true }],
        },
        defaults: { ...defaults, turnServers: [] },
      },
      onSave,
    );

    // Non-default saved field (port=9001 vs default 8890) enables Reset immediately.
    const portRow = document.querySelector('[data-field-key="port"]') as HTMLElement;
    expect(within(portRow).getByRole('button', { name: 'Reset' })).not.toBeDisabled();

    // Typing a secret then clicking Cancel clears the draft input.
    const secretInput = screen.getByLabelText('Passphrase') as HTMLInputElement;
    fireEvent.change(secretInput, { target: { value: 'draft-secret' } });
    expect(secretInput.value).toBe('draft-secret');
    fireEvent.click(screen.getByText('Cancel changes'));
    expect(secretInput.value).toBe('');

    // Resetting turnServers (secret: true, type: turnServerList) sends [] not "".
    const turnRow = document.querySelector('[data-field-key="turnServers"]') as HTMLElement;
    fireEvent.click(within(turnRow).getByRole('button', { name: 'Reset' }));
    fireEvent.click(screen.getByText('Save changes'));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toEqual({ turnServers: [] });
  });
});
