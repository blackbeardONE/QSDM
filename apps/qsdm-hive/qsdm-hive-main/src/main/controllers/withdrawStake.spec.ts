/**
 * @jest-environment node
 */

import * as qsdmWeb3 from 'vendor/qsdm-chain/web3';
// import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import {
  TASK_CONTRACT_ID,
  TASK_INSTRUCTION_LAYOUTS,
  encodeData,
} from 'vendor/qsdm-chain/taskNode';
import { getTaskDataFromCache } from 'main/services/tasks-cache-utils';

import {
  getMainSystemAccountKeypair,
  getStakingAccountKeypair,
} from '../node/helpers';

import withdrawStake from './withdrawStake';

jest.mock('config/qsdm', () => ({
  ...jest.requireActual('config/qsdm'),
  QSDM_TASK_RUNTIME_MODE: 'legacy',
}));

jest.spyOn(qsdmWeb3.Transaction.prototype, 'add');
jest.spyOn(qsdmWeb3, 'sendAndConfirmTransaction');
jest.mock('../node/helpers', () => ({
  __esModule: true,
  getMainSystemAccountKeypair: jest.fn(),
  getStakingAccountKeypair: jest.fn(),
}));
jest.mock('../node/helpers/getAppDataPath', () => ({
  getAppDataPath: jest.fn().mockReturnValue('/path/to/appdata'),
}));
jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    fetchStartedTaskData: jest.fn(),
  },
}));

jest.mock('main/services/qsdmHiveTasks', () => ({
  ...jest.requireActual('main/services/qsdmHiveTasks'),
  updateStartedTasksData: jest.fn(),
  getStartedTasksPubKeys: jest.fn(() => [
    '7x8tP5ipyqPfrRSXoxgGz6EzfTe3S84J3WUvJwbTwgnY',
  ]),
}));
jest.mock('main/services/tasks-cache-utils', () => ({
  __esModule: true,
  saveStakeRecordToCache: jest.fn(),
  getTaskDataFromCache: jest.fn(),
  savePendingRewardsRecordToCache: jest.fn(),
}));

describe('withdrawStake', () => {
  it('sends transaction with correct instruction', async () => {
    const exampleAddress = '7x8tP5ipyqPfrRSXoxgGz6EzfTe3S84J3WUvJwbTwgnY';
    const payload: any = {
      taskAccountPubKey: new qsdmWeb3.PublicKey(exampleAddress),
    };

    const mainSystemAccount = qsdmWeb3.Keypair.generate();
    const stakingAccKeypair = qsdmWeb3.Keypair.generate();
    (getTaskDataFromCache as jest.Mock).mockReturnValue({
      stake_list: { [stakingAccKeypair.publicKey.toBase58()]: 43 },
    });
    (getMainSystemAccountKeypair as jest.Mock).mockReturnValue(
      mainSystemAccount
    );
    (getStakingAccountKeypair as jest.Mock).mockReturnValue(stakingAccKeypair);
    (qsdmWeb3.sendAndConfirmTransaction as jest.Mock).mockReturnValue(
      'example_transaction_hash'
    );

    const data = encodeData(TASK_INSTRUCTION_LAYOUTS.Withdraw, {});
    const expectedInstruction = new qsdmWeb3.TransactionInstruction({
      keys: [
        {
          pubkey: new qsdmWeb3.PublicKey(payload.taskAccountPubKey),
          isSigner: false,
          isWritable: true,
        },
        {
          pubkey: stakingAccKeypair.publicKey,
          isSigner: true,
          isWritable: true,
        },
        {
          pubkey: qsdmWeb3.SYSVAR_CLOCK_PUBKEY,
          isSigner: false,
          isWritable: false,
        },
      ],
      programId: TASK_CONTRACT_ID,
      data,
    });

    const result = await withdrawStake({} as Event, payload);

    expect(qsdmWeb3.Transaction.prototype.add).toHaveBeenCalledWith(
      expectedInstruction
    );
    expect(qsdmWeb3.sendAndConfirmTransaction).toHaveBeenCalled();
    expect(result).toBe('example_transaction_hash');
  });
});
