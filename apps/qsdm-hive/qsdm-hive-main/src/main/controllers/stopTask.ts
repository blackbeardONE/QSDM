import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import { TaskStartStopParam } from 'models/api';

const stopTask = async (event: Event, payload: TaskStartStopParam) => {
  const { taskAccountPubKey } = payload;

  await qsdmHiveTasks.stopTask(taskAccountPubKey);
  return true;
};

export default stopTask;
