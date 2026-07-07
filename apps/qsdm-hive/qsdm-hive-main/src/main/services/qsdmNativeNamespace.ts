import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import getCurrentSlot from 'main/controllers/getCurrentSlot';
import {
  getTaskDataFromCache,
  updateTaskCacheRecord,
} from 'main/services/tasks-cache-utils';
import { RawTaskData, Submission, SubmissionsPerRound } from 'models';

import { getQsdmTaskActionSender } from './qsdmTaskActionSigner';
import { submitQsdmTaskActionIntent } from './qsdmTaskActions';

type NativeNamespaceCallParams = {
  taskId: string;
  method: string;
  params: unknown[];
  taskData?: Partial<RawTaskData>;
};

type NativeNamespaceCallResult =
  | {
      handled: false;
    }
  | {
      handled: true;
      response: unknown;
    };

const DEFAULT_NATIVE_SUBMISSION_METHODS = ['checkSubmissionAndUpdateRound'];

const readEnv = (key: string, fallback: string) => {
  const value = process.env[key];
  return value?.trim() || fallback;
};

const getNativeSubmissionMethods = () =>
  readEnv(
    'QSDM_NATIVE_SUBMISSION_METHODS',
    DEFAULT_NATIVE_SUBMISSION_METHODS.join(',')
  )
    .split(',')
    .map((method) => method.trim())
    .filter(Boolean);

const toRound = (value: unknown, fallback?: number): number => {
  const round = typeof value === 'number' ? value : Number(value);
  if (Number.isFinite(round) && round >= 0) {
    return Math.floor(round);
  }
  return Number.isFinite(fallback) && fallback !== undefined
    ? Math.floor(fallback)
    : 0;
};

const toSubmissionValue = (value: unknown): string => {
  if (value === undefined || value === null) {
    throw new Error('Native QSDM submission value is required');
  }
  return typeof value === 'string' ? value : JSON.stringify(value);
};

const cacheNativeSubmission = async ({
  taskId,
  sender,
  round,
  submission,
}: {
  taskId: string;
  sender: string;
  round: number;
  submission: Submission;
}) => {
  const existingCache = await getTaskDataFromCache(taskId, 'submissions');
  const existingSubmissions = existingCache?.submissions || {};
  const roundKey = String(round);
  const mergedSubmissions: SubmissionsPerRound = {
    ...existingSubmissions,
    [roundKey]: {
      ...(existingSubmissions[roundKey] || {}),
      [sender]: submission,
    },
  };

  await updateTaskCacheRecord(
    taskId,
    { submissions: mergedSubmissions },
    'submissions'
  );
};

export const tryHandleQsdmNativeNamespaceCall = async ({
  taskId,
  method,
  params,
  taskData,
}: NativeNamespaceCallParams): Promise<NativeNamespaceCallResult> => {
  if (
    QSDM_TASK_RUNTIME_MODE !== 'qsdm-native' ||
    !getNativeSubmissionMethods().includes(method)
  ) {
    return { handled: false };
  }

  const sender = getQsdmTaskActionSender();
  if (!sender) {
    throw new Error(
      'QSDM_TASK_ACTION_SENDER or QSDM_WALLET_ADDRESS is required to submit native QSDM task proofs'
    );
  }

  const [submissionParam, roundParam] = params;
  const round = toRound(roundParam, taskData?.current_round);
  const slot = await getCurrentSlot();
  const submissionValue = toSubmissionValue(submissionParam);
  const rewardAmount = taskData?.bounty_amount_per_round || 0;

  const submission: Submission = {
    submission_value: submissionValue,
    slot,
    ...(rewardAmount > 0 ? { reward_amount: rewardAmount } : {}),
  };

  const response = await submitQsdmTaskActionIntent({
    taskId,
    action: 'submit',
    payload: {
      source: 'qsdm-hive',
      namespace_method: method,
      round,
      slot,
      submission_value: submissionValue,
      ...(rewardAmount > 0 ? { reward_amount: rewardAmount } : {}),
    },
  });

  await cacheNativeSubmission({
    taskId,
    sender,
    round,
    submission,
  });

  return { handled: true, response };
};
