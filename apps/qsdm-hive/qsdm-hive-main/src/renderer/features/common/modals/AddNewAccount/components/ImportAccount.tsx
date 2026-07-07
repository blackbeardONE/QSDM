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
          QSDM CELL wallets are recovered with a QSDM keystore JSON file plus
          passphrase. This account panel only creates a local Hive profile for
          this device.
        </div>

        {!hideQsdmSignerImport && (
          <AddAccountAction
            onClick={() => setNextStep(Steps.ImportQsdmWallet)}
            ref={ref}
            title="Import QSDM signer wallet"
            description="Import a QSDM keystore JSON file and passphrase for signed CELL actions."
            icon={<Icon source={UploadLine} className="h-8 w-8" />}
          />
        )}
        <AddAccountAction
          onClick={() => setNextStep(Steps.CreateNewKey)}
          ref={hideQsdmSignerImport ? ref : undefined}
          title="Create Hive profile"
          description="Create a fresh local profile for Hive. Back up the QSDM keystore and passphrase for CELL wallet recovery."
          icon={<Icon source={AddLine} className="h-8 w-8" />}
        />
      </div>
    </ModalContent>
  );
}

export default memo(ImportAccount);
