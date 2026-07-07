import { PublicKey } from 'vendor/qsdm-chain/web3';
import { buildQsdmTaskReadUrls, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import {
  isQsdmHiveInternalTaskId,
  isQsdmMinerSystemTaskId,
} from 'config/qsdmSystemTasks';
import { getQsdmMinerProtocolRewardInfo } from 'main/services/qsdmMinerProtocolRewards';
import { qsdmGetFirstJson } from 'main/services/qsdmHttpRead';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import {
  getCachedQsdmTaskStakeInDenomination,
  getConfirmedQsdmTaskState,
  getQsdmTaskParticipantBySender,
  qsdmCellToDenomination,
  readFiniteNumber,
} from 'main/services/qsdmTaskStake';
import { getTaskDataFromCache } from 'main/services/tasks-cache-utils';
import { ErrorType } from 'models';
import { GetTaskNodeInfoResponse } from 'models/api';
import { QsdmTasksListResponse } from 'models/api/qsdm';
import { throwDetailedError } from 'utils';

import qsdmHiveTasks from '../services/qsdmHiveTasks';

import { getKPLStakingAccountPubKey } from './getKPLStakingAccountPubKey';
import getStakingAccountPubKey from './getStakingAccountPubKey';

const NATIVE_TOKEN_KEY = 'CELL';

const isVisibleQsdmSidebarTask = (task: { task_id?: string }) =>
  !isQsdmHiveInternalTaskId(task.task_id);

const getQsdmNativeTasksForSidebar = async (fallbackTasks: any[]) => {
  try {
    const response = await qsdmGetFirstJson<QsdmTasksListResponse>(
      buildQsdmTaskReadUrls('/tasks'),
      { timeout: 4000 }
    );
    return response.tasks?.length ? response.tasks : fallbackTasks;
  } catch (error) {
    console.warn(
      'Native QSDM task list unavailable for sidebar totals; using started tasks.',
      error instanceof Error ? error.message : String(error)
    );
    return fallbackTasks;
  }
};

const getTaskNodeInfo = async (_: Event): Promise<GetTaskNodeInfoResponse> => {
  try {
    const totalStaked: GetTaskNodeInfoResponse['totalStaked'] = {};
    const pendingRewards: GetTaskNodeInfoResponse['pendingRewards'] = {};
    const allTimeRewards: GetTaskNodeInfoResponse['allTimeRewards'] = {};
    const tasks = await qsdmHiveTasks.getStartedTasks();

    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      const sender = getQsdmTaskActionSender();
      if (!sender) {
        return { totalStaked, pendingRewards, allTimeRewards };
      }

      totalStaked[NATIVE_TOKEN_KEY] = 0;
      pendingRewards[NATIVE_TOKEN_KEY] = 0;
      allTimeRewards[NATIVE_TOKEN_KEY] = 0;

      const tasksForSidebar = (
        await getQsdmNativeTasksForSidebar(tasks)
      ).filter(isVisibleQsdmSidebarTask);

      await Promise.all(
        tasksForSidebar.map(async (task) => {
          try {
            const state = await getConfirmedQsdmTaskState(task.task_id);
            const participant = getQsdmTaskParticipantBySender(
              state.task.participants,
              sender
            );
            const claimedRewards =
              readFiniteNumber(participant?.total_reward_claimed_amount) || 0;
            const protocolRewards = isQsdmMinerSystemTaskId(task.task_id)
              ? await getQsdmMinerProtocolRewardInfo()
              : null;

            totalStaked[NATIVE_TOKEN_KEY] += qsdmCellToDenomination(
              readFiniteNumber(participant?.stake) || 0
            );
            pendingRewards[NATIVE_TOKEN_KEY] += qsdmCellToDenomination(
              readFiniteNumber(participant?.pending_reward_amount) || 0
            );
            allTimeRewards[NATIVE_TOKEN_KEY] +=
              qsdmCellToDenomination(claimedRewards) +
              (protocolRewards?.earnedDenomination || 0);
          } catch (error) {
            console.warn(
              `Native QSDM sidebar state unavailable for ${task.task_id}; using cached task info.`,
              error instanceof Error ? error.message : String(error)
            );
            totalStaked[NATIVE_TOKEN_KEY] +=
              await getCachedQsdmTaskStakeInDenomination(task.task_id, sender);
            pendingRewards[NATIVE_TOKEN_KEY] +=
              readFiniteNumber(task.available_balances?.[sender]) || 0;
          }
        })
      );

      return {
        totalStaked,
        pendingRewards,
        allTimeRewards,
      };
    }

    await Promise.all(
      tasks.map(async (task) => {
        const stake = await getTaskDataFromCache(task.task_id, 'stakeList');
        const getCorrespondingStakingKey =
          task.task_type === 'KPL'
            ? getKPLStakingAccountPubKey
            : getStakingAccountPubKey;
        const stakingPubKeyToUse = await getCorrespondingStakingKey();
        const stakeValue = stake?.stake_list?.[stakingPubKeyToUse];
        const tokenKey = task.token_type
          ? new PublicKey(task.token_type)?.toBase58()
          : 'CELL';

        if (!totalStaked[tokenKey]) totalStaked[tokenKey] = 0;
        if (!pendingRewards[tokenKey]) pendingRewards[tokenKey] = 0;
        totalStaked[tokenKey] += stakeValue || 0;
        pendingRewards[tokenKey] +=
          task.available_balances?.[stakingPubKeyToUse] || 0;
      })
    );

    return {
      totalStaked,
      pendingRewards,
      allTimeRewards,
    };
  } catch (e: any) {
    if (e?.message !== 'Tasks not fetched yet') console.error(e);
    return throwDetailedError({
      detailed: e,
      type: ErrorType.GENERIC,
    });
  }
};

export default getTaskNodeInfo;
