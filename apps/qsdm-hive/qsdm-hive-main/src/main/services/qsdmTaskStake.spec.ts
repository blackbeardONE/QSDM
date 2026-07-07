/**
 * @jest-environment node
 */

import axios from 'axios';

const activeSender =
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const foreignSender =
  'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_CELL_DECIMALS: 9,
  buildQsdmTaskReadUrls: (path: string) => [
    `http://127.0.0.1:8080/api/v1/${path.replace(/^\/+/, '')}`,
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
  ],
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () => activeSender,
}));

jest.mock('main/services/tasks-cache-utils', () => ({
  getTaskDataFromCache: jest.fn(),
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('qsdmTaskStake', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
  });

  it('normalizes native CELL amounts to Hive base units', async () => {
    const {
      normalizeQsdmNativeCellAmountMapToDenomination,
      normalizeQsdmNativeCellAmountToDenomination,
    } = await import('./qsdmTaskStake');

    expect(normalizeQsdmNativeCellAmountToDenomination(2)).toBe(2000000000);
    expect(normalizeQsdmNativeCellAmountToDenomination(2.5)).toBe(2500000000);
    expect(normalizeQsdmNativeCellAmountToDenomination(2000000000)).toBe(
      2000000000
    );
    expect(
      normalizeQsdmNativeCellAmountMapToDenomination({
        [activeSender]: 3,
        [foreignSender]: 4000000000,
      })
    ).toEqual({
      [activeSender]: 3000000000,
      [foreignSender]: 4000000000,
    });
  });

  it('reports stake owned by the active QSDM signer', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          participants: {
            [activeSender]: {
              sender: activeSender,
              stake: 2.5,
              running: true,
            },
          },
        },
      },
    });

    const { getQsdmTaskStakeOwnership } = await import('./qsdmTaskStake');
    const ownership = await getQsdmTaskStakeOwnership('task-1');

    expect(ownership.currentStakeCell).toBe(2.5);
    expect(ownership.currentStakeDenomination).toBe(2500000000);
    expect(ownership.foreignParticipants).toEqual([]);
    expect(ownership.runningForCurrentSender).toBe(true);
  });

  it('separates stake owned by another signer from the active signer', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          participants: {
            [foreignSender]: {
              sender: foreignSender,
              stake: 2,
              running: true,
            },
          },
        },
      },
    });

    const { getQsdmTaskStakeOwnership } = await import('./qsdmTaskStake');
    const ownership = await getQsdmTaskStakeOwnership('task-1');

    expect(ownership.currentStakeCell).toBe(0);
    expect(ownership.currentStakeDenomination).toBe(0);
    expect(ownership.foreignStakeCell).toBe(2);
    expect(ownership.foreignParticipants).toEqual([
      {
        sender: foreignSender,
        stakeCell: 2,
        running: true,
      },
    ]);
    expect(ownership.runningForOtherSender).toBe(true);
  });
});
