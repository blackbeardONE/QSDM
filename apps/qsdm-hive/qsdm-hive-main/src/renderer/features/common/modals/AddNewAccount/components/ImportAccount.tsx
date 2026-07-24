import { AddLine, Icon, UploadLine } from 'vendor/qsdm-styleguide';
import React, { memo, useEffect, useRef } from 'react';

import { ModalContent, ModalTopBar } from 'renderer/features/modals';
import { Theme } from 'renderer/types/common';

import { Steps } from '../types';

import { AddAccountAction } from './AddAccountAction';

type PropsType = Readonly<{
  onClose: () => void;
  setNextStep: (step: Steps) => void;
  hideQsdmSignerImport?: boolean;
}>;

function ImportAccount({
  onClose,
  setNextStep,
  hideQsdmSignerImport,
}: PropsType) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (ref.current) {
      ref.current.focus();
    }
  });

  return (
    <ModalContent
      theme={Theme.Dark}
      className={
        hideQsdmSignerImport ? 'w-[800px] h-80' : 'w-[800px] h-[430px]'
      }
    >
      <ModalTopBar theme="dark" title="Key Management" onClose={onClose} />
      <div className="flex flex-col items-start gap-2 pt-4 pl-12">
        <div className="text-xl font-semibold text-white">Add New Account</div>
        <div className="w-[680px] py-3 text-sm leading-6 text-finnieTeal-100">
          New QSDM CELL wallets use 24 QSDM Recovery Words. Existing legacy
          wallets still import with their keystore JSON and passphrase. Hive
          profiles are local to this device.
        </div>

        {!hideQsdmSignerImport && (
          <AddAccountAction
            onClick={() => setNextStep(Steps.ImportQsdmWallet)}
            ref={ref}
            title="Import QSDM signer wallet"
            description="Import an existing QSDM keystore JSON file and passphrase. Recovery Words are available in Settings > Wallet."
            icon={<Icon source={UploadLine} className="h-8 w-8" />}
          />
        )}
        <AddAccountAction
          onClick={() => setNextStep(Steps.CreateNewKey)}
          ref={hideQsdmSignerImport ? ref : undefined}
          title="Create Hive profile"
          description="Create a local Hive profile. This profile is separate from your CELL wallet recovery backup."
          icon={<Icon source={AddLine} className="h-8 w-8" />}
        />
      </div>
    </ModalContent>
  );
}

export default memo(ImportAccount);
