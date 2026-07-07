import React from 'react';

import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { Theme } from 'renderer/types/common';

import { SettingSwitch } from '../MainSettings/SettingSwitch';

export function Network() {
  const isGateway = QSDM_BRIDGE_CONFIG.coreConnectionMode === 'gateway';
  const tooltipContent = isGateway
    ? 'QSDM Hive is securely connected through the configured HTTPS gateway. Linux uses this mode by default.'
    : `QSDM Hive is using ${QSDM_BRIDGE_CONFIG.coreApiUrl}. Set QSDM_CORE_API_URL before launch to override the Core endpoint.`;

  return (
    <Popover tooltipContent={tooltipContent} theme={Theme.Dark}>
      <div className="flex flex-col gap-5">
        <SettingSwitch
          id="qsdm-endpoint"
          isLoading={false}
          isChecked={isGateway}
          onSwitch={() => undefined}
          labels={['LOCAL', 'GATEWAY']}
          isDisabled
        />
      </div>
    </Popover>
  );
}
