import { ScheduleMetadata } from 'models';

import { taskScheduler } from '../../tasks-scheduler';

export const addSession = async (_: Event, payload: ScheduleMetadata) => {
  await taskScheduler.setTaskSchedule(payload);
};
