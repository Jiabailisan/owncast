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

const listGroups = [
  { id: 'srt.auth', label: 'Authentication' },
  { id: 'srt.diagnostics', label: 'Diagnostics' },
  { id: 'webrtc.domain', label: 'Domain and origins' },
  { id: 'webrtc.ice', label: 'ICE servers' },
];

const listFields: FieldMeta[] = [
  {
    key: 'allowedCIDRs',
    group: 'srt.auth',
    type: 'cidrList',
    default: [],
    description: 'Optional allow-list of publisher networks. Empty allows any publisher.',
    secret: false,
    applyMode: 'live',
    readOnly: false,
  },
  {
    key: 'deniedCIDRs',
    group: 'srt.auth',
    type: 'cidrList',
    default: [],
    description: 'Optional deny-list of publisher networks. Takes precedence over the allow-list.',
    secret: false,
    applyMode: 'live',
    readOnly: false,
  },
  {
    key: 'logTopics',
    group: 'srt.diagnostics',
    type: 'stringList',
    default: [],
    description: 'SRT log topics to enable for diagnostics.',
    secret: false,
    applyMode: 'restart-service',
    readOnly: false,
  },
  {
    key: 'allowedOrigins',
    group: 'webrtc.domain',
    type: 'stringList',
    default: [],
    description: 'Extra origins allowed to embed the WebRTC player.',
    secret: false,
    applyMode: 'live',
    readOnly: false,
  },
  {
    key: 'stunServers',
    group: 'webrtc.ice',
    type: 'stringList',
    default: ['stun:stun.l.google.com:19302'],
    description: 'STUN servers used by the browser when negotiating WebRTC.',
    secret: false,
    applyMode: 'live',
    readOnly: false,
  },
];

const listValues: Record<string, unknown> = {
  allowedCIDRs: ['10.0.0.0/8', '192.168.0.0/16', '2001:db8::/32'],
  deniedCIDRs: ['203.0.113.0/24', '198.51.100.0/24'],
  logTopics: ['srt:live', 'srt:congestion'],
  allowedOrigins: ['https://example.org', 'https://embed.example.org'],
  stunServers: ['stun:stun.l.google.com:19302', 'stun:stun1.l.google.com:19302'],
};

const listDefaults: Record<string, unknown> = {
  allowedCIDRs: [],
  deniedCIDRs: [],
  logTopics: [],
  allowedOrigins: [],
  stunServers: ['stun:stun.l.google.com:19302'],
};

// Regression baseline for the list controls: every tags select must fill its
// form column and wrap multiple entries instead of collapsing to a tiny box.
export const ListFieldsWithValues: Story = {
  args: {
    fields: listFields,
    groups: listGroups,
    values: listValues,
    defaults: listDefaults,
    pendingRestart: { pending: false, fields: [] },
    onSave: async () => {},
  },
};

// Empty list controls must keep a readable "add" placeholder and a usable
// dropdown (open it in Storybook to inspect the empty state).
export const EmptyListFields: Story = {
  args: {
    fields: listFields.map(field => ({ ...field, default: [] })),
    groups: listGroups,
    values: {},
    defaults: listDefaults,
    pendingRestart: { pending: false, fields: [] },
    onSave: async () => {},
  },
};

export const ReadOnly: Story = {
  args: {
    ...Default.args,
    disabledReason: 'Ignored while a custom frontend directory is active.',
  },
};

const enumGroups = [{ id: 'webrtc.media', label: 'Media' }];

const enumFields: FieldMeta[] = [
  {
    key: 'h264Profile',
    group: 'webrtc.media',
    type: 'enum',
    default: 'constrained-baseline',
    enum: ['constrained-baseline', 'baseline', 'main', 'high'],
    description: 'H.264 profile used when transcoding for WebRTC.',
    secret: false,
    applyMode: 'restart-stream',
    readOnly: false,
  },
  {
    key: 'videoCodec',
    group: 'webrtc.media',
    type: 'enum',
    default: 'h264_nvenc',
    enum: ['libx264', 'h264_nvenc', 'h264_qsv', 'h264_vaapi', 'h264_videotoolbox'],
    description: 'Encoder preference for the transcoder.',
    secret: false,
    applyMode: 'restart-stream',
    readOnly: false,
  },
];

const enumValues = { h264Profile: 'constrained-baseline', videoCodec: 'h264_videotoolbox' };
const enumDefaults = { h264Profile: 'constrained-baseline', videoCodec: 'h264_nvenc' };

// Regression baseline for long enum values: the collapsed value and the
// expanded dropdown must show the full label (e.g. "constrained-baseline"),
// never "con..."/"bas...".
export const EnumSelects: Story = {
  args: {
    fields: enumFields,
    groups: enumGroups,
    values: enumValues,
    defaults: enumDefaults,
    pendingRestart: { pending: false, fields: [] },
    onSave: async () => {},
  },
};
