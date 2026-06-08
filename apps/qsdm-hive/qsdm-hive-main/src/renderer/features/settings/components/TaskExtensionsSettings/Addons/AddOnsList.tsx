import React from 'react';

export function AddOnsList() {
  return (
    <div className="m-4 mt-0 max-w-md">
      <div className="mb-4 text-2xl font-semibold text-left">Add-ons</div>
      <div className="p-4 mb-4 text-sm text-left rounded-md bg-finnieBlue-light-tertiary text-gray-300">
        QSDM Hive currently supports local task extensions only. QSDM Core
        reads saved variables through Hive and the signed CELL action loop;
        third-party add-on packages are intentionally disabled in this build.
      </div>
    </div>
  );
}
