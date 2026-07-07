import { getAllTimeRewards } from './getAllTimeRewards';
import { getAllTimeRewardsByTask } from './getAllTimeRewardsByTask';

const mockGetQsdmMinerProtocolRewardInfo = jest.fn();

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
}));

jest.mock('main/services/qsdmMinerProtocolRewards', () => ({
  getQsdmMinerProtocolRewardInfo: () => mockGetQsdmMinerProtocolRewardInfo(),
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: jest.fn(),
}));

jest.mock('main/services/qsdmTaskStake', () => ({
  getConfirmedQsdmTaskState: jest.fn(),
  getQsdmTaskParticipantBySender: jest.fn(),
  qsdmCellToDenomination: (amount: number) => Math.round(amount * 10 ** 9),
  readFiniteNumber: (value: unknown) => {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  },
}));

jest.mock('./getAllTimeRewards', () => ({
  __esModule: true,
  getAllTimeRewards: jest.fn().mockReturnValue({ exampleTaskId: 1000 }),
}));

describe('getAllTimeRewardsByTask', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetQsdmMinerProtocolRewardInfo.mockReset();
  });

  it('returns task rewards', async () => {
    const taskId = 'exampleTaskId';

    const result = await getAllTimeRewardsByTask({} as Event, { taskId });

    expect(getAllTimeRewards).toHaveBeenCalled();
    expect(result).toBe(1000);
  });

  it('falls back to cached miner rewards when the public gateway times out', async () => {
    mockGetQsdmMinerProtocolRewardInfo.mockRejectedValue(
      new Error('timeout of 10000ms exceeded')
    );
    (getAllTimeRewards as jest.Mock).mockReturnValueOnce({
      'qsdm-system-miner': 42,
    });

    const result = await getAllTimeRewardsByTask({} as Event, {
      taskId: 'qsdm-system-miner',
    });

    expect(result).toBe(42);
    expect(getAllTimeRewards).toHaveBeenCalled();
  });
});
