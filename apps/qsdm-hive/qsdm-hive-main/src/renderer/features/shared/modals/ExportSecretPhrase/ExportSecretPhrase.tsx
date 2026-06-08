import { create, useModal } from '@ebay/nice-modal-react';
import React, { useState } from 'react';

import { useCloseWithEsc } from 'renderer/features/common/hooks/useCloseWithEsc';
import { Modal, ModalContent, ModalTopBar } from 'renderer/features/modals';
import { Theme } from 'renderer/types/common';
import { AppRoute } from 'renderer/types/routes';

import { EnterNodePassword } from './components/EnterNodePassword';
import ShowSecretPhrase from './components/ShowSecretPhrase';
import { Steps } from './types';

type PropsType = {
  accountName: string;
  publicKey: string;
};

export const ExportSecretPhrase = create<PropsType>(
  function ExportSecretePhrase({ accountName, publicKey }) {
    const modal = useModal();
    const [currentStep, setCurrentStep] = useState(Steps.EnterNodePassword);
    const [seedPhrase, setSeedPhrase] = useState('');

    const handleClose = () => {
      modal.remove();
    };

    const handleOpenWalletBackup = () => {
      modal.remove();
      window.location.hash = AppRoute.SettingsWallet;
    };

    useCloseWithEsc({ closeModal: handleClose });

    const getCurrentView = (step: Steps) => {
      const views = {
        [Steps.EnterNodePassword]: (
          <EnterNodePassword
            setNextStep={setCurrentStep}
            accountName={accountName}
            publicKey={publicKey}
            setSeedPhrase={setSeedPhrase}
            onOpenWalletBackup={handleOpenWalletBackup}
          />
        ),
        [Steps.ShowSecretPhase]: (
          <ShowSecretPhrase onClose={handleClose} seedPhrase={seedPhrase} />
        ),
      };

      return (
        <Modal>
          <ModalContent
            theme={Theme.Dark}
            className="w-[791px] text-white pb-6"
          >
            <ModalTopBar
              theme="dark"
              title="Reveal Hive Profile Phrase"
              onClose={handleClose}
            />
            {views[step]}
          </ModalContent>
        </Modal>
      );
    };
    return getCurrentView(currentStep);
  }
);
