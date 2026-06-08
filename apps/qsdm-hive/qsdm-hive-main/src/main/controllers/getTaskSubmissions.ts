import axios from 'axios';
import { buildQsdmCoreApiUrl, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { getTaskDataFromCache } from 'main/services/tasks-cache-utils';
import { QsdmTaskResponse } from 'models/api/qsdm';
import { GetRetryDataByTaskIdParam, SubmissionsPerRound } from 'models';

export const getTaskSubmissions = async (
  _: Event,
  { taskPubKey }: GetRetryDataByTaskIdParam
): Promise<SubmissionsPerRound> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    try {
      const response = await axios.get<QsdmTaskResponse>(
        buildQsdmCoreApiUrl(`/tasks/${encodeURIComponent(taskPubKey)}`),
        { timeout: 10000 }
      );
      return response.data.task?.submissions || {};
    } catch (error) {
      console.error('Error while fetching native task submissions', error);
    }
  }

  const submissions = await getTaskDataFromCache(taskPubKey, 'submissions');

  return submissions?.submissions || {};
};
