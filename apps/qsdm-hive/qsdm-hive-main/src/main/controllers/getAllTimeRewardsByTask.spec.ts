import { getAllTimeRewards } from './getAllTimeRewards';
import { getAllTimeRewardsByTask } from './getAllTimeRewardsByTask';

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'k2-compat',
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: jest.fn(),
}));

jest.mock('main/services/qsdmTaskStake', () => ({
  getConfirmedQsdmTaskState: jest.fn(),
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
  });

  it('returns task rewards', async () => {
    const taskId = 'exampleTaskId';

    const result = await getAllTimeRewardsByTask({} as Event, { taskId });

    expect(getAllTimeRewards).toHaveBeenCalled();
    expect(result).toBe(1000);
  });
});
