import { Event } from 'electron';

import { SystemDbKeys } from 'config/systemDbKeys';
import { namespaceInstance } from 'main/node/helpers/Namespace';

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export const getSchedulerTasks = async (event: Event): Promise<string[]> => {
  try {
    const dbResult = await namespaceInstance.storeGet(
      SystemDbKeys.SchedulerTasks
    );

    if (!dbResult) {
      return [];
    }

    const results = JSON.parse(dbResult) as string[];
    return Array.isArray(results)
      ? results.filter((taskId): taskId is string => typeof taskId === 'string')
      : [];
  } catch (err) {
    console.warn('Invalid scheduler task list; starting with an empty list', err);
    return [];
  }
};
