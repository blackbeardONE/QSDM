import { Event } from 'electron';

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

import { getRunnedPrivateTasks } from './privateTasks';
import startTask from './startTask';
import { startAllTasks } from './startAllTasks';
import { getSchedulerTasks } from './tasksScheduler/getSchedulerTasks';

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    getStartedTasks: jest.fn(),
  },
}));

jest.mock('./privateTasks', () => ({
  getRunnedPrivateTasks: jest.fn(),
}));

jest.mock('./startTask', () => ({
  __esModule: true,
  default: jest.fn(),
}));

jest.mock('./tasksScheduler/getSchedulerTasks', () => ({
  getSchedulerTasks: jest.fn(),
}));

describe('startAllTasks scheduled mode', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (getRunnedPrivateTasks as jest.Mock).mockResolvedValue([]);
  });

  it('skips unscheduled tasks without blocking later scheduled tasks', async () => {
    (qsdmHiveTasks.getStartedTasks as jest.Mock).mockResolvedValue([
      { task_id: 'unscheduled-task', is_running: false },
      { task_id: 'scheduled-task', is_running: false },
    ]);
    (getSchedulerTasks as jest.Mock).mockResolvedValue(['scheduled-task']);

    await startAllTasks({} as Event, { runOnlyScheduledTasks: true });

    expect(startTask).toHaveBeenCalledTimes(1);
    expect(startTask).toHaveBeenCalledWith({} as Event, {
      taskAccountPubKey: 'scheduled-task',
      isPrivate: false,
    });
  });
});
