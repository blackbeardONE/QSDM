import { Event } from 'electron';

import {
  PublicKey,
  SYSVAR_CLOCK_PUBKEY,
  Transaction,
  TransactionInstruction,
} from 'vendor/qsdm-chain/web3';
import {
  KPL_CONTRACT_ID,
  TASK_CONTRACT_ID,
  TASK_INSTRUCTION_LAYOUTS,
  TASK_INSTRUCTION_LAYOUTS_KPL,
  encodeData,
} from 'vendor/qsdm-chain/taskNode';
import { QSDM_CELL_DECIMALS, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import { getConfirmedQsdmTaskStakeInCell } from 'main/services/qsdmTaskStake';
import sdk from 'main/services/sdk';
import {
  getTaskDataFromCache,
  savePendingRewardsRecordToCache,
  saveStakeRecordToCache,
} from 'main/services/tasks-cache-utils';
import { sendAndDoubleConfirmTransaction } from 'main/util';
import { ErrorType, RawTaskData } from 'models';
import { WithdrawStakeParam } from 'models/api';
import { throwDetailedError, throwTransactionError } from 'utils/error';

import {
  getKplStakingAccountKeypair,
  getMainSystemAccountKeypair,
  getStakingAccountKeypair,
} from '../node/helpers';

const normalizeNativeStakeAmountToCell = (amount: unknown) => {
  const parsed = Number(amount);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0;
  }

  const denominationFactor = 10 ** QSDM_CELL_DECIMALS;
  return parsed >= denominationFactor ? parsed / denominationFactor : parsed;
};

