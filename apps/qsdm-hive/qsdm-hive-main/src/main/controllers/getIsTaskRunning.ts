import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import { GetIsTaskRunningParam } from 'models';

export const getIsTaskRunning = async (
  _: Event,
  { taskPublicKey }: GetIsTaskRunningParam
): Promise<boolean> => {
  if (qsdmHiveTasks.RUNNING_TASKS[taskPublicKey]) {
    return true;
  }

  const intendedRunningTasks = await qsdmHiveTasks.getRunningTaskPubKeys();
  if (intendedRunningTasks.includes(taskPublicKey)) {
    await qsdmHiveTasks.reconcileQsdmSystemTaskRuntime(taskPublicKey, {
      submitStartAction: true,
    });
  }

  return Boolean(qsdmHiveTasks.RUNNING_TASKS[taskPublicKey]);
};
