import { Meta, StoryObj } from '@storybook/nextjs';
import { SettingField } from './SettingField';
import { FieldMeta } from '../../../../interfaces/settings-metadata.model';

const baseMeta: FieldMeta = {
  key: 'enabled',
  group: 'srt.listener',
  type: 'bool',
  default: false,
  description: 'Enable the SRT ingest listener.',
  secret: false,
  applyMode: 'restart-service',
  readOnly: false,
};

const meta = {
  title: 'owncast/Admin/Live Streaming/SettingField',
  component: SettingField,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SettingField>;

export default meta;
type Story = StoryObj<typeof meta>;

export const BooleanField: Story = {
  args: {
    meta: baseMeta,
    value: true,
    defaultValue: false,
    modified: true,
    onChange: () => {},
    onReset: () => {},
  },
};

export const PortFieldWithUnit: Story = {
  args: {
    meta: {
      ...baseMeta,
      key: 'port',
      type: 'int',
      default: 8890,
      min: 1024,
      max: 65535,
      unit: 'port',
      applyMode: 'restart-service',
    },
    value: 8890,
    defaultValue: 8890,
    modified: false,
    onChange: () => {},
    onReset: () => {},
  },
};

export const SecretField: Story = {
  args: {
    meta: {
      ...baseMeta,
      key: 'passphrase',
      type: 'secret',
      default: '',
      secret: true,
      applyMode: 'restart-service',
    },
    value: undefined,
    defaultValue: '',
    modified: false,
    secretSet: true,
    onChange: () => {},
    onReset: () => {},
  },
};

export const CidrListField: Story = {
  args: {
    meta: {
      ...baseMeta,
      key: 'allowedCIDRs',
      type: 'cidrList',
      default: [],
      applyMode: 'live',
    },
    value: ['10.0.0.0/8', '192.168.0.0/16', '2001:db8::/32'],
    defaultValue: [],
    modified: true,
    onChange: () => {},
    onReset: () => {},
  },
};

export const CidrListEmptyField: Story = {
  args: {
    ...CidrListField.args,
    value: [],
    modified: false,
  },
};

export const StringListField: Story = {
  args: {
    meta: {
      ...baseMeta,
      key: 'allowedOrigins',
      type: 'stringList',
      default: [],
      applyMode: 'live',
    },
    value: ['https://example.org', 'https://embed.example.org'],
    defaultValue: [],
    modified: true,
    onChange: () => {},
    onReset: () => {},
  },
};

export const FieldError: Story = {
  args: {
    meta: {
      ...baseMeta,
      key: 'port',
      type: 'int',
      default: 8890,
      min: 1024,
      max: 65535,
    },
    value: 80,
    defaultValue: 8890,
    modified: true,
    error: 'must not collide with the web port',
    onChange: () => {},
    onReset: () => {},
  },
};
