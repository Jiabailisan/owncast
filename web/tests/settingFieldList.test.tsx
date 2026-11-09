import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import { SettingField } from '../components/admin/config/live-streaming/SettingField';
import { FieldMeta } from '../interfaces/settings-metadata.model';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

// rc-select schedules dropdown work through MessageChannel, which jsdom lacks.
class MockMessageChannel {
  port1 = { onmessage: null, postMessage: jest.fn(), close: jest.fn() };

  port2 = { onmessage: null, postMessage: jest.fn(), close: jest.fn() };
}

beforeAll(() => {
  (global as unknown as { MessageChannel: unknown }).MessageChannel = MockMessageChannel;
});

const baseMeta: FieldMeta = {
  key: 'allowedCIDRs',
  group: 'srt.auth',
  type: 'cidrList',
  default: [],
  description: 'Optional allow-list of publisher networks.',
  secret: false,
  applyMode: 'live',
  readOnly: false,
};

const renderField = (overrides: Partial<Parameters<typeof SettingField>[0]> = {}) =>
  render(
    <SettingField
      meta={baseMeta}
      value={[]}
      defaultValue={[]}
      modified={false}
      onChange={jest.fn()}
      onReset={jest.fn()}
      {...overrides}
    />,
  );

describe('SettingField list controls', () => {
  test('renders a full-width tags control for a CIDR list with multiple entries', () => {
    renderField({
      value: ['10.0.0.0/8', '192.168.0.0/16', '2001:db8::/32'],
    });

    const control = screen.getByTestId('setting-list-allowedCIDRs');
    expect(control).toBeInTheDocument();
    expect(control).toHaveClass('ant-select');

    // The list wrapper opts out of the narrow single-value max-width so the
    // control fills its form column.
    const row = document.querySelector('[data-field-key="allowedCIDRs"]') as HTMLElement;
    const listWrapper = row.querySelector('[data-list-input="true"]') as HTMLElement;
    expect(listWrapper).toBeInTheDocument();
    expect(row.querySelector('.settingInput, [class*="settingInput"]')).toBe(listWrapper);

    // Every entry is rendered as a selection tag instead of collapsing.
    ['10.0.0.0/8', '192.168.0.0/16', '2001:db8::/32'].forEach(value => {
      expect(screen.getByText(value)).toBeInTheDocument();
    });
  });

  test('empty CIDR list shows a readable add placeholder and an inline empty popup', async () => {
    renderField();

    // Placeholder comes from the localizable addCidr key (identity in tests).
    expect(screen.getByText('Admin.LiveStreaming.Settings.addCidr')).toBeInTheDocument();

    fireEvent.mouseDown(screen.getByTestId('setting-list-allowedCIDRs'));
    await waitFor(() =>
      expect(screen.getByText('Type a value and press Enter')).toBeInTheDocument(),
    );
  });

  test('string lists use the generic add placeholder', () => {
    renderField({
      meta: { ...baseMeta, key: 'stunServers', type: 'stringList' },
    });
    expect(screen.getByText('Admin.LiveStreaming.Settings.addValue')).toBeInTheDocument();
  });

  test('renders a disabled tags control in read-only state', () => {
    renderField({ disabled: true });
    expect(screen.getByTestId('setting-list-allowedCIDRs')).toHaveClass('ant-select-disabled');
  });

  test('adds a typed value to the list on Enter without losing existing entries', async () => {
    const onChange = jest.fn();
    renderField({ value: ['10.0.0.0/8'], onChange });

    fireEvent.mouseDown(screen.getByTestId('setting-list-allowedCIDRs'));
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: '192.168.0.0/16' } });
    fireEvent.keyDown(input, { key: 'Enter', keyCode: 13, which: 13 });

    await waitFor(() => expect(onChange).toHaveBeenCalled());
    const [key, next] = onChange.mock.calls[onChange.mock.calls.length - 1];
    expect(key).toBe('allowedCIDRs');
    expect(next).toEqual(expect.arrayContaining(['10.0.0.0/8', '192.168.0.0/16']));
  });

  test('enum selects reserve room for long values and full option labels', async () => {
    const meta: FieldMeta = {
      ...baseMeta,
      key: 'h264Profile',
      type: 'enum',
      enum: ['constrained-baseline', 'baseline', 'main', 'high'],
      default: 'constrained-baseline',
    };
    renderField({ meta, value: 'constrained-baseline' });

    const control = screen.getByTestId('setting-enum-h264Profile');
    expect(control.className).toContain('enumSelect');
    fireEvent.mouseDown(control);
    await waitFor(() =>
      expect(screen.getAllByText('constrained-baseline').length).toBeGreaterThan(0),
    );
    ['constrained-baseline', 'baseline', 'main', 'high'].forEach(label => {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    });
  });

  test('TURN server lists also use the full-width list layout', () => {
    renderField({
      meta: {
        ...baseMeta,
        key: 'turnServers',
        type: 'turnServerList',
        default: [],
      },
      value: [],
    });
    const row = document.querySelector('[data-field-key="turnServers"]') as HTMLElement;
    expect(row.querySelector('[data-list-input="true"]')).toBeInTheDocument();
    expect(screen.getByText('Add TURN server')).toBeInTheDocument();
  });
});