const withdrawStake = async (
  _: Event,
  payload: WithdrawStakeParam
): Promise<string> => {
  const {
    taskAccountPubKey,
    shouldCheckCachedStake = true,
    taskType = 'CELL',
  } = payload;
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    const sender = getQsdmTaskActionSender();
    if (!sender) {
      throw new Error(
        'QSDM_TASK_ACTION_SENDER or QSDM_WALLET_ADDRESS is required to withdraw native QSDM task stake'
      );
    }

    let amountToUnstake = 0;
    let confirmedStakeRead = false;

    try {
      amountToUnstake = await getConfirmedQsdmTaskStakeInCell(
        taskAccountPubKey,
        sender
      );
      confirmedStakeRead = true;
    } catch (error) {
      console.error('Failed to read confirmed native QSDM stake', error);
    }

    if (!confirmedStakeRead && !amountToUnstake) {
      try {
        const taskState = await qsdmHiveTasks.getTaskState(taskAccountPubKey);
        amountToUnstake = normalizeNativeStakeAmountToCell(
          taskState?.stake_list?.[sender]
        );
      } catch (error) {
        console.error('Failed to read native QSDM task state', error);
      }
    }

    if (!confirmedStakeRead && !amountToUnstake) {
      const cacheData = await getTaskDataFromCache(
        taskAccountPubKey,
        'stakeList'
      );
      amountToUnstake = normalizeNativeStakeAmountToCell(
        cacheData?.stake_list?.[sender]
      );
    }

    if (shouldCheckCachedStake && !amountToUnstake) {
      throw new Error(
        `No stake found for native QSDM task ${taskAccountPubKey} when attempting to withdraw`
      );
    }
    if (!Number.isFinite(amountToUnstake) || amountToUnstake <= 0) {
      throw new Error('Withdraw amount must be greater than zero');
    }

    try {
      const response = await submitQsdmTaskActionIntent({
        taskId: taskAccountPubKey,
        action: 'withdraw',
        amount: amountToUnstake,
        payload: {
          source: 'qsdm-hive',
          reason: 'manual-withdraw',
        },
      });

      await saveStakeRecordToCache(
        taskAccountPubKey,
        sender,
        0
      );

      qsdmHiveTasks.updateStartedTasksData(taskAccountPubKey, (taskData) => ({
        ...taskData,
        stake_list: {
          ...taskData.stake_list,
          [sender]: 0,
        },
        total_stake_amount: Math.max(
          0,
          (taskData.total_stake_amount || 0) - amountToUnstake
        ),
      }));

      return response.action_id;
    } catch (e: any) {
      console.error('Native QSDM withdraw error', e);
      return throwTransactionError(e);
    }
  }

  const isKPLTask = taskType === 'KPL';
  const mainSystemAccount = await getMainSystemAccountKeypair();
  const getCorrespondingStakingAccKeypair = isKPLTask
    ? getKplStakingAccountKeypair
    : getStakingAccountKeypair;
  const stakingAccKeypair = await getCorrespondingStakingAccKeypair();

  const instructionLayout = isKPLTask
    ? TASK_INSTRUCTION_LAYOUTS_KPL.Withdraw
    : TASK_INSTRUCTION_LAYOUTS.Withdraw;
  const data = encodeData(instructionLayout, {});

  const programId = isKPLTask ? KPL_CONTRACT_ID : TASK_CONTRACT_ID;

  const instruction = new TransactionInstruction({
    keys: [
      {
        pubkey: new PublicKey(taskAccountPubKey),
        isSigner: false,
        isWritable: true,
      },
      { pubkey: stakingAccKeypair.publicKey, isSigner: true, isWritable: true },
      { pubkey: SYSVAR_CLOCK_PUBKEY, isSigner: false, isWritable: false },
    ],
    programId,
    data,
  });

  const cacheData = await getTaskDataFromCache(taskAccountPubKey, 'stakeList');

  const amountToUnstake =
    cacheData?.stake_list?.[stakingAccKeypair.publicKey.toBase58()];

  if (shouldCheckCachedStake && !amountToUnstake)
    throw new Error(
      `No stake found in cache for the task ${taskAccountPubKey} when attempting to unstake`
    );

  try {
    const res = await sendAndDoubleConfirmTransaction(
      sdk.k2Connection,
      new Transaction().add(instruction),
      [mainSystemAccount, stakingAccKeypair]
    );

    const getStartedTasksPubKeys =
      (await qsdmHiveTasks.getStartedTasksPubKeys()) || [];
    const taskIsStarted = getStartedTasksPubKeys.includes(taskAccountPubKey);

    if (taskIsStarted) {
      await saveStakeRecordToCache(
        taskAccountPubKey,
        stakingAccKeypair.publicKey.toBase58(),
        0
      );

      if (shouldCheckCachedStake && amountToUnstake) {
        await savePendingRewardsRecordToCache(
          taskAccountPubKey,
          stakingAccKeypair.publicKey.toBase58(),
          amountToUnstake,
          true
        );

        qsdmHiveTasks.updateStartedTasksData(taskAccountPubKey, (taskData) => {
          const currentBalance =
            taskData.available_balances[
              stakingAccKeypair.publicKey.toBase58()
            ] ?? 0;

          const newTaskData: Omit<RawTaskData, 'is_running'> = {
            ...taskData,
            available_balances: {
              ...taskData.available_balances,
              [stakingAccKeypair.publicKey.toBase58()]:
                currentBalance + amountToUnstake,
            },
          };
          return newTaskData;
        });
      }
    }
    return res.signature;
  } catch (e: any) {
    console.error('Unstake error', e);
    console.error('Unstake error message', e?.message);
    const unstakingIsNotAvailableYet =
      e?.message.toLowerCase().includes('submission cannot withdraw') ||
      (e?.logs &&
        e?.logs.some((log: any) =>
          log.toLowerCase().includes('submission cannot withdraw')
        ));
    if (unstakingIsNotAvailableYet) {
      return throwDetailedError({
        detailed: e,
        type: ErrorType.UNSTAKE_UNAVAILABLE,
      });
    } else {
      return throwTransactionError(e);
    }
  }
};

export default withdrawStake;
