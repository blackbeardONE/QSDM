import { CellBaseUnits } from 'models/api/storeAllTimeRewards';

import { getAllAccountsResponse } from './getAllAccounts';

export type OwnerAccount = Omit<
  getAllAccountsResponse[0],
  'isDefault' | 'mainPublicKeyBalance' | 'stakingPublicKeyBalance'
>;

export interface TaskToMigrate extends OwnerAccount {
  publicKey: string;
  stake: CellBaseUnits;
}

export type TasksToMigrate = Record<string, TaskToMigrate>;
