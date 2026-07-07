import axios from 'axios';

import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';

import { manageQsdmTaskCatalog } from './manageQsdmTaskCatalog';

jest.mock('axios', () => ({
  get: jest.fn(),
  isAxiosError: (error: { isAxiosError?: boolean }) =>
    Boolean(error?.isAxiosError),
}));
jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: jest.fn(),
}));
jest.mock('main/services/qsdmTaskActions', () => ({
  submitQsdmTaskActionIntent: jest.fn(),
}));

const mockedGet = axios.get as jest.Mock;
const mockedGetSender = getQsdmTaskActionSender as jest.Mock;
const mockedSubmit = submitQsdmTaskActionIntent as jest.Mock;
const sender = 'a'.repeat(64);
const draft = {
  task_id: 'shared-edge',
  name: 'Shared Edge',
  active: true,
  runtime: {
    kind: 'capability' as const,
    capability: 'generic-proof-v1',
  },
  round_time: 60,
};

describe('manageQsdmTaskCatalog', () => {
  beforeEach(() => {
    mockedGet.mockReset();
    mockedGetSender.mockReset().mockReturnValue(sender);
    mockedSubmit.mockReset().mockResolvedValue({
      action_id: 'catalog-action-1',
      status: 'accepted',
      sender,
      task_id: 'shared-edge',
      action: 'catalog-register',
      mempool_submitted: true,
      mempool_status: 'submitted',
    });
  });

  it('registers version one when the task does not exist', async () => {
    mockedGet.mockRejectedValue({
      isAxiosError: true,
      response: { status: 404 },
    });
    const response = await manageQsdmTaskCatalog({} as never, {
      operation: 'publish',
      taskId: 'shared-edge',
      draft,
    });
    expect(mockedSubmit).toHaveBeenCalledWith({
      taskId: 'shared-edge',
      action: 'catalog-register',
      payload: expect.objectContaining({
        schema_version: 1,
        task_id: 'shared-edge',
        version: 1,
        manager: sender,
      }),
      waitForCommit: false,
    });
    expect(response).toMatchObject({ created: true, catalogVersion: 1 });
  });

  it('updates only the manager and increments the current version', async () => {
    mockedGet.mockResolvedValue({
      data: {
        task: {
          manifest: { manager: sender, version: 4 },
        },
      },
    });
    await manageQsdmTaskCatalog({} as never, {
      operation: 'publish',
      taskId: 'shared-edge',
      draft,
    });
    expect(mockedSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        action: 'catalog-update',
        payload: expect.objectContaining({ version: 5, manager: sender }),
      })
    );
  });

  it('rejects changes from a different manager wallet', async () => {
    mockedGet.mockResolvedValue({
      data: {
        task: {
          manifest: { manager: 'b'.repeat(64), version: 1 },
        },
      },
    });
    await expect(
      manageQsdmTaskCatalog({} as never, {
        operation: 'pause',
        taskId: 'shared-edge',
      })
    ).rejects.toThrow(/different QSDM wallet/);
    expect(mockedSubmit).not.toHaveBeenCalled();
  });
});
