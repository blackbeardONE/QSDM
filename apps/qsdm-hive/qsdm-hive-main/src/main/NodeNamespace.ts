import fs from 'fs';

import { Keypair } from 'vendor/qsdm-chain/web3';
import {
  IDatabase,
  TaskNodeBase,
  TaskNodeConfig,
} from 'vendor/qsdm-chain/taskNode';

import { SystemDbKeys } from '../config/systemDbKeys';

import { getAppDataPath } from './node/helpers/getAppDataPath';

export class NodeNamespace extends TaskNodeBase {
  appDataPath: string;

  private readonly db?: IDatabase;

  constructor(config: TaskNodeConfig) {
    super(config);

    this.appDataPath = getAppDataPath();
    this.db = config.db as IDatabase | undefined;
  }

  async storeGet(key: string): Promise<any> {
    if (this.db?.get) {
      return this.db.get(key);
    }
    return super.storeGet(key);
  }

  async storeGetRaw(key: string): Promise<any> {
    return this.storeGet(key);
  }

  async storeSet(key: string, value: any): Promise<any> {
    if (this.db?.put) {
      return this.db.put(key, value);
    }
    return super.storeSet(key, value);
  }

  async getMainSystemAccountPubKey(db?: IDatabase): Promise<Keypair> {
    if (!db) throw new Error('No database provided');

    const activeAccount = await db.get(SystemDbKeys.ActiveAccount);

    if (!activeAccount) {
      throw new Error('No active account found');
    }

    const mainSystemAccountRetrieved = Keypair.fromSecretKey(
      Uint8Array.from(
        JSON.parse(
          fs.readFileSync(
            `${this.appDataPath}/wallets/${activeAccount}_mainSystemWallet.json`,
            'utf-8'
          )
        ) as Uint8Array
      )
    );

    return mainSystemAccountRetrieved;
  }

  async getSubmitterAccount(taskType?: string): Promise<Keypair | null> {
    let submitterAccount: Keypair | null;
    if (!taskType) {
      // eslint-disable-next-line no-param-reassign
      taskType = this.taskType;
    }
    try {
      const activeAccount = await this.storeGetRaw(SystemDbKeys.ActiveAccount);
      const STAKING_WALLET_PATH =
        taskType === 'CELL'
          ? `${getAppDataPath()}/namespace/${activeAccount}_stakingWallet.json`
          : `${getAppDataPath()}/namespace/${activeAccount}_kplStakingWallet.json`;
      if (!fs.existsSync(STAKING_WALLET_PATH)) return null;
      submitterAccount = Keypair.fromSecretKey(
        Uint8Array.from(
          JSON.parse(
            fs.readFileSync(STAKING_WALLET_PATH, 'utf-8')
          ) as Uint8Array
        )
      );
    } catch (e) {
      console.error(
        'Staking wallet not found. Please create a staking wallet and place it in the namespace folder'
      );
      submitterAccount = null;
    }
    return submitterAccount;
  }

  async getDistributionAccount(): Promise<Keypair | null> {
    let distributionAccount: Keypair | null;

    try {
      const activeAccount = await this.storeGetRaw(SystemDbKeys.ActiveAccount);
      const STAKING_WALLET_PATH =
        this.taskType === 'CELL'
          ? `${getAppDataPath()}/namespace/${activeAccount}_stakingWallet.json`
          : `${getAppDataPath()}/namespace/${activeAccount}_kplStakingWallet.json`;
      if (!fs.existsSync(STAKING_WALLET_PATH)) return null;
      distributionAccount = Keypair.fromSecretKey(
        Uint8Array.from(
          JSON.parse(
            fs.readFileSync(STAKING_WALLET_PATH, 'utf-8')
          ) as Uint8Array
        )
      );
    } catch (e) {
      console.error(
        'Distribution wallet not found. Please create a staking wallet and place it in the namespace folder'
      );
      distributionAccount = null;
    }
    return distributionAccount;
  }
}
