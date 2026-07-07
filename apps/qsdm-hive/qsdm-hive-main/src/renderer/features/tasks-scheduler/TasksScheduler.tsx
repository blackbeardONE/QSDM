import React, { useCallback } from 'react';
import { toast } from 'react-hot-toast';
import { useQueryClient } from 'react-query';

import { isNumber } from 'lodash';
import { TimeFormat } from 'models';
import { useUserAppConfig } from 'renderer/features/settings/hooks/useUserAppConfig';
import {
  QueryKeys,
  addTasksSchedulerSession,
  removeTasksSchedulerSession,
} from 'renderer/services';

import { AddSessionButton } from './components/AddSessionButton';
import { CheckIcon, ClockIcon } from './components/SchedulerIcons';
import { Session } from './components/Session/Session';
import { useDefaultSchedulerSession, useTaskSchedulers } from './hooks';

export function TasksScheduler() {
  const queryCache = useQueryClient();
  useDefaultSchedulerSession();

  const { userConfig } = useUserAppConfig({});
  const { schedulerSessions, refetchSchedules } = useTaskSchedulers();

  const handleAddSessionClick = useCallback(async () => {
    const defaultTimeString: TimeFormat = '00:00:00';

    const newSession = {
      startTime: defaultTimeString,
      stopTime: null,
      days: [],
      isEnabled: false,
    };

    try {
      await addTasksSchedulerSession(newSession);
    } catch (error) {
      console.error('Failed to add new scheduler session: ', error);
      toast.error('Failed to add new scheduler session');
    } finally {
      queryCache.invalidateQueries([QueryKeys.SchedulerSessions]);
    }
  }, [queryCache]);

  const handleRemoveSessionClick = useCallback(
    async (sessionId: string) => {
      try {
        await removeTasksSchedulerSession(sessionId);
        toast.success('Session removed.', {
          duration: 4500,
          icon: <CheckIcon className="w-5 h-5" />,
          style: {
            backgroundColor: '#BEF0ED',
            paddingRight: 0,
            maxWidth: '100%',
          },
        });
      } catch (error) {
        console.error('Failed to remove scheduler session: ', error);
        toast.error('Failed to remove scheduler session');
      } finally {
        queryCache.invalidateQueries([QueryKeys.SchedulerSessions]);
      }
    },
    [queryCache]
  );

  const isStayAwakeEnabled = isNumber(userConfig?.stayAwake);

  const sessions = schedulerSessions ?? [];

  return (
    <div>
      <div className="flex justify-between">
        <div>
          <div className="flex justify-between mb-4">
            <div className="text-xl font-semibold text-white ">
              Schedule QSDM task work time
            </div>
          </div>

          <div className="mb-5 text-sm leading-6 text-white w-[460px] xl:w-full">
            Choose when QSDM Hive starts and stops the tasks you marked for
            automation.
            <br /> Keep this computer awake during scheduled windows so CELL
            task work can run without interruption.
          </div>

          <div className="mb-5 text-sm leading-6 text-white w-[460px] xl:w-full flex">
            <span>Turn on the Automate toggle&nbsp;&nbsp;</span>
            <ClockIcon className="w-5 h-5 text-white" />
            <span>
              &nbsp;&nbsp;inside each task&apos;s details that you want to
              schedule through QSDM.
            </span>
          </div>

          {!isStayAwakeEnabled && (
            <div className="mb-5 rounded-md border border-finnieEmerald-light/40 bg-purple-light-transparent px-4 py-3 text-sm leading-6 text-finnieTeal-100 w-[460px] xl:w-full">
              Enable Settings &gt; General &gt; Stay Awake before relying on
              automation. Schedules remain editable, but QSDM Hive will not run
              sessions while Windows is allowed to sleep.
            </div>
          )}
        </div>
      </div>

      <div className="mb-2">
        <div className="mb-3 text-base font-semibold text-finnieEmerald-light">
          Select the time and days of the week.
        </div>
        <div className="flex flex-col gap-4">
          {sessions?.map((session) => (
            <Session
              scheduleMetadata={session}
              key={session.id}
              disabled={!isStayAwakeEnabled}
              onRemoveSessionClick={handleRemoveSessionClick}
              refetchSchedules={refetchSchedules}
            />
          ))}
        </div>
      </div>

      <AddSessionButton
        onClick={handleAddSessionClick}
        disabled={!isStayAwakeEnabled}
      />
    </div>
  );
}
