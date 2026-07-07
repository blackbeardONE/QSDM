import axios from 'axios';
import { Event } from 'electron';

import { buildQsdmCoreApiUrl, QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import {
  getQsdmLocalSignedLoopEnabled,
  getQsdmTaskActionSignerStatus,
} from 'main/services/qsdmTaskActionSigner';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import {
  QsdmMiningAccountResponse,
  QsdmSignedCellLoopRequest,
  QsdmSignedCellLoopResponse,
  QsdmTaskAction,
  QsdmTaskStateResponse,
} from 'models/api/qsdm';

const DEFAULT_TASK_ID = 'qsdm-hive-local-task';
const DEFAULT_FUND_AMOUNT = 10;
const DEFAULT_STAKE_AMOUNT = 2.5;
const DEFAULT_REWARD_AMOUNT = 1.25;

const buildUrl = (path: string, params: Record<string, string>) => {
  const url = new URL(buildQsdmCoreApiUrl(path));
  Object.entries(params).forEach(([key, value]) => {
    url.searchParams.set(key, value);
  });
  return url.toString();
};

const numericOrDefault = (value: unknown, fallback: number) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
};

const getMiningAccount = async (sender: string) => {
  const response = await axios.get<QsdmMiningAccountResponse>(
    buildUrl('/mining/account', { address: sender }),
    { timeout: 10000 }
  );
  return response.data;
};

const getChainTip = async () => {
  const response = await axios.get<Record<string, unknown>>(
    buildQsdmCoreApiUrl('/status'),
    { timeout: 5000 }
  );
  return numericOrDefault(response.data.chain_tip, Date.now());
};

const getTaskState = async (taskId: string) => {
  const response = await axios.get<QsdmTaskStateResponse>(
    buildQsdmCoreApiUrl(`/tasks/${encodeURIComponent(taskId)}/state`),
    { timeout: 5000 }
  );
  return response.data.task;
};

export const runQsdmSignedCellLoop = async (
  _: Event,
  payload?: QsdmSignedCellLoopRequest
): Promise<QsdmSignedCellLoopResponse> => {
  if (!getQsdmLocalSignedLoopEnabled()) {
    throw new Error(
      'QSDM_ENABLE_LOCAL_SIGNED_LOOP=1 or the local QSDM Hive signer is required to run the signed CELL proof loop'
    );
  }

  const signer = getQsdmTaskActionSignerStatus();
  if (!signer.ready || !signer.sender) {
    throw new Error(signer.reason || 'QSDM task action signer is not ready');
  }

  const taskId = payload?.taskId?.trim() || DEFAULT_TASK_ID;
  const fundAmount = numericOrDefault(payload?.fundAmount, DEFAULT_FUND_AMOUNT);
  const stakeAmount = numericOrDefault(
    payload?.stakeAmount,
    DEFAULT_STAKE_AMOUNT
  );
  const rewardAmount = numericOrDefault(
    payload?.rewardAmount,
    DEFAULT_REWARD_AMOUNT
  );
  const actions: QsdmSignedCellLoopResponse['actions'] = [];

  const submitAndWait = async (
    action: QsdmTaskAction,
    amount?: number,
    actionPayload?: Record<string, unknown>
  ) => {
    const before = await getMiningAccount(signer.sender as string);
    const response = await submitQsdmTaskActionIntent({
      taskId,
      action,
      amount,
      payload: actionPayload,
    });

    if (response.status !== 'accepted' && response.status !== 'duplicate') {
      throw new Error(
        `QSDM task action ${action} returned status ${response.status}`
      );
    }

    const after = await getMiningAccount(signer.sender as string);

    actions.push({
      action,
      action_id: response.action_id,
      status: response.status,
      mempool_status: response.mempool_status,
      mempool_error: response.mempool_error,
      nonce_before: before.nonce,
      nonce_after: after.nonce,
      balance_after: after.balance,
    });
  };

  if (!payload?.skipFund) {
    await submitAndWait('fund', fundAmount, {
      source: 'qsdm-hive-loop',
      reason: 'seed reward pool for signed loop proof',
    });
  }
  await submitAndWait('start', undefined, {
    source: 'qsdm-hive-loop',
    mode: 'proof',
  });
  await submitAndWait('stake', stakeAmount, { source: 'qsdm-hive-loop' });

  const slot = await getChainTip();
  await submitAndWait('submit', undefined, {
    source: 'qsdm-hive-loop',
    round: 1,
    slot,
    submission_value: `qsdm-hive-loop-proof-${slot}`,
    reward_amount: rewardAmount,
  });
  await submitAndWait('claim', undefined, {
    source: 'qsdm-hive-loop',
    round: 0,
  });

  const finalAccount = await getMiningAccount(signer.sender);
  let taskState: QsdmSignedCellLoopResponse['taskState'];
  try {
    taskState = await getTaskState(taskId);
  } catch {
    taskState = undefined;
  }

  return {
    apiUrl: QSDM_BRIDGE_CONFIG.apiUrl,
    taskId,
    sender: signer.sender,
    actions,
    finalBalance: finalAccount.balance,
    finalNonce: finalAccount.nonce,
    taskState,
  };
};
