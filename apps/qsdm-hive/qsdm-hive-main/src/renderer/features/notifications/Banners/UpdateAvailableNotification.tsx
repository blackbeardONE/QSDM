// UpdateAvailableNotification

import React, { useEffect } from 'react';

import { BackButtonSlotType, NotificationType } from '../types';
import { useNotificationActions } from '../useNotificationStore';

import { NotificationDisplayBanner } from './components/NotificationDisplayBanner';

export function UpdateAvailableNotification({
  notification,
  BackButtonSlot,
}: {
  notification: NotificationType;
  BackButtonSlot: BackButtonSlotType;
}) {
  const { markAsRead } = useNotificationActions();

  const [isDownloaded, setIsDownloaded] = React.useState(false);

  useEffect(() => {
    const destroy = window.main.onAppDownloaded(() => {
      setIsDownloaded(() => true);

      setTimeout(() => {
        markAsRead(notification.id);
      }, 5000);
    });

    return () => {
      destroy();
    };
  }, [markAsRead, notification.id]);

  const getContent = () => {
    if (isDownloaded) {
      return 'Ready to restart';
    }

    return 'Downloading...';
  };

  return (
    <NotificationDisplayBanner
      notification={notification}
      messageSlot={
        <div className="">
          {isDownloaded
            ? 'The required QSDM Hive update is verified.'
            : 'A required QSDM Hive update is downloading.'}
        </div>
      }
      actionButtonSlot={getContent()}
      BackButtonSlot={BackButtonSlot}
    />
  );
}
