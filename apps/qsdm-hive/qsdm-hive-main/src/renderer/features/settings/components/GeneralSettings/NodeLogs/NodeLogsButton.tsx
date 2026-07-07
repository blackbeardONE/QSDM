import React from 'react';

import { Button } from 'renderer/components/ui';

export function NodeLogsButton() {
  const handleClick = () => {
    window.main.openNodeLogfileFolder();
  };

  return (
    <Button
      onClick={handleClick}
      label="Get Node Logs"
      className="w-40 h-10 text-sm font-semibold text-white bg-transparent border border-white"
    />
  );
}
