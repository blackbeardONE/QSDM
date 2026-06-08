import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import { GetIsTaskRunningParam } from 'models';

export const getIsTaskRunning = async (
  _: Event,
  { taskPublicKey }: GetIsTaskRunningParam
): Promise<boolean> => {
  await qsdmHiveTasks.reconcileQsdmSystemTaskRuntime(taskPublicKey, {
    submitStartAction: true,
  });
  const runningTaskPubKeys = Object.keys(qsdmHiveTasks.RUNNING_TASKS);
  const isRunning = runningTaskPubKeys?.includes(taskPublicKey);
  return isRunning;
};
