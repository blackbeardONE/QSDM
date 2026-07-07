import { buildQsdmTaskReadUrls, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { qsdmGetFirstJson } from 'main/services/qsdmHttpRead';
import { getTaskDataFromCache } from 'main/services/tasks-cache-utils';
import { QsdmTaskResponse } from 'models/api/qsdm';
import { GetRetryDataByTaskIdParam, SubmissionsPerRound } from 'models';

export const getTaskSubmissions = async (
  _: Event,
  { taskPubKey }: GetRetryDataByTaskIdParam
): Promise<SubmissionsPerRound> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    try {
      const response = await qsdmGetFirstJson<QsdmTaskResponse>(
        buildQsdmTaskReadUrls(
          `/tasks/${encodeURIComponent(taskPubKey)}`
        ),
        { timeout: 4000 }
      );
      return response.task?.submissions || {};
    } catch (error) {
      console.warn(
        'Native task submissions are temporarily unavailable; using the local cache.',
        error instanceof Error ? error.message : String(error)
      );
    }
  }

  const submissions = await getTaskDataFromCache(taskPubKey, 'submissions');

  return submissions?.submissions || {};
};
