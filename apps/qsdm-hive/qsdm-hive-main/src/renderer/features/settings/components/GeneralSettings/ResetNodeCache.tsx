import React from 'react';

import { Button } from 'renderer/components/ui';
import { useConfirmModal } from 'renderer/features/shared';

export function ResetNodeCache() {
  const { showModal: confirmCacheReset } = useConfirmModal({
    header: 'Reset Node Cache',
    content: 'Are you sure you want to reset the node cache?',
  });

  const resetNodeCache = () => {
    confirmCacheReset().then(async (confirmed) => {
      if (confirmed) {
        await window.main.resetTasksCache();
        window.location.reload();
      }
    });
  };

  return (
    <div className="flex justify-end w-fit mr-auto">
      <Button
        onClick={resetNodeCache}
        label="Reset Cache"
        className="w-40 h-10 text-sm font-semibold text-white bg-transparent border border-white"
      />
    </div>
  );
}
