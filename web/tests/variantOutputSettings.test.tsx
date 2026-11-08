import { useState } from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import { VariantOutputSettings } from '../components/admin/VariantOutputSettings';
import { VariantOutputs } from '../types/config-section';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

class MockMessageChannel {
  port1 = { onmessage: null, postMessage: jest.fn(), close: jest.fn() };

  port2 = { onmessage: null, postMessage: jest.fn(), close: jest.fn() };
}

beforeAll(() => {
  (global as unknown as { MessageChannel: unknown }).MessageChannel = MockMessageChannel;
});

const openSelect = (testId: string) => {
  const element = screen.getByTestId(testId);
  const selector = element.closest('.ant-select')?.querySelector('.ant-select-selector') || element;
  fireEvent.mouseDown(selector);
};

const selectOption = (testId: string, optionText: string) => {
  openSelect(testId);
  fireEvent.click(screen.getByText(optionText));
};

// Stateful harness: the component is fully controlled, so the test parent owns
// the mapping state as the modal does.
const Harness = ({
  initial,
  onChange,
  disabled = false,
}: {
  initial?: VariantOutputs;
  onChange?: (outputs: VariantOutputs | undefined) => void;
  disabled?: boolean;
}) => {
  const [value, setValue] = useState<VariantOutputs | undefined>(initial);
  return (
    <VariantOutputSettings
      value={value}
      disabled={disabled}
      onChange={outputs => {
        setValue(outputs);
        onChange?.(outputs);
      }}
    />
  );
};

describe('VariantOutputSettings', () => {
  test('renders a legacy variant as HLS-enabled with WebRTC off and the documented notes', () => {
    render(<Harness />);
    expect(screen.getByTestId('variant-output-settings')).toBeInTheDocument();
    expect(screen.getByTestId('hls-output-status')).toBeInTheDocument();
    expect(screen.getByTestId('webrtc-output-enabled')).not.toBeChecked();
    expect(screen.getByText('Admin.VideoVariantForm.implicitEnableNote')).toBeInTheDocument();
    expect(screen.queryByTestId('webrtc-output-mode')).not.toBeInTheDocument();
  });

  test('enabling WebRTC emits an explicit enabled mapping with inherit mode', async () => {
    const onChange = jest.fn();
    render(<Harness onChange={onChange} />);
    fireEvent.click(screen.getByTestId('webrtc-output-enabled'));
    await waitFor(() => expect(onChange).toHaveBeenCalledWith({ webrtc: { enabled: true } }));
  });

  test('copy mode shows and emits the copy fallback with the H.264/B-frame note', async () => {
    const onChange = jest.fn();
    render(
      <Harness initial={{ webrtc: { enabled: true, mode: 'inherit' } }} onChange={onChange} />,
    );
    selectOption('webrtc-output-mode', 'Admin.VideoVariantForm.modeCopy');

    await waitFor(() => expect(onChange).toHaveBeenCalled());
    expect(onChange.mock.calls[onChange.mock.calls.length - 1][0]).toEqual({
      webrtc: { enabled: true, mode: 'copy', copyFallback: 'transcode' },
    });
    expect(screen.getByTestId('webrtc-copy-fallback')).toBeInTheDocument();
    expect(screen.getByText('Admin.VideoVariantForm.copyNote')).toBeInTheDocument();
  });

  test('disabling an existing mapping emits enabled:false and preserves overrides', async () => {
    const onChange = jest.fn();
    render(
      <Harness
        initial={{ webrtc: { enabled: true, mode: 'transcode', videoBitrate: 2500 } }}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByTestId('webrtc-output-enabled'));
    await waitFor(() =>
      expect(onChange).toHaveBeenCalledWith({
        webrtc: { enabled: false, mode: 'transcode', videoBitrate: 2500 },
      }),
    );
  });

  test('can be fully disabled by the parent (e.g. read-only)', () => {
    render(<Harness initial={{ webrtc: { enabled: true, mode: 'transcode' } }} disabled />);
    expect(screen.getByTestId('webrtc-output-enabled')).toBeDisabled();
  });
});
