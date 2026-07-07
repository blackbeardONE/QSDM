import React, { useState } from 'react';

import { ColumnsLayout } from 'renderer/components/ui';
import { Icon, UploadLine } from 'vendor/qsdm-styleguide';

import { AddPrivateTask } from './AddPrivateTask/AddPrivateTask';
import { AdvancedButton } from './AdvancedButton';
import { TaskStudio } from './TaskStudio/TaskStudio';

interface Props {
  columnsLayout: ColumnsLayout;
}

export function AdvancedOptions({ columnsLayout }: Props) {
  const [visiblePanel, setVisiblePanel] = useState<
    'private-task' | 'task-studio' | null
  >(null);

  const handleAdvancedButtonClick = () => {
    setVisiblePanel((current) =>
      current === 'private-task' ? null : 'private-task'
    );
  };

  const handleAddPrivateTaskClose = () => {
    setVisiblePanel(null);
  };

  const animationClasses = visiblePanel
    ? 'opacity-100 scale-100'
    : 'opacity-0 scale-95';

  return (
    <div className="z-50">
      <div
        className={`transition-all duration-500 ease-in-out transform ${animationClasses}`}
      >
        {visiblePanel === 'private-task' && (
          <div>
            <AddPrivateTask
              columnsLayout={columnsLayout}
              onClose={handleAddPrivateTaskClose}
            />
          </div>
        )}
        {visiblePanel === 'task-studio' && (
          <TaskStudio onClose={() => setVisiblePanel(null)} />
        )}
      </div>
      {!visiblePanel && (
        <div className="pt-4 pl-4 flex items-center gap-8">
          <AdvancedButton onClick={handleAdvancedButtonClick} />
          <button
            type="button"
            className="flex items-center gap-3 text-sm text-white"
            onClick={() => setVisiblePanel('task-studio')}
          >
            <Icon source={UploadLine} size={18} color="#5ED9D1" />
            Task Studio
          </button>
        </div>
      )}
    </div>
  );
}
