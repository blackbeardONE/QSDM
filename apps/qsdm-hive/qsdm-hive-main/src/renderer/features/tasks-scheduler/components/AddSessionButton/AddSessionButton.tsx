import React from 'react';
import { twMerge } from 'tailwind-merge';

import { AddIcon } from '../SchedulerIcons';

type PropsType = {
  onClick: () => void;
  disabled?: boolean;
};

export function AddSessionButton({ onClick, disabled }: PropsType) {
  const buttonClasses = twMerge(
    'flex items-center gap-3 px-4 py-2 w-fit',
    disabled && 'opacity-50 cursor-not-allowed'
  );

  return (
    <button className={buttonClasses} onClick={onClick} disabled={disabled}>
      <AddIcon className="w-[18px] h-[18px] text-green-2" />
      <div className="text-sm font-semibold underline underline-offset-2 text-green-2">
        Add Session
      </div>
    </button>
  );
}
