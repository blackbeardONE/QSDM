import React from 'react';
import { useQuery } from 'react-query';

import { NotificationStatusIndicator } from 'renderer/features/notifications/NotificationsCenter/components/NotificationStatusIndicator';
import { QueryKeys } from 'renderer/services';
import { checkUPnPbinary } from 'renderer/services/api/upnp';

import { Spacer } from '../Spacer';

export function UpnpBinaries() {
  const {
    data: binaryStatus,
    isLoading: isCheckingBinary,
  } = useQuery([QueryKeys.UpnpBinaryExists], () => checkUPnPbinary(), {});
  const isBinaryExists = !!binaryStatus?.exists;

  return (
    <div>
      <div className="mb-2 text-sm">
        UPnP is optional. QSDM Hive includes a built-in UPnP client and falls
        back to Network Tunneling when your router does not support safe port
        mapping.
      </div>

      <Spacer size="md" />

      <div className="flex items-center gap-4">
        {isCheckingBinary ? (
          'Checking UPnP support...'
        ) : (
          <div>
            {isBinaryExists ? (
              <div className="flex items-center gap-1">
                <NotificationStatusIndicator
                  notificationType="SUCCESS"
                  isRead={false}
                />
                <span>Built-in QSDM UPnP client is available.</span>
              </div>
            ) : (
              <div className="flex items-center gap-1">
                <NotificationStatusIndicator
                  notificationType="INFO"
                  isRead={false}
                />
                <span>UPnP unavailable. Network Tunneling will be used.</span>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
