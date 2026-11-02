import React from 'react';

import { Popover } from 'renderer/components/ui/Popover/Popover';
import { Theme } from 'renderer/types/common';

import { NOT_AVAILABLE_PLACEHOLDER } from '../constants';

import { type TaskStat } from './TaskStats';

type PropsType = TaskStat;

export function StatBox({ label, value, fullValue }: PropsType) {
  const getContent = () => {
    if (!value) {
      return NOT_AVAILABLE_PLACEHOLDER;
    }

    if (fullValue && value) {
      return (
        <Popover theme={Theme.Light} tooltipContent={fullValue}>
          {value}
        </Popover>
      );
    }

    return value;
  };

  const content = getContent();

  return (
    <div className="flex flex-col p-3 2xl:p-4 text-white rounded-lg border border-qsdm-border bg-qsdm-panel w-[15%] overflow-hidden">
      <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-[0.08em] text-qsdm-muted">
        {label}
      </div>
      <div className="font-mono text-xl xl:text-2xl font-medium">{content}</div>
    </div>
  );
}
