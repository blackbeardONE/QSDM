import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

import stopTask from './stopTask';
import { getSchedulerTasks } from './tasksScheduler/getSchedulerTasks';
import { StartStopAllTasksParams } from './types';

type StopAllTasksParams = StartStopAllTasksParams;

export const stopAllTasks = async (
  _: Event,
  { runOnlyScheduledTasks = false }: StopAllTasksParams = {}
) => {
  try {
    const startedTasks = await qsdmHiveTasks.getStartedTasks();
    const schedulerTasks = runOnlyScheduledTasks
      ? await getSchedulerTasks({} as Event)
      : [];

    const stopTaskPromises = startedTasks
      .filter((rawTaskData) => rawTaskData.is_running)
      .filter((rawTaskData) => {
        if (!runOnlyScheduledTasks) return true;
        return schedulerTasks.includes(rawTaskData.task_id);
      });

    for (const rawTaskData of stopTaskPromises) {
      await stopTask({} as Event, { taskAccountPubKey: rawTaskData.task_id });
    }
  } catch (error) {
    console.error('Failed to stop all tasks:', error);
    throw error;
  }
};
