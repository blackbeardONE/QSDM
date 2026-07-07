import { SystemDbKeys } from 'config/systemDbKeys';
import { ScheduleMetadata } from 'models';

import getUserConfig from '../controllers/getUserConfig';

import { TaskSchedulerService } from './TaskSchedulerService';

jest.mock('../controllers/getUserConfig', () => ({
  __esModule: true,
  default: jest.fn(),
}));

const mockedGetUserConfig = getUserConfig as jest.MockedFunction<
  typeof getUserConfig
>;

const flushSchedulerLoad = async () => {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
};

const makeNamespace = (initialStore: Record<string, string> = {}) => {
  const store = new Map<string, string>(Object.entries(initialStore));

  return {
    namespace: {
      storeGet: jest.fn(async (key: string) => store.get(key)),
      storeSet: jest.fn(async (key: string, value: string) => {
        store.set(key, value);
        return value;
      }),
    },
    store,
  };
};

const stopAllJobs = (service: TaskSchedulerService) => {
  service.schedules.forEach((schedule) => {
    schedule.startJob?.stop();
    schedule.stopJob?.stop();
  });
};

describe('TaskSchedulerService', () => {
  const services: TaskSchedulerService[] = [];

  beforeEach(() => {
    mockedGetUserConfig.mockResolvedValue({ stayAwake: 1 });
  });

  afterEach(() => {
    services.forEach(stopAllJobs);
    services.length = 0;
    jest.clearAllMocks();
  });

  it('starts persisted enabled schedules when Stay Awake is enabled', async () => {
    const schedules: ScheduleMetadata[] = [
      {
        id: 'session-1',
        startTime: '23:00:00',
        stopTime: '23:30:00',
        days: [1],
        isEnabled: true,
      },
    ];
    const { namespace } = makeNamespace({
      [SystemDbKeys.Schedules]: JSON.stringify(schedules),
    });

    const service = new TaskSchedulerService(
      namespace as any,
      async () => undefined,
      async () => undefined
    );
    services.push(service);

    await flushSchedulerLoad();

    const loadedSchedule = service.schedules.get('session-1');
    expect(loadedSchedule?.startJob?.running).toBe(true);
    expect(loadedSchedule?.stopJob?.running).toBe(true);
  });

  it('preserves stop time and rebuilds jobs when only days change', async () => {
    const { namespace, store } = makeNamespace();
    const service = new TaskSchedulerService(
      namespace as any,
      async () => undefined,
      async () => undefined
    );
    services.push(service);

    await flushSchedulerLoad();
    await service.setTaskSchedule({
      id: 'session-1',
      startTime: '08:00:00',
      stopTime: '10:00:00',
      days: [1],
      isEnabled: false,
    });

    const originalStartJob = service.schedules.get('session-1')?.startJob;

    await service.updateTaskSchedule({
      id: 'session-1',
      days: [2, 3],
    });

    const updatedSchedule = service.schedules.get('session-1');
    expect(updatedSchedule?.days).toEqual([2, 3]);
    expect(updatedSchedule?.stopTime).toBe('10:00:00');
    expect(updatedSchedule?.startJob).not.toBe(originalStartJob);
    expect(JSON.parse(store.get(SystemDbKeys.Schedules) || '[]')).toEqual([
      {
        id: 'session-1',
        startTime: '08:00:00',
        stopTime: '10:00:00',
        days: [2, 3],
        isEnabled: false,
      },
    ]);
  });
});
