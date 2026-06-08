import React, { useState } from 'react';
import { useMutation, useQuery } from 'react-query';

import { Button } from 'renderer/components/ui';
import { NotificationStatusIndicator } from 'renderer/features/notifications/NotificationsCenter/components/NotificationStatusIndicator';
import { QueryKeys } from 'renderer/services';
import {
  checkUPnPbinary,
  fetchAndSaveUPnPBinary,
} from 'renderer/services/api/upnp';

import { Spacer } from '../Spacer';

export function UpnpBinaries() {
  const {
    data: binaryStatus,
    isLoading: isCheckingBinary,
    refetch,
  } = useQuery([QueryKeys.UpnpBinaryExists], () => checkUPnPbinary(), {});
  const [error, setError] = useState('');
  const isBinaryExists = !!binaryStatus?.exists;
  const canDownload = !!binaryStatus?.downloadConfigured;

  // Using useMutation to handle the fetchAndSaveUPnPBinary operation
  const mutation = useMutation(fetchAndSaveUPnPBinary, {
    onSuccess: () => {
      refetch();
      setError('');
    },
    onError: (error) => {
      console.log(error);
      setError(
        (error as { message?: string }).message ||
          'Unable to download the UPnP helper. Use Network Tunneling instead.'
      );
    },
  });

  const downloadDisabled = mutation.isLoading || !canDownload;

  return (
    <div>
      <div className="mb-2 text-sm">
        UPnP is optional. If the helper executable is missing or cannot open a
        port, QSDM Hive falls back to network tunneling.
      </div>

      <Spacer size="md" />

      <div className="flex items-center gap-4">
        <Button
          onClick={() => {
            if (!canDownload) {
              setError(
                'QSDM UPnP binary download is not configured. Enable Network Tunneling instead.'
              );
              return;
            }
            mutation.mutate();
          }}
          label={mutation.isLoading ? 'Downloading...' : 'Download binary'}
          disabled={downloadDisabled}
          loading={mutation.isLoading}
          className="w-44 h-10 font-semibold bg-gray-primary text-purple-5"
        />

        {isCheckingBinary ? (
          'Checking binary...'
        ) : (
          <div>
            {isBinaryExists ? (
              <div className="flex items-center gap-1">
                <NotificationStatusIndicator
                  notificationType="SUCCESS"
                  isRead={false}
                />
                <span>Executable already downloaded.</span>
              </div>
            ) : (
              <div className="flex items-center gap-1">
                <NotificationStatusIndicator
                  notificationType={canDownload ? 'ERROR' : 'INFO'}
                  isRead={false}
                />
                <span>
                  {canDownload
                    ? 'Executable is missing.'
                    : 'Executable is missing. Network Tunneling will be used.'}
                </span>
              </div>
            )}
          </div>
        )}
      </div>
      <div className="py-4 text-sm text-finnieRed">{error}</div>
    </div>
  );
}
