import React from 'react';

import { Popover } from 'renderer/components/ui/Popover/Popover';
import { useConfirmModal } from 'renderer/features/shared';
import { appRelaunch } from 'renderer/services';
import { Theme } from 'renderer/types/common';

import { useUserAppConfig } from '../../hooks';
import { SwitchWithLoader } from '../GeneralSettings/AutomaticUpdatesSwitch';

export function SwitchForceNetworkTunneling() {
  const { userConfig, userConfigMutation, isMutating } = useUserAppConfig({
    onConfigSaveSuccess() {
      appRelaunch();
    },
  });

  const { showModal } = useConfirmModal({
    header: 'Toggle Network Tunneling',
    content:
      'By toggling Network Tunneling, you will cause the app to restart, \n\nare you sure?',
  });

  const forceNetworkTunneling = userConfig?.forceTunneling;
  const areNetworkingFeaturesEnabled = !!userConfig?.networkingFeaturesEnabled;

  const tooltipContent =
    areNetworkingFeaturesEnabled
      ? 'When enabled, QSDM Hive skips UPnP and exposes networking tasks through a tunnel.'
      : 'Enable Networking before changing the tunneling mode.';

  return (
    <div className="flex flex-col gap-5">
      <Popover tooltipContent={tooltipContent} theme={Theme.Dark}>
        <SwitchWithLoader
          id="force-network-tunneling"
          isChecked={!!forceNetworkTunneling}
          isLoading={isMutating}
          onSwitch={async () => {
            if (!areNetworkingFeaturesEnabled) {
              return;
            }

            const confirm = await showModal();

            if (confirm) {
              const forceTunneling = !forceNetworkTunneling;

              userConfigMutation.mutate({
                settings: {
                  forceTunneling,
                },
              });
            }
          }}
          labels={['OFF', 'ON']}
          disabled={!areNetworkingFeaturesEnabled || isMutating}
        />
      </Popover>
    </div>
  );
}
