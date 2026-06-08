import axios from 'axios';

import {
  buildQsdmApiUrl,
  buildQsdmCoreApiUrl,
  QSDM_CELL_DECIMALS,
} from 'config/qsdm';
import {
  QsdmTaskResponse,
  QsdmTaskStateParticipant,
  QsdmTaskStateResponse,
} from 'models/api/qsdm';

import { getQsdmTaskActionSender } from './qsdmTaskActionSigner';
import { getTaskDataFromCache } from './tasks-cache-utils';

export const qsdmCellToDenomination = (amount: number) =>
  Math.round(amount * 10 ** QSDM_CELL_DECIMALS);

const unique = (values: string[]) => Array.from(new Set(values));

export const readFiniteNumber = (value: unknown) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
};

export interface QsdmTaskStakeParticipantOwnership {
  sender: string;
  stakeCell: number;
  running: boolean;
}

export interface QsdmTaskStakeOwnership {
  sender: string;
  currentStakeCell: number;
  currentStakeDenomination: number;
  foreignStakeCell: number;
  foreignParticipants: QsdmTaskStakeParticipantOwnership[];
  runningForCurrentSender: boolean;
  runningForOtherSender: boolean;
}

const qsdmTaskUrls = (path: string) =>
  unique([buildQsdmCoreApiUrl(path), buildQsdmApiUrl(path)]);

const readStakeFromTaskState = async (sender: string, url: string) => {
  const response = await axios.get<QsdmTaskStateResponse>(url, {
    timeout: 10000,
  });
  if (!response.data.task) {
    return undefined;
  }

  return readFiniteNumber(response.data.task.participants?.[sender]?.stake) || 0;
};

export const getConfirmedQsdmTaskState = async (taskId: string) => {
  const encodedTaskId = encodeURIComponent(taskId);
  const errors: string[] = [];

  for (const stateUrl of qsdmTaskUrls(`/tasks/${encodedTaskId}/state`)) {
    try {
      const response = await axios.get<QsdmTaskStateResponse>(stateUrl, {
        timeout: 10000,
      });
      if (response.data.task) {
        return response.data;
      }
    } catch (error: any) {
      errors.push(`${stateUrl}: ${error?.message || error}`);
    }
  }

  throw new Error(
    `Could not read native QSDM task state for ${taskId}: ${errors.join('; ')}`
  );
};

const readStakeFromTaskRegistry = async (sender: string, url: string) => {
  const response = await axios.get<QsdmTaskResponse>(url, { timeout: 10000 });
  if (!response.data.task) {
    return undefined;
  }

  return readFiniteNumber(response.data.task.stake_list?.[sender]) || 0;
};

export const getCachedQsdmTaskStakeInDenomination = async (
  taskId: string,
  sender = getQsdmTaskActionSender()
) => {
  if (!sender) {
    return 0;
  }

  const cachedStakeList = await getTaskDataFromCache(taskId, 'stakeList');
  const cachedStake = cachedStakeList?.stake_list?.[sender];

  return readFiniteNumber(cachedStake) || 0;
};

export const getConfirmedQsdmTaskStakeInCell = async (
  taskId: string,
  sender = getQsdmTaskActionSender()
) => {
  if (!sender) {
    return 0;
  }

  const encodedTaskId = encodeURIComponent(taskId);
  const errors: string[] = [];

  for (const stateUrl of qsdmTaskUrls(`/tasks/${encodedTaskId}/state`)) {
    try {
      const stake = await readStakeFromTaskState(sender, stateUrl);
      if (stake !== undefined) {
        return stake;
      }
    } catch (error: any) {
      errors.push(`${stateUrl}: ${error?.message || error}`);
    }
  }

  for (const taskUrl of qsdmTaskUrls(`/tasks/${encodedTaskId}`)) {
    try {
      const stake = await readStakeFromTaskRegistry(sender, taskUrl);
      if (stake !== undefined) {
        return stake;
      }
    } catch (error: any) {
      errors.push(`${taskUrl}: ${error?.message || error}`);
    }
  }

  throw new Error(
    `Could not read native QSDM task stake for ${taskId}: ${errors.join('; ')}`
  );
};

export const getConfirmedQsdmTaskStakeInDenomination = async (
  taskId: string,
  sender = getQsdmTaskActionSender()
) =>
  qsdmCellToDenomination(
    await getConfirmedQsdmTaskStakeInCell(taskId, sender)
  );

const getParticipantSender = (
  participantKey: string,
  participant: QsdmTaskStateParticipant
) => participant.sender || participantKey;

export const getQsdmTaskStakeOwnership = async (
  taskId: string,
  sender = getQsdmTaskActionSender()
): Promise<QsdmTaskStakeOwnership> => {
  const normalizedSender = (sender || '').toLowerCase();
  const state = await getConfirmedQsdmTaskState(taskId);
  const participants = Object.entries(state.task?.participants || {});

  let currentStakeCell = 0;
  let runningForCurrentSender = false;
  const foreignParticipants: QsdmTaskStakeParticipantOwnership[] = [];

  for (const [participantKey, participant] of participants) {
    const participantSender = getParticipantSender(participantKey, participant);
    const normalizedParticipantSender = participantSender.toLowerCase();
    const stakeCell = readFiniteNumber(participant.stake) || 0;
    const running = Boolean(participant.running);

    if (normalizedSender && normalizedParticipantSender === normalizedSender) {
      currentStakeCell = stakeCell;
      runningForCurrentSender = running;
      continue;
    }

    if (stakeCell > 0 || running) {
      foreignParticipants.push({
        sender: participantSender,
        stakeCell,
        running,
      });
    }
  }

  return {
    sender: sender || '',
    currentStakeCell,
    currentStakeDenomination: qsdmCellToDenomination(currentStakeCell),
    foreignStakeCell: foreignParticipants.reduce(
      (total, participant) => total + participant.stakeCell,
      0
    ),
    foreignParticipants,
    runningForCurrentSender,
    runningForOtherSender: foreignParticipants.some(
      (participant) => participant.running
    ),
  };
};
