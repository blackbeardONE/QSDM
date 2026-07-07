import { Event } from 'electron';

// eslint-disable-next-line @cspell/spellchecker
import { PublicKey } from 'vendor/qsdm-chain/web3';
import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import { savePendingRewardsRecordToCache } from 'main/services/tasks-cache-utils';
import { ClaimRewardParam, ClaimRewardResponse, RawTaskData } from 'models';
import { throwTransactionError } from 'utils/error';

import {
  getMainSystemAccountKeypair,
  getStakingAccountKeypair,
} from '../node/helpers';
import { namespaceInstance } from '../node/helpers/Namespace';

import { getTaskInfo } from './getTaskInfo';

const claimReward = async (
  _: Event,
  payload: ClaimRewardParam & { stakePotAccount?: string }
): Promise<ClaimRewardResponse> => {
  const { taskAccountPubKey, stakePotAccount } = payload;
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    const sender = getQsdmTaskActionSender();
    try {
      console.log(`Claiming native QSDM reward for task: ${taskAccountPubKey}`);
      const response = await submitQsdmTaskActionIntent({
        taskId: taskAccountPubKey,
        action: 'claim',
        payload: { reason: 'manual-claim' },
      });

      const getStartedTasksPubKeys =
        (await qsdmHiveTasks.getStartedTasksPubKeys()) || [];
      const taskIsStarted = getStartedTasksPubKeys.includes(taskAccountPubKey);

      if (taskIsStarted && sender) {
        await savePendingRewardsRecordToCache(
          taskAccountPubKey,
          sender,
          0
        );

        qsdmHiveTasks.updateStartedTasksData(taskAccountPubKey, (taskData) => {
          const newTaskData: Omit<RawTaskData, 'is_running'> = {
            ...taskData,
            available_balances: {
            ...taskData.available_balances,
              [sender]: 0,
            },
          };
          return newTaskData;
        });
      }

      return response.action_id;
    } catch (err: any) {
      console.error(
        `Failed to claim the native QSDM reward for Task: ${taskAccountPubKey}`
      );
      console.error(err);
      return throwTransactionError(err);
    }
  }

  const taskStateInfoPublicKey = new PublicKey(taskAccountPubKey);

  const stakingAccKeypair = await getStakingAccountKeypair();
  const mainSystemAccountKeyPair = await getMainSystemAccountKeypair();

  const taskStakePotAccount =
    stakePotAccount ||
    (await getTaskInfo({} as Event, { taskAccountPubKey })).stakePotAccount;

  const statePotPubKey = new PublicKey(taskStakePotAccount);

  try {
    console.log(`Claiming reward for task: ${taskAccountPubKey}`);
    const response = await namespaceInstance.claimReward(
      statePotPubKey,
      mainSystemAccountKeyPair.publicKey,
      stakingAccKeypair,
      taskStateInfoPublicKey,
      'CELL'
    );

    console.log(`Claimed reward for task: ${taskAccountPubKey}`);

    const getStartedTasksPubKeys =
      (await qsdmHiveTasks.getStartedTasksPubKeys()) || [];
    const taskIsStarted = getStartedTasksPubKeys.includes(taskAccountPubKey);

    if (taskIsStarted) {
      await savePendingRewardsRecordToCache(
        taskAccountPubKey,
        stakingAccKeypair.publicKey.toBase58(),
        0
      );

      qsdmHiveTasks.updateStartedTasksData(taskAccountPubKey, (taskData) => {
        const newTaskData: Omit<RawTaskData, 'is_running'> = {
          ...taskData,
          available_balances: {
            ...taskData.available_balances,
            [stakingAccKeypair.publicKey.toBase58()]: 0,
          },
        };
        return newTaskData;
      });
    }

    return response;
  } catch (err: any) {
    console.error(`Failed to claim the reward for Task: ${taskAccountPubKey}`);
    console.error(err);
    return throwTransactionError(err);
  }
};

export default claimReward;
