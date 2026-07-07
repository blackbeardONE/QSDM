import React, { useEffect, useState } from 'react';
import toast from 'react-hot-toast';

import { Switch } from 'renderer/components/ui/Switch';

import { CheckIcon, CloseIcon } from '../SchedulerIcons';
import { useUpdateSession } from '../../hooks';

type PropsType = {
  sessionId: string;
  disabled?: boolean;
  value?: boolean;
};

export function ToggleScheduleSwitch({
  sessionId,
  disabled,
  value,
}: PropsType) {
  const [isChecked, setIsChecked] = useState<boolean | null | undefined>(value);

  useEffect(() => {
    setIsChecked(value);
  }, [value]);

  const updateSessionMutation = useUpdateSession({
    onSessionUpdateError(error) {
      toast.error(error, {
        duration: 4500,
        icon: <CloseIcon className="w-5 h-5" />,
        style: {
          backgroundColor: '#FFA6A6',
          paddingRight: 0,
          maxWidth: '100%',
        },
      });
    },
    onSessionUpdateSuccess(data) {
      toast.success(
        data.variables.isEnabled ? 'Session enabled.' : 'Session disabled.',
        {
          duration: 4500,
          icon: <CheckIcon className="w-5 h-5" />,
          style: {
            backgroundColor: '#BEF0ED',
            paddingRight: 0,
            maxWidth: '100%',
          },
        }
      );
    },
  });

  const onSwitch = async (e?: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e ? e.target.checked : false;
    const previousValue = !!isChecked;
    setIsChecked(newValue);

    try {
      await updateSessionMutation.mutateAsync({
        id: sessionId,
        isEnabled: newValue,
      });
    } catch {
      setIsChecked(previousValue);
    }
  };

  return (
    <Switch
      id={`${sessionId}onoff`}
      isChecked={!!isChecked}
      onSwitch={onSwitch}
      disabled={disabled || updateSessionMutation.isLoading}
    />
  );
}
