import { Meta, StoryObj } from '@storybook/nextjs';
import { SettingsForm } from './SettingsForm';
import { FieldMeta } from '../../../../interfaces/settings-metadata.model';

const groups = [
  { id: 'srt.listener', label: 'Listener' },
  { id: 'srt.latency', label: 'Latency and reliability' },
  { id: 'srt.encryption', label: 'Encryption' },
  { id: 'srt.auth', label: 'Authentication' },
];

const fields: FieldMeta[] = [
  {
    key: 'enabled',
    group: 'srt.listener',
    type: 'bool',
    default: false,
    description: 'Enable the SRT ingest listener. Disabled by default.',
    secret: false,
    applyMode: 'restart-service',
    readOnly: false,
  },
  {
    key: 'bindAddress',
    group: 'srt.listener',
    type: 'string',
    default: '0.0.0.0',
    description: 'IPv4/IPv6 address the SRT listener binds to.',
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
    description: 'UDP port the SRT listener binds. Must not collide with other Owncast ports.',
    secret: false,
    applyMode: 'restart-service',
    readOnly: false,
  },
  {
    key: 'receiverLatencyMs',
    group: 'srt.latency',
    type: 'int',
    default: 120,
    min: 20,
    max: 10000,
    unit: 'ms',
    description: 'Receiver latency buffer. Keep about 4x the path RTT.',
    secret: false,
    applyMode: 'restart-stream',
    readOnly: false,
  },
  {
    key: 'streamIdFormats',
    group: 'srt.auth',
    type: 'stringList',
    default: ['raw-key', 'publish:/live/<key>'],
    description: 'Accepted streamid formats.',
    secret: false,
    applyMode: 'live',
    readOnly: false,
  },
  {
    key: 'allowedCIDRs',
    group: 'srt.auth',
    type: 'cidrList',
    default: [],
    description: 'Optional allow-list of publisher networks.',
    secret: false,
    applyMode: 'live',
    readOnly: false,
  },
  {
    key: 'passphrase',
    group: 'srt.encryption',
    type: 'secret',
    default: '',
    description: 'Optional passphrase used to encrypt the SRT link.',
    secret: true,
    applyMode: 'restart-service',
    readOnly: false,
  },
];

const values: Record<string, unknown> = {
  enabled: true,
  bindAddress: '0.0.0.0',
  port: 8890,
  receiverLatencyMs: 120,
  streamIdFormats: ['raw-key', 'publish:/live/<key>'],
  allowedCIDRs: [],
  passphraseSet: true,
};

const defaults: Record<string, unknown> = {
  enabled: false,
  bindAddress: '0.0.0.0',
  port: 8890,
  receiverLatencyMs: 120,
  streamIdFormats: ['raw-key', 'publish:/live/<key>', '#!::r=<app>/<key>,m=publish'],
  allowedCIDRs: [],
  passphrase: '',
};

const meta = {
  title: 'owncast/Admin/Live Streaming/SettingsForm',
  component: SettingsForm,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SettingsForm>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: {
    fields,
    groups,
    values,
    defaults,
    pendingRestart: { pending: true, fields: ['port'] },
    onSave: async () => {},
  },
};

export const ValidationErrors: Story = {
  args: {
    ...Default.args,
    pendingRestart: { pending: false, fields: [] },
    fieldErrors: {
      port: 'must not collide with the web port',
      receiverLatencyMs: 'must be at least 20',
    },
  },
};

export const ReadOnly: Story = {
  args: {
    ...Default.args,
    disabledReason: 'Ignored while a custom frontend directory is active.',
  },
};
