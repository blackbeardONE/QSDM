import React from 'react';

import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { Theme } from 'renderer/types/common';

import { SettingSwitch } from '../MainSettings/SettingSwitch';

export function Network() {
  const isGateway =
    QSDM_BRIDGE_CONFIG.apiUrl === QSDM_BRIDGE_CONFIG.gatewayApiUrl;
  const tooltipContent = `QSDM Hive is using ${
    isGateway ? 'the configured gateway' : 'the local core API'
  }. Change QSDM_HIVE_API_URL in the app environment and restart Hive to switch endpoints.`;

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
