import React, { KeyboardEventHandler, MouseEventHandler } from 'react';

import GenericThumbnail from 'assets/svgs/generic-task-thumbnail.png';
import QsdmHiveLogo from 'assets/svgs/qsdm-hive-logo.svg';
import { isQsdmSystemTaskId } from 'config/qsdmSystemTasks';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { Theme } from 'renderer/types/common';

interface Props {
  taskName: string;
  tooltipContent: string;
  onKeyDown: KeyboardEventHandler;
  onClick: MouseEventHandler;
  src: string;
  taskId?: string;
  isBonusTask?: boolean;
  isBountyEmpty?: boolean;
}

export function Thumbnail({
  taskName,
  tooltipContent,
  onKeyDown,
  onClick,
  src,
  taskId,
  isBonusTask,
  isBountyEmpty,
}: Props) {
  const shouldUseQsdmLogo = isQsdmSystemTaskId(taskId);

  return (
    <div className="flex flex-row gap-x-4 justify-self-start">
      <Popover tooltipContent={tooltipContent} theme={Theme.Dark}>
        <div className="relative w-[10.25rem] h-auto md2:w-[12rem] transition-all duration-300 ease-in-out rounded-md">
          {shouldUseQsdmLogo ? (
            <div className="h-[62px] md2:h-[72px] w-full flex items-center justify-center rounded-md bg-[#091b2a]">
              <QsdmHiveLogo className="h-11 w-11 text-finnieTeal" />
            </div>
          ) : (
            <img
              src={src}
              onError={(e) => {
                e.currentTarget.src = GenericThumbnail;
              }}
              alt={`${taskName} thumbnail`}
              className={`max-h-[62px] md2:max-h-[72px] mx-auto transition-all duration-300 ease-in-out rounded-md ${
                isBonusTask && isBountyEmpty ? 'grayscale' : ''
              }`}
            />
          )}
          <button
            onKeyDown={onKeyDown}
            tabIndex={0}
            onClick={onClick}
            aria-label={`${tooltipContent}: ${taskName}`}
            className="absolute rounded-md inset-0 flex items-end justify-center bg-gradient-to-t from-theme-shade via-theme-shade/70 to-transparent opacity-100 transition-opacity duration-300 ease-in-out"
          >
            <span
              onKeyDown={onKeyDown}
              tabIndex={0}
              role="button"
              onClick={onClick}
              className="w-full max-h-[34px] overflow-hidden px-2 pb-1 text-center text-white text-[13px] leading-tight [text-shadow:0_1px_3px_rgba(0,0,0,0.9)]"
            >
              {taskName}
            </span>
          </button>
        </div>
      </Popover>
    </div>
  );
}
