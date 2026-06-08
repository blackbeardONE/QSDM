import { taskScheduler } from '../../tasks-scheduler';

export const removeSession = async (_: Event, payload: { id: string }) => {
  const { id } = payload;

  await taskScheduler.removeSchedule(id);
};
