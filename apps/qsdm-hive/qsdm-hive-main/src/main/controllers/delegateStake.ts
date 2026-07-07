import { Event } from 'electron';

import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import {
  getCachedQsdmTaskStakeInDenomination,
  getConfirmedQsdmTaskStakeInDenomination,
  qsdmCellToDenomination,
} from 'main/services/qsdmTaskStake';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import { saveStakeRecordToCache } from 'main/services/tasks-cache-utils';
import { sleep } from 'main/util';
import { DelegateStakeParam, DelegateStakeResponse } from 'models';
import { throwTransactionError } from 'utils/error';

import { delegateStakeCell } from './delegateStakeCell';
import { delegateStakeKPL } from './delegateStakeKPL';

const QSDM_STAKE_CONFIRMATION_ATTEMPTS = 10;
const QSDM_STAKE_CONFIRMATION_DELAY_MS = 1500;
const QSDM_DENOMINATION_THRESHOLD = 1_000_000;

const qsdmStakeInputToCell = (amount: number) =>
  amount >= QSDM_DENOMINATION_THRESHOLD ? amount / 1_000_000_000 : amount;

const waitForConfirmedQsdmStake = async (
  taskId: string,
  minStakeInDenomination: number,
  sender: string
) => {
  let lastConfirmedStake = 0;
  for (let attempt = 0; attempt < QSDM_STAKE_CONFIRMATION_ATTEMPTS; attempt++) {
    try {
      lastConfirmedStake = await getConfirmedQsdmTaskStakeInDenomination(
        taskId,
        sender
      );
      if (lastConfirmedStake >= minStakeInDenomination) {
        return lastConfirmedStake;
      }
    } catch (error) {
      console.error('Error while confirming native QSDM stake', error);
    }
    await sleep(QSDM_STAKE_CONFIRMATION_DELAY_MS);
  }

  throw new Error(
    `Stake was submitted but QSDM core has not confirmed it yet. Last confirmed stake: ${lastConfirmedStake}`
  );
};

const delegateStakeQSDM = async (
  _: Event,
  payload: DelegateStakeParam
): Promise<DelegateStakeResponse> => {
  const {
    taskAccountPubKey,
    stakeAmount,
    isNetworkingTask,
    useStakingWallet,
    skipIfItIsAlreadyStaked,
  } = payload;

  const sender = getQsdmTaskActionSender();
  if (!sender) {
    throw new Error(
      'QSDM_TASK_ACTION_SENDER or QSDM_WALLET_ADDRESS is required to stake on native QSDM tasks'
    );
  }
  if (!Number.isFinite(stakeAmount) || stakeAmount <= 0) {
    throw new Error('Stake amount must be greater than zero');
  }
  const stakeAmountCell = qsdmStakeInputToCell(stakeAmount);

  let confirmedStakeBefore = 0;
  try {
    confirmedStakeBefore = await getConfirmedQsdmTaskStakeInDenomination(
      taskAccountPubKey,
      sender
    );
  } catch (error) {
    console.warn(
      'Current native QSDM stake is temporarily unavailable; checking the local cache.',
      error instanceof Error ? error.message : String(error)
    );
    confirmedStakeBefore = await getCachedQsdmTaskStakeInDenomination(
      taskAccountPubKey,
      sender
    );
  }

  if (skipIfItIsAlreadyStaked && confirmedStakeBefore > 0) {
    await saveStakeRecordToCache(
      taskAccountPubKey,
      sender,
      confirmedStakeBefore
    );
    return String(confirmedStakeBefore);
  }

  try {
    const response = await submitQsdmTaskActionIntent({
      taskId: taskAccountPubKey,
      action: 'stake',
      amount: stakeAmountCell,
      payload: {
        source: 'qsdm-hive',
        isNetworkingTask: Boolean(isNetworkingTask),
        useStakingWallet: Boolean(useStakingWallet),
      },
    });
    const stakeAmountInDenomination = qsdmCellToDenomination(stakeAmountCell);
    const confirmedStake = await waitForConfirmedQsdmStake(
      taskAccountPubKey,
      confirmedStakeBefore + stakeAmountInDenomination,
      sender
    );

    await saveStakeRecordToCache(taskAccountPubKey, sender, confirmedStake);

    qsdmHiveTasks.updateStartedTasksData(taskAccountPubKey, (taskData) => {
      const currentStake = taskData.stake_list?.[sender] || 0;
      const currentTotalStake = taskData.total_stake_amount || 0;
      return {
        ...taskData,
        stake_list: {
          ...taskData.stake_list,
          [sender]: confirmedStake,
        },
        total_stake_amount:
          Math.max(0, currentTotalStake - currentStake + confirmedStake),
      };
    });

    return response.action_id;
  } catch (error: any) {
    console.error(
      'Native QSDM stake error',
      error instanceof Error ? error.message : String(error)
    );
    return throwTransactionError(error);
  }
};

const delegateStake = async (
  _: Event,
  payload: DelegateStakeParam
): Promise<DelegateStakeResponse> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    return delegateStakeQSDM(_, payload);
  }

  const handler =
    payload.taskType === 'KPL' ? delegateStakeKPL : delegateStakeCell;

  return handler(_, payload);
};

export default delegateStake;
