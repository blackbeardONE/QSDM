import { TransactionSignature } from 'vendor/qsdm-chain/web3';

export interface ClaimRewardParam {
  taskAccountPubKey: string;
}

export type ClaimRewardResponse = TransactionSignature;
