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
  buildQsdmTaskActionReadUrls: (path: string) => [
    `https://api.qsdm.tech/api/v1/${path.replace(/^\/+/, '')}`,
    `https://api.qsdm.tech/attest/home-validator/api/v1/${path.replace(
      /^\/+/,
      ''
    )}`,
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

  it('does not accept a stale zero stake from the home gateway', async () => {
    mockedAxiosGet.mockImplementation(async (url: string) => {
      if (url.startsWith('https://api.qsdm.tech/api/v1/')) {
        return {
          data: {
            task: {
              participants: {
                [activeSender]: {
                  sender: activeSender,
                  stake: 2,
                  running: false,
                },
              },
            },
          },
        };
      }

      return {
        data: {
          task: {
            participants: {},
          },
        },
      };
    });

    const { getConfirmedQsdmTaskStakeInDenomination } = await import(
      './qsdmTaskStake'
    );
    const stake = await getConfirmedQsdmTaskStakeInDenomination('task-1');

    expect(stake).toBe(2000000000);
    expect(mockedAxiosGet).toHaveBeenCalledTimes(1);
    expect(mockedAxiosGet.mock.calls[0][0]).toBe(
      'https://api.qsdm.tech/api/v1/tasks/task-1/state'
    );
  });
});
