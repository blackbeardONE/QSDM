import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

export const getStartedTasksPubKeys = async (
  event?: Event
): Promise<string[]> => {
  return qsdmHiveTasks.getStartedTasksPubKeys();
};
