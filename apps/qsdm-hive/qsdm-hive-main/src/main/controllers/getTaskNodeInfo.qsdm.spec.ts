/**
 * @jest-environment node
 */

export {};

const sender =
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const getStartedTasks = jest.fn();
const getQsdmTaskActionSender = jest.fn();
const getConfirmedQsdmTaskState = jest.fn();
const getCachedQsdmTaskStakeInDenomination = jest.fn();
const getQsdmMinerProtocolRewardInfo = jest.fn();
const mockAxiosGet = jest.fn();

jest.mock('config/qsdm', () => ({
  buildQsdmTaskReadUrls: (path: string) => [
    `http://127.0.0.1:8080/api/v1${path}`,
  ],
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
}));

jest.mock('axios', () => ({
  __esModule: true,
  default: {
    get: mockAxiosGet,
  },
}));

jest.mock('../services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    getStartedTasks,
  },
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender,
}));

jest.mock('main/services/qsdmMinerProtocolRewards', () => ({
  getQsdmMinerProtocolRewardInfo,
}));

jest.mock('main/services/qsdmTaskStake', () => ({
  getCachedQsdmTaskStakeInDenomination,
  getConfirmedQsdmTaskState,
  getQsdmTaskParticipantBySender: (
    participants: Record<string, any> = {},
    requestedSender = ''
  ) => {
    const normalizedSender = String(requestedSender).toLowerCase();
    return Object.entries(participants).find(([key, participant]) => {
      const participantSender = String(participant?.sender || key).toLowerCase();
      return participantSender === normalizedSender;
    })?.[1];
  },
  qsdmCellToDenomination: (amount: number) => Math.round(amount * 10 ** 9),
  readFiniteNumber: (value: unknown) => {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  },
}));

jest.mock('main/services/tasks-cache-utils', () => ({
  getTaskDataFromCache: jest.fn(),
}));

jest.mock('utils', () => ({
  throwDetailedError: jest.fn().mockImplementation((payload) => payload),
}));

const makeTask = (taskId: string, availableBalances = {}) =>
  ({
    task_id: taskId,
    available_balances: availableBalances,
  } as any);

describe('getTaskNodeInfo qsdm-native', () => {
  beforeEach(() => {
    getStartedTasks.mockReset();
    getQsdmTaskActionSender.mockReset();
    getConfirmedQsdmTaskState.mockReset();
    getCachedQsdmTaskStakeInDenomination.mockReset();
    getQsdmMinerProtocolRewardInfo.mockReset();
    mockAxiosGet.mockReset();
    // eslint-disable-next-line global-require
    require('main/services/qsdmHttpRead').clearQsdmReadCircuitState();
    getQsdmTaskActionSender.mockReturnValue(sender);
    getQsdmMinerProtocolRewardInfo.mockResolvedValue(null);
  });

  it('aggregates signer stake and rewards from native QSDM task state', async () => {
    getStartedTasks.mockResolvedValue([
      makeTask('qsdm-system-miner'),
      makeTask('qsdm-cpu-edge-compute'),
    ]);
    mockAxiosGet.mockResolvedValue({
      data: {
        tasks: [
          makeTask('qsdm-system-miner'),
          makeTask('qsdm-cpu-edge-compute'),
        ],
      },
    });
    getConfirmedQsdmTaskState
      .mockResolvedValueOnce({
        task: {
          participants: {
            [sender]: {
              stake: 2,
              pending_reward_amount: 0.5,
              total_reward_claimed_amount: 0.1,
            },
          },
        },
      })
      .mockResolvedValueOnce({
        task: {
          participants: {
            'edge-worker-alias': {
              sender,
              stake: 1.25,
              pending_reward_amount: 0.75,
              total_reward_claimed_amount: 0.25,
            },
          },
        },
      });

    const { default: getTaskNodeInfo } = await import('./getTaskNodeInfo');
    const result = await getTaskNodeInfo({} as Event);

    expect(result).toEqual({
      totalStaked: { CELL: 3250000000 },
      pendingRewards: { CELL: 1250000000 },
      allTimeRewards: { CELL: 350000000 },
    });
  });

  it('adds miner protocol emission rewards to visible sidebar totals', async () => {
    getStartedTasks.mockResolvedValue([makeTask('qsdm-system-miner')]);
    mockAxiosGet.mockResolvedValue({
      data: {
        tasks: [makeTask('qsdm-system-miner')],
      },
    });
    getQsdmMinerProtocolRewardInfo.mockResolvedValue({
      earnedDenomination: 1250000000,
    });
    getConfirmedQsdmTaskState.mockResolvedValue({
      task: {
        participants: {
          [sender]: {
            stake: 2,
            pending_reward_amount: 0,
            total_reward_claimed_amount: 0,
          },
        },
      },
    });

    const { default: getTaskNodeInfo } = await import('./getTaskNodeInfo');
    const result = await getTaskNodeInfo({} as Event);

    expect(getQsdmMinerProtocolRewardInfo).toHaveBeenCalled();
    expect(result).toEqual({
      totalStaked: { CELL: 2000000000 },
      pendingRewards: { CELL: 0 },
      allTimeRewards: { CELL: 1250000000 },
    });
  });

  it('falls back to cached stake and started-task rewards when state is offline', async () => {
    getStartedTasks.mockResolvedValue([
      makeTask('qsdm-system-miner', {
        [sender]: 250000000,
      }),
    ]);
    mockAxiosGet.mockRejectedValue(new Error('offline'));
    getConfirmedQsdmTaskState.mockRejectedValue(new Error('offline'));
    getCachedQsdmTaskStakeInDenomination.mockResolvedValue(2000000000);

    const { default: getTaskNodeInfo } = await import('./getTaskNodeInfo');
    const result = await getTaskNodeInfo({} as Event);

    expect(getCachedQsdmTaskStakeInDenomination).toHaveBeenCalledWith(
      'qsdm-system-miner',
      sender
    );
    expect(result).toEqual({
      totalStaked: { CELL: 2000000000 },
      pendingRewards: { CELL: 250000000 },
      allTimeRewards: { CELL: 0 },
    });
  });

  it('does not include hidden local signed-loop stake in visible sidebar totals', async () => {
    getStartedTasks.mockResolvedValue([]);
    mockAxiosGet.mockResolvedValue({
      data: {
        tasks: [
          makeTask('qsdm-hive-local-task'),
          makeTask('qsdm-system-miner'),
        ],
      },
    });
    getConfirmedQsdmTaskState.mockResolvedValue({
      task: {
        participants: {
          [sender]: {
            stake: 2,
            pending_reward_amount: 0,
          },
        },
      },
    });

    const { default: getTaskNodeInfo } = await import('./getTaskNodeInfo');
    const result = await getTaskNodeInfo({} as Event);

    expect(getConfirmedQsdmTaskState).toHaveBeenCalledTimes(1);
    expect(getConfirmedQsdmTaskState).toHaveBeenCalledWith(
      'qsdm-system-miner'
    );
    expect(result).toEqual({
      totalStaked: { CELL: 2000000000 },
      pendingRewards: { CELL: 0 },
      allTimeRewards: { CELL: 0 },
    });
  });
});
