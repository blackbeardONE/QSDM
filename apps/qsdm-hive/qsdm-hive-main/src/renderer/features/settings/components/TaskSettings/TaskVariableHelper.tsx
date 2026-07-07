import React from 'react';

import { Button } from 'renderer/components/ui';

import { HelperIcon } from './TaskExtensionIcons';

export const TaskVariableHelper: React.FC = () => {
  const handleOpenHelperPage = () => {
    // Open the landing page in default browser in full screen
    const { width } = window.screen;
    const { height } = window.screen;

    const windowFeatures = `
      width=${width},
      height=${height},
    `.replace(/\s/g, '');

    window.open('http://localhost:30017/task-helper', '_blank', windowFeatures);
  };

  return (
    <div className="mb-8">
      <div className="text-left w-full rounded text-white flex flex-col gap-4">
        <div className="flex items-center gap-4 pt-2 text-2xl font-semibold">
          <HelperIcon className="w-8 h-8" />
          <span>Task Extension Helper</span>
        </div>

        <div className="mr-12 text-sm">
          <p>
            This helper shows which local variables QSDM Hive can pass to QSDM
            Core tasks.
          </p>
          <p>
            Values stay on this machine. External add-ons are disabled in this
            build, so a task only receives variables that you explicitly save
            and pair.
          </p>
        </div>

        <Button
          label="Open Extension Helper"
          onClick={handleOpenHelperPage}
          className="w-64 h-10 font-semibold bg-white text-finnieBlue-light"
        />
      </div>
    </div>
  );
};
