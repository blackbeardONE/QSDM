import React, { useEffect, useMemo, useState } from 'react';
import toast from 'react-hot-toast';
import { useMutation, useQuery, useQueryClient } from 'react-query';
import { twMerge } from 'tailwind-merge';

import { Tooltip } from 'renderer/components/ui';
import { Switch } from 'renderer/components/ui/Switch';
import { ClockIcon } from 'renderer/features/tasks-scheduler/components/SchedulerIcons';
import {
  removeTaskFromScheduler,
  addTaskToScheduler,
  QueryKeys,
  getSchedulerTasks,
} from 'renderer/services';

type PropsType = {
  taskPublicKey: string;
};

export function TurnSchedulerOnOffOnTask({ taskPublicKey }: PropsType) {
  const queryClient = useQueryClient();

  const { data: schedulerTasks, isLoading: isLoadingSchedulerTasks } = useQuery(
    [QueryKeys.SchedulerTasks, taskPublicKey],
    getSchedulerTasks,
    {
      enabled: !!taskPublicKey,
    }
  );

  const isScheduledTask = useMemo(
    () => !!schedulerTasks?.includes(taskPublicKey),
    [taskPublicKey, schedulerTasks]
  );

  const [isChecked, setIsChecked] = useState<boolean>(isScheduledTask);

  useEffect(() => {
    setIsChecked(isScheduledTask);
  }, [isScheduledTask]);

  const mutationAdd = useMutation(() => addTaskToScheduler(taskPublicKey), {
    onSuccess: () => {
      queryClient.invalidateQueries([QueryKeys.SchedulerTasks]);
      setIsChecked(true);
    },
    onError: () => {
      setIsChecked(false);
      toast.error('Failed to enable task automation');
    },
  });

  const mutationRemove = useMutation(
    () => removeTaskFromScheduler(taskPublicKey),
    {
      onSuccess: () => {
        queryClient.invalidateQueries([QueryKeys.SchedulerTasks]);
        setIsChecked(false);
      },
      onError: () => {
        setIsChecked(true);
        toast.error('Failed to disable task automation');
      },
    }
  );

  const handleSwitch = () => {
    if (isChecked) {
      setIsChecked(false);
      mutationRemove.mutate();
    } else {
      setIsChecked(true);
      mutationAdd.mutate();
    }
  };

  const isOn = !!isChecked;

  const switchContainerClasses = twMerge(
    'flex gap-2 rounded-lg w-fit items-center'
  );

  const iconClasses = twMerge('w-5 h-5');

  if (isLoadingSchedulerTasks) {
    return null;
  }

  return (
    <Tooltip
      tooltipContent="This task will run when the Automate feature is turned on."
      placement="bottom-left"
    >
      <div className={switchContainerClasses}>
        <ClockIcon
          className={iconClasses}
          color={isOn ? '#9BE7C4' : '#FFF'}
        />
        <Switch
          id={`switch-${taskPublicKey}`}
          isChecked={isOn}
          disabled={mutationAdd.isLoading || mutationRemove.isLoading}
          onSwitch={handleSwitch}
        />
      </div>
    </Tooltip>
  );
}
