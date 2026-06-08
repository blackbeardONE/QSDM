import { Icon, InformationCircleLine } from 'vendor/qsdm-styleguide';
import React from 'react';

import { Button, Placement } from 'renderer/components/ui';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { Theme } from 'renderer/types/common';

import { TaskName } from '../../../common';

type PropsType = {
  publicKey: string;
  taskName: string;
  infoIconClasses: string;
  handleToggleInfoAccordion: () => void;
  propsManagingMainTooltipState: any;
  tooltipRightPlacement?: Placement;
  infoButtonTooltipContent: string;
};

export function TaskInfoCell({
  taskName,
  infoIconClasses,
  handleToggleInfoAccordion,
  propsManagingMainTooltipState,
  infoButtonTooltipContent,
}: PropsType) {
  return (
    <div className="flex items-center gap-2 justify-self-start">
      <Popover theme={Theme.Dark} tooltipContent={infoButtonTooltipContent}>
        <div
          {...propsManagingMainTooltipState}
          className="flex flex-col items-center justify-start w-10"
        >
          <Button
            onClick={handleToggleInfoAccordion}
            icon={
              <Icon
                source={InformationCircleLine}
                size={32}
                className={infoIconClasses}
              />
            }
            onlyIcon
          />
        </div>
      </Popover>

      <Popover theme={Theme.Dark} tooltipContent={taskName}>
        <TaskName taskName={taskName} />
      </Popover>
    </div>
  );
}
