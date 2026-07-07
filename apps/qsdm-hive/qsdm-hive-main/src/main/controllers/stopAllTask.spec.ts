import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

import stopTask from './stopTask';
import { stopAllTasks } from './stopAllTask';
import { getSchedulerTasks } from './tasksScheduler/getSchedulerTasks';

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    getStartedTasks: jest.fn(),
  },
}));

jest.mock('./stopTask', () => ({
  __esModule: true,
  default: jest.fn(),
}));

jest.mock('./tasksScheduler/getSchedulerTasks', () => ({
  getSchedulerTasks: jest.fn(),
}));

describe('stopAllTasks scheduled mode', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (qsdmHiveTasks.getStartedTasks as jest.Mock).mockResolvedValue([
      { task_id: 'unscheduled-task', is_running: true },
      { task_id: 'scheduled-task', is_running: true },
    ]);
  });

  it('does not stop every task when no tasks are scheduled', async () => {
    (getSchedulerTasks as jest.Mock).mockResolvedValue([]);

    await stopAllTasks({} as Event, { runOnlyScheduledTasks: true });

    expect(stopTask).not.toHaveBeenCalled();
  });

  it('stops only tasks marked for automation', async () => {
    (getSchedulerTasks as jest.Mock).mockResolvedValue(['scheduled-task']);

    await stopAllTasks({} as Event, { runOnlyScheduledTasks: true });

    expect(stopTask).toHaveBeenCalledTimes(1);
    expect(stopTask).toHaveBeenCalledWith({} as Event, {
      taskAccountPubKey: 'scheduled-task',
    });
  });
});
