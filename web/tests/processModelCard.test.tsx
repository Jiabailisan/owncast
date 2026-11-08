import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import { ProcessModelCard } from '../components/admin/ProcessModelCard';

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

const makeResponse = (processModel: 'shared' | 'isolated', pending = false) => ({
  success: true,
  config: { processModel },
  defaults: { processModel: 'shared' as const },
  pendingRestart: { pending, fields: pending ? ['processModel'] : [] },
});

// antd Select's data-testid lands on the inner combobox input; open the
// dropdown through the closest .ant-select wrapper.
const openSelect = (testId: string) => {
  const element = screen.getByTestId(testId);
  const selector = element.closest('.ant-select')?.querySelector('.ant-select-selector') || element;
  fireEvent.mouseDown(selector);
};

const selectOption = (testId: string, optionText: string) => {
  openSelect(testId);
  const options = screen.getAllByText(optionText);
  fireEvent.click(options[options.length - 1]);
};

describe('ProcessModelCard', () => {
  test('renders the current model, apply-mode badge and the model explanations', () => {
    render(<ProcessModelCard initialModel="shared" saver={jest.fn()} />);
    expect(screen.getByTestId('process-model-card')).toBeInTheDocument();
    expect(screen.getByText('Admin.ProcessModel.title')).toBeInTheDocument();
    // ApplyModeBadge renders its default label ("Next stream" for the
    // restart-stream apply mode) when the catalog has no translation.
    expect(screen.getAllByText('Next stream').length).toBeGreaterThan(0);
    // The model labels appear both in the select and in the explanation notes.
    expect(screen.getAllByText('Admin.ProcessModel.sharedLabel').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Admin.ProcessModel.isolatedLabel').length).toBeGreaterThan(0);
    expect(screen.getByTestId('process-model-save')).toBeDisabled();
  });

  test('saves a changed process model and shows the saved confirmation', async () => {
    const saver = jest.fn().mockResolvedValue(makeResponse('isolated'));
    render(<ProcessModelCard initialModel="shared" saver={saver} />);

    selectOption('process-model-select', 'Admin.ProcessModel.isolatedLabel');
    const saveButton = screen.getByTestId('process-model-save');
    expect(saveButton).not.toBeDisabled();
    fireEvent.click(saveButton);

    await waitFor(() => expect(saver).toHaveBeenCalledWith('isolated'));
    await waitFor(() => expect(screen.getByText('Admin.ProcessModel.saved')).toBeInTheDocument());
  });

  test('shows the pending-restart banner while a live stream keeps the old model', async () => {
    const saver = jest.fn().mockResolvedValue(makeResponse('isolated', true));
    render(<ProcessModelCard initialModel="shared" saver={saver} />);
    selectOption('process-model-select', 'Admin.ProcessModel.isolatedLabel');
    fireEvent.click(screen.getByTestId('process-model-save'));

    await waitFor(() =>
      expect(screen.getByText('Admin.ProcessModel.pendingRestartTitle')).toBeInTheDocument(),
    );
  });

  test('surfaces a field-level validation error from the API', async () => {
    const saver = jest.fn().mockRejectedValue(
      Object.assign(new Error('validation failed'), {
        fieldErrors: [{ field: 'processModel', message: 'must be one of shared, isolated' }],
      }),
    );
    render(<ProcessModelCard initialModel="shared" saver={saver} />);
    selectOption('process-model-select', 'Admin.ProcessModel.isolatedLabel');
    fireEvent.click(screen.getByTestId('process-model-save'));

    await waitFor(() =>
      expect(screen.getByText('must be one of shared, isolated')).toBeInTheDocument(),
    );
  });

  test('loads the model from the API', async () => {
    const loader = jest.fn().mockResolvedValue(makeResponse('isolated'));
    render(<ProcessModelCard loader={loader} saver={jest.fn()} />);
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(1));
  });

  test('shows the load error when the endpoint is unavailable', async () => {
    render(
      <ProcessModelCard
        loader={jest.fn().mockRejectedValue(new Error('endpoint unavailable'))}
        saver={jest.fn()}
      />,
    );
    await waitFor(() => expect(screen.getByText('endpoint unavailable')).toBeInTheDocument());
  });
});
