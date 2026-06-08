/**
 * @jest-environment node
 */

export {};

const sender =
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const getCurrentSlot = jest.fn();
const submitQsdmTaskActionIntent = jest.fn();
const getTaskDataFromCache = jest.fn();
const updateTaskCacheRecord = jest.fn();

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () =>
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
}));

jest.mock('main/controllers/getCurrentSlot', () => ({
  __esModule: true,
  default: getCurrentSlot,
}));

jest.mock('main/services/qsdmTaskActions', () => ({
  submitQsdmTaskActionIntent,
}));

jest.mock('main/services/tasks-cache-utils', () => ({
  getTaskDataFromCache,
  updateTaskCacheRecord,
}));

describe('qsdmNativeNamespace', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    delete process.env.QSDM_NATIVE_SUBMISSION_METHODS;
    getCurrentSlot.mockResolvedValue(321);
    getTaskDataFromCache.mockResolvedValue({
      submissions: {
        '7': {
          otherSender: {
            submission_value: 'old-proof',
            slot: 1,
          },
        },
      },
    });
    submitQsdmTaskActionIntent.mockResolvedValue({
      action_id: 'hive_submit_1',
      status: 'accepted',
    });
  });

  it('submits a signed native task proof for checkSubmissionAndUpdateRound', async () => {
    const { tryHandleQsdmNativeNamespaceCall } = await import(
      './qsdmNativeNamespace'
    );

    const result = await tryHandleQsdmNativeNamespaceCall({
      taskId: 'task-1',
      method: 'checkSubmissionAndUpdateRound',
      params: ['proof-cid', 7],
      taskData: {
        bounty_amount_per_round: 1.25,
      },
    });

    expect(submitQsdmTaskActionIntent).toHaveBeenCalledWith({
      taskId: 'task-1',
      action: 'submit',
      payload: {
        source: 'qsdm-hive',
        namespace_method: 'checkSubmissionAndUpdateRound',
        round: 7,
        slot: 321,
        submission_value: 'proof-cid',
        reward_amount: 1.25,
      },
    });
    expect(updateTaskCacheRecord).toHaveBeenCalledWith(
      'task-1',
      {
        submissions: {
          '7': {
            otherSender: {
              submission_value: 'old-proof',
              slot: 1,
            },
            [sender]: {
              submission_value: 'proof-cid',
              slot: 321,
              reward_amount: 1.25,
            },
          },
        },
      },
      'submissions'
    );
    expect(result).toEqual({
      handled: true,
      response: {
        action_id: 'hive_submit_1',
        status: 'accepted',
      },
    });
  });

  it('passes through namespace calls that are not native submission methods', async () => {
    const { tryHandleQsdmNativeNamespaceCall } = await import(
      './qsdmNativeNamespace'
    );

    const result = await tryHandleQsdmNativeNamespaceCall({
      taskId: 'task-1',
      method: 'storeGet',
      params: ['key'],
    });

    expect(submitQsdmTaskActionIntent).not.toHaveBeenCalled();
    expect(updateTaskCacheRecord).not.toHaveBeenCalled();
    expect(result).toEqual({ handled: false });
  });
});
