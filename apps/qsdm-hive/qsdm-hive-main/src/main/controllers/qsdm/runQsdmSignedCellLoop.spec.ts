import { Event } from 'electron';

const originalEnv = process.env;

const loadController = async (enabled: boolean) => {
  jest.resetModules();

  process.env = {
    ...originalEnv,
    QSDM_ENABLE_LOCAL_SIGNED_LOOP: enabled ? '1' : '0',
    QSDM_TASK_ACTION_SIGNER: 'cli',
    QSDM_TASK_ACTION_SENDER: 'sender-1',
    QSDM_TASK_ACTION_CLI_PATH: 'qsdmcli',
    QSDM_TASK_ACTION_PASSPHRASE_FILE: 'pass.txt',
    ...(enabled
      ? { QSDM_TASK_ACTION_KEYSTORE_PATH: 'wallet.json' }
      : { QSDM_TASK_ACTION_KEYSTORE_PATH: '' }),
  };

  const axiosMock = {
    get: jest.fn(),
  };
  const submitQsdmTaskActionIntent = jest.fn();

  jest.doMock('axios', () => axiosMock);
  jest.doMock('fs', () => ({
    __esModule: true,
    default: {
      existsSync: jest.fn(() => true),
    },
  }));
  jest.doMock('main/services/qsdmTaskActions', () => ({
    submitQsdmTaskActionIntent,
  }));

  const module = await import('./runQsdmSignedCellLoop');
  return {
    runQsdmSignedCellLoop: module.runQsdmSignedCellLoop,
    axiosMock,
    submitQsdmTaskActionIntent,
  };
};

describe('runQsdmSignedCellLoop', () => {
  afterEach(() => {
    process.env = originalEnv;
    jest.dontMock('axios');
    jest.dontMock('fs');
    jest.dontMock('main/services/qsdmTaskActions');
  });

  it('is disabled unless explicitly enabled for local proof runs', async () => {
    const { runQsdmSignedCellLoop } = await loadController(false);

    await expect(runQsdmSignedCellLoop({} as Event)).rejects.toThrow(
      'QSDM_ENABLE_LOCAL_SIGNED_LOOP=1'
    );
  });

  it('runs fund, start, stake, submit, and claim through the signer', async () => {
    const { runQsdmSignedCellLoop, axiosMock, submitQsdmTaskActionIntent } =
      await loadController(true);
    let nonce = 0;

    axiosMock.get.mockImplementation((url: string) => {
      if (url.endsWith('/status')) {
        return Promise.resolve({ data: { chain_tip: 123 } });
      }
      if (url.endsWith('/tasks/task-1/state')) {
        return Promise.resolve({
          data: {
            task: {
              task_id: 'task-1',
              last_action: 'claim',
              running_count: 1,
              participants: {},
              submissions: {},
            },
          },
        });
      }
      return Promise.resolve({
        data: {
          address: 'sender-1',
          balance: 100 - nonce,
          nonce: nonce++,
          present: true,
        },
      });
    });
    submitQsdmTaskActionIntent.mockImplementation(
      ({ action }: { action: string }) =>
        Promise.resolve({
          action_id: `${action}-id`,
          status: 'accepted',
          sender: 'sender-1',
          task_id: 'task-1',
          action,
          mempool_status: 'submitted',
        })
    );

    const response = await runQsdmSignedCellLoop({} as Event, {
      taskId: 'task-1',
      waitSeconds: 1,
    });

    expect(submitQsdmTaskActionIntent).toHaveBeenCalledTimes(5);
    expect(
      submitQsdmTaskActionIntent.mock.calls.map(([params]) => params.action)
    ).toEqual(['fund', 'start', 'stake', 'submit', 'claim']);
    expect(response).toMatchObject({
      taskId: 'task-1',
      sender: 'sender-1',
      finalNonce: 10,
      taskState: {
        task_id: 'task-1',
        last_action: 'claim',
      },
    });
  });
});
