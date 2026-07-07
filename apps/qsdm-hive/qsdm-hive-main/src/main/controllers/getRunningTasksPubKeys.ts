import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

export const getRunningTasksPubKeys = async (
  event: Event
): Promise<string[]> => {
  const runningTasks = qsdmHiveTasks.RUNNING_TASKS;
  const runningTasksPubKeys = Object.keys(runningTasks);
  return runningTasksPubKeys;
};
