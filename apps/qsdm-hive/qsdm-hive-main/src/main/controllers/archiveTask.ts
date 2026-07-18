import { ArchiveTaskParams } from 'models';
import { PublicKey } from 'vendor/qsdm-chain/web3';

import qsdmHiveTasks from '../services/qsdmHiveTasks';

import claimReward from './claimReward';
import { claimRewardKPL } from './claimRewardKPL';

import type { Event } from 'electron';

export const archiveTask = async (_: Event, payload: ArchiveTaskParams) => {
  if (!payload.skipClaimRewards) {
    try {
      const taskState = (await qsdmHiveTasks.getStartedTasks())?.find(
        (task) => task.task_id === payload.taskPubKey
      );
      const handlerToClaimRewards =
        taskState?.task_type === 'KPL' ? claimRewardKPL : claimReward;

      await handlerToClaimRewards({} as Event, {
        taskAccountPubKey: payload.taskPubKey,
        tokenType: taskState?.token_type
          ? new PublicKey(taskState?.token_type as any).toBase58()
          : '',
      });
    } catch (error: any) {
      const taskDidntHaveRewardsToClaim = error?.message?.includes(
        "The provided claimer account doesn't have any balance on task state"
      );
      if (!taskDidntHaveRewardsToClaim) {
        throw error;
      }
    }
  }

  qsdmHiveTasks.removeTaskFromStartedTasks(payload.taskPubKey);
};
