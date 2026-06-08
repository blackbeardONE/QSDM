import { TransactionSignature } from 'vendor/qsdm-chain/web3';
import { TaskType } from 'models/task';

export interface DelegateStakeParam {
  taskAccountPubKey: string;
  stakePotAccount: string;
  stakeAmount: number;
  isNetworkingTask?: boolean;
  useStakingWallet?: boolean;
  skipIfItIsAlreadyStaked?: boolean;
  taskType?: TaskType;
  mintAddress?: string;
}

export type DelegateStakeResponse = TransactionSignature;
