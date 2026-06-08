import React, { useState } from 'react';

import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import { Button } from 'renderer/components/ui';

import { SectionHeader } from '../SectionHeader';

import { AccountsTable } from './AccountsTable';
import { QsdmWalletPanel } from './QsdmWalletPanel';

export function Accounts() {
  const isQsdmNative = QSDM_BRIDGE_CONFIG.runtimeMode === 'qsdm-native';
  const [showLegacyProfiles, setShowLegacyProfiles] = useState(!isQsdmNative);

  return (
    <div className="flex flex-col h-full text-white">
      <SectionHeader title="Wallet" />
      {isQsdmNative && <QsdmWalletPanel />}
      {isQsdmNative && (
        <div className="w-[90%] pb-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="text-sm font-semibold text-finnieGray-secondary">
                Advanced legacy Hive profiles
              </div>
              <div className="pt-1 text-xs text-finnieGray-secondary">
                Local profile keys are separate from the QSDM signer wallet.
              </div>
            </div>
            <Button
              label={showLegacyProfiles ? 'Hide Profiles' : 'Show Profiles'}
              onClick={() => setShowLegacyProfiles((value) => !value)}
              className="h-9 w-32 bg-finnieBlue-light-secondary"
            />
          </div>
        </div>
      )}
      {(!isQsdmNative || showLegacyProfiles) && (
        <AccountsTable
          addButtonLabel={
            isQsdmNative ? 'Add legacy Hive profile' : 'Add new account'
          }
          hideQsdmSignerImport={isQsdmNative}
        />
      )}
    </div>
  );
}
