import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { getMyTaskStakeInfo } from 'vendor/qsdm-chain/taskNode';
import { isNumber } from 'lodash';
import {
  getKplStakingAccountKeypair,
  getStakingAccountKeypair,
} from 'main/node/helpers';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import sdk from 'main/services/sdk';
import {
  getTaskDataFromCache,
  saveStakeRecordToCache,
} from 'main/services/tasks-cache-utils';
import {
  getCachedQsdmTaskStakeInDenomination,
  getConfirmedQsdmTaskStakeInDenomination,
} from 'main/services/qsdmTaskStake';

export type GetMyTaskStakeParams = {
  taskAccountPubKey: string;
  revalidate?: boolean;
  shouldCache?: boolean;
  taskType: string;
};

export async function getMyTaskStake(
  _: Event,
  {
    taskAccountPubKey,
    revalidate,
    shouldCache = true,
    taskType,
  }: GetMyTaskStakeParams
): Promise<number> {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    const sender = getQsdmTaskActionSender();
    if (!sender) {
      return 0;
    }

    try {
      const taskStakeInDenomination =
        await getConfirmedQsdmTaskStakeInDenomination(
          taskAccountPubKey,
          sender
        );

      if (shouldCache) {
        await saveStakeRecordToCache(
          taskAccountPubKey,
          sender,
          taskStakeInDenomination
        );
      }

      return taskStakeInDenomination;
    } catch (error) {
      console.warn(
        'Native task stake is temporarily unavailable; using the local cache.',
        error instanceof Error ? error.message : String(error)
      );
      return getCachedQsdmTaskStakeInDenomination(taskAccountPubKey, sender);
    }
  }

  try {
    const functionToGetStakingAccountKeypair =
      taskType === 'KPL'
        ? getKplStakingAccountKeypair
        : getStakingAccountKeypair;
    const stakingAccKeypair = await functionToGetStakingAccountKeypair();
    const stakingPubkey = stakingAccKeypair.publicKey.toBase58();

    const cachedStakeList = await getTaskDataFromCache(
      taskAccountPubKey,
      'stakeList'
    );
    const cachedStakeInfo = cachedStakeList?.stake_list?.[stakingPubkey];

    if (isNumber(cachedStakeInfo) && !revalidate) {
      return cachedStakeInfo;
    }
    let taskStakeInfo = 0;
    try {
      taskStakeInfo = await getMyTaskStakeInfo(
        sdk.k2Connection,
        taskAccountPubKey,
        stakingPubkey,
        taskType
      );
    } catch (error) {
      const thereIsNoStakeOnTask =
        error instanceof Error &&
        error.message.includes('No stake available on this task');
      if (thereIsNoStakeOnTask && shouldCache) {
        await saveStakeRecordToCache(taskAccountPubKey, stakingPubkey, 0);
      } else {
        throw error;
      }
    }
    if (shouldCache) {
      await saveStakeRecordToCache(
        taskAccountPubKey,
        stakingPubkey,
        taskStakeInfo
      );
    }

    return taskStakeInfo;
  } catch (error: any) {
    if (!error?.message?.toLowerCase().includes('no stake available')) {
      console.error('Error while fetching task stake info', error);
    }

    return 0;
  }
}
