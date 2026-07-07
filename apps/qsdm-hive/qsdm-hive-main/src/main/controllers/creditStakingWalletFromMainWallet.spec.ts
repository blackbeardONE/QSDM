/**
 * @jest-environment node
 */

import * as qsdmWeb3 from 'vendor/qsdm-chain/web3';
import { TASK_CONTRACT_ID } from 'vendor/qsdm-chain/taskNode';
import sdk from 'main/services/sdk';

import {
  getMainSystemAccountKeypair,
  getStakingAccountKeypair,
} from '../node/helpers';

import { creditStakingWalletFromMainWallet } from './creditStakingWalletFromMainWallet';

jest.spyOn(qsdmWeb3, 'sendAndConfirmTransaction');
jest.spyOn(qsdmWeb3.SystemProgram, 'createAccount');
jest.spyOn(qsdmWeb3.SystemProgram, 'transfer');

jest.mock('../node/helpers', () => ({
  __esModule: true,
  getMainSystemAccountKeypair: jest.fn(),
  getStakingAccountKeypair: jest.fn(),
}));

jest.mock('main/services/sdk', () => ({
  k2Connection: {
    getAccountInfo: jest.fn(),
    getMinimumBalanceForRentExemption: jest.fn().mockReturnValue(3000),
  },
}));

describe('delegateStake', () => {
  let mainSystemAccount: any;
  let stakingAccKeypair: any;

  beforeAll(() => {
    (qsdmWeb3.sendAndConfirmTransaction as jest.Mock).mockImplementation(() => {
      return 'example_transaction_hash';
    });
  });

  beforeEach(() => {
    jest.clearAllMocks();

    mainSystemAccount = qsdmWeb3.Keypair.generate();
    stakingAccKeypair = qsdmWeb3.Keypair.generate();

    (getMainSystemAccountKeypair as jest.Mock).mockReturnValue(
      mainSystemAccount
    );
    (getStakingAccountKeypair as jest.Mock).mockReturnValue(stakingAccKeypair);
  });

  describe('creditStakingWalletFromMainWallet', () => {
    beforeEach(() => {
      (sdk.k2Connection.getAccountInfo as jest.Mock).mockReturnValue({
        owner: {
          toBase58: jest.fn().mockReturnValue(TASK_CONTRACT_ID.toBase58()),
        },
      });
    });

    it('transfers given base units amount from main account to staking account', async () => {
      const amountInBaseUnits = 5 * 1e9;
      const result = await creditStakingWalletFromMainWallet({} as Event, {
        amountInBaseUnits,
      });

      expect(result).toEqual('example_transaction_hash');

      expect(qsdmWeb3.SystemProgram.transfer).toHaveBeenCalledWith({
        fromPubkey: mainSystemAccount.publicKey,
        toPubkey: stakingAccKeypair.publicKey,
        lamports: amountInBaseUnits,
      });

      expect(qsdmWeb3.SystemProgram.transfer).toHaveBeenCalledTimes(1);
    });
  });
});
