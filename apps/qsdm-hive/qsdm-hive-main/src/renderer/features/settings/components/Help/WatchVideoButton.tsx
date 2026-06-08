import React from 'react';

import { Button } from 'renderer/components/ui';
import { openBrowserWindow } from 'renderer/services';

export function WatchVideoButton() {
  const redirectToQsdmVideo = () => openBrowserWindow('https://qsdm.tech');

  return (
    <Button
      label="Watch a Video"
      onClick={redirectToQsdmVideo}
      className="font-semibold bg-transparent border-2 text-white text-[14px] leading-[14px] min-w-[200px] h-9 self-end mt-2"
    />
  );
}
