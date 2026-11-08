import { Meta, StoryObj } from '@storybook/nextjs';

import { ProcessModelCard } from './ProcessModelCard';

const makeResponse = (processModel: 'shared' | 'isolated', pending = false) => ({
  success: true,
  config: { processModel },
  defaults: { processModel: 'shared' as const },
  pendingRestart: { pending, fields: pending ? ['processModel'] : [] },
});

const meta = {
  title: 'owncast/Admin/Process model',
  component: ProcessModelCard,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ProcessModelCard>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Shared: Story = {
  args: { initialModel: 'shared', saver: async processModel => makeResponse(processModel) },
};

export const Isolated: Story = {
  args: {
    initialModel: 'isolated',
    saver: async processModel => makeResponse(processModel, true),
  },
};

export const LoadError: Story = {
  args: {
    loader: async () => {
      throw new Error('output configuration endpoint unavailable');
    },
    saver: async processModel => makeResponse(processModel),
  },
};
