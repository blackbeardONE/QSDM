import {
  buildQsdmTaskReadUrls,
  QSDM_CELL_DECIMALS,
} from 'config/qsdm';
import {
  QsdmTaskResponse,
  QsdmTaskStateParticipant,
  QsdmTaskStateResponse,
} from 'models/api/qsdm';

import { getQsdmTaskActionSender } from './qsdmTaskActionSigner';
import { getQsdmReadErrorMessage, qsdmGetJson } from './qsdmHttpRead';
import { getTaskDataFromCache } from './tasks-cache-utils';

export const qsdmCellToDenomination = (amount: number) =>
  Math.round(amount * 10 ** QSDM_CELL_DECIMALS);

const QSDM_NATIVE_CELL_AMOUNT_NORMALIZATION_LIMIT = 1_000_000;

export const readFiniteNumber = (value: unknown) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
};

export const normalizeQsdmNativeCellAmountToDenomination = (
  value: number | undefined
) => {
  const amount = readFiniteNumber(value);
  if (amount === undefined) {
    return value;
  }

  return amount > 0 && amount < QSDM_NATIVE_CELL_AMOUNT_NORMALIZATION_LIMIT
    ? qsdmCellToDenomination(amount)
    : amount;
};

export const normalizeQsdmNativeCellAmountMapToDenomination = (
  values: Record<string, number> = {}
) =>
  Object.entries(values).reduce((acc, [key, value]) => {
    acc[key] = normalizeQsdmNativeCellAmountToDenomination(value) || 0;
    return acc;
  }, {} as Record<string, number>);

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

const getParticipantSender = (
  participantKey: string,
  participant: QsdmTaskStateParticipant
) => participant.sender || participantKey;

export const getQsdmTaskParticipantBySender = (
  participants: Record<string, QsdmTaskStateParticipant> = {},
  sender = getQsdmTaskActionSender()
) => {
  const normalizedSender = (sender || '').toLowerCase();
  if (!normalizedSender) {
    return undefined;
  }

  return Object.entries(participants).find(([participantKey, participant]) => {
    const participantSender = getParticipantSender(
      participantKey,
      participant
    );
    return participantSender.toLowerCase() === normalizedSender;
  })?.[1];
};

const readStakeFromTaskState = async (sender: string, url: string) => {
  const response = await qsdmGetJson<QsdmTaskStateResponse>(url, {
    timeout: 4000,
  });
  if (!response.task) {
    return undefined;
  }

  const participant = getQsdmTaskParticipantBySender(
    response.task.participants,
    sender
  );

  return readFiniteNumber(participant?.stake) || 0;
};

export const getConfirmedQsdmTaskState = async (taskId: string) => {
  const encodedTaskId = encodeURIComponent(taskId);
  const errors: string[] = [];

  for (const stateUrl of buildQsdmTaskReadUrls(
    `/tasks/${encodedTaskId}/state`
  )) {
    try {
      const response = await qsdmGetJson<QsdmTaskStateResponse>(stateUrl, {
        timeout: 4000,
      });
      if (response.task) {
        return response;
      }
    } catch (error) {
      errors.push(`${stateUrl}: ${getQsdmReadErrorMessage(error)}`);
    }
  }

  throw new Error(
    `Could not read native QSDM task state for ${taskId}: ${errors.join('; ')}`
  );
};

const readStakeFromTaskRegistry = async (sender: string, url: string) => {
  const response = await qsdmGetJson<QsdmTaskResponse>(url, {
    timeout: 4000,
  });
  if (!response.task) {
    return undefined;
  }

  return readFiniteNumber(response.task.stake_list?.[sender]) || 0;
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

  for (const stateUrl of buildQsdmTaskReadUrls(
    `/tasks/${encodedTaskId}/state`
  )) {
    try {
      const stake = await readStakeFromTaskState(sender, stateUrl);
      if (stake !== undefined) {
        return stake;
      }
    } catch (error) {
      errors.push(`${stateUrl}: ${getQsdmReadErrorMessage(error)}`);
    }
  }

  for (const taskUrl of buildQsdmTaskReadUrls(
    `/tasks/${encodedTaskId}`
  )) {
    try {
      const stake = await readStakeFromTaskRegistry(sender, taskUrl);
      if (stake !== undefined) {
        return stake;
      }
    } catch (error) {
      errors.push(`${taskUrl}: ${getQsdmReadErrorMessage(error)}`);
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
