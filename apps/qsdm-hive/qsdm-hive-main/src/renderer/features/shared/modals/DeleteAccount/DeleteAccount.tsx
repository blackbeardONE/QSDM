import { create, useModal } from '@ebay/nice-modal-react';
import React from 'react';

import { Button } from 'renderer/components/ui';
import { useCloseWithEsc } from 'renderer/features/common/hooks/useCloseWithEsc';
import { Modal, ModalContent, ModalTopBar } from 'renderer/features/modals';

export interface Props {
  accountName: string;
}

export const DeleteAccount = create<Props>(function DeleteAccount({
  accountName,
}) {
  const modal = useModal();

  const handleClose = () => {
    modal.resolve();
    modal.remove();
  };

  const handleConfirm = () => {
    modal.resolve(true);
    modal.remove();
  };

  useCloseWithEsc({ closeModal: handleClose });

  return (
    <Modal>
      <ModalContent className="w-fit h-fit text-finnieBlue rounded-md">
        <ModalTopBar
          title="Delete Account"
          onClose={handleClose}
          titleClasses="text-finnieBlue"
        />
        <div className="flex justify-center px-4 py-6">
          <div>
            <p>
              Are you sure you want to delete{' '}
              <span className="text-lg text-green-dark">{accountName}</span>?
            </p>
            <br />
            This will erase this local Hive profile from the Node. <br /> You
            can restore the local profile with its Hive recovery phrase, but
            QSDM CELL signing requires the separate QSDM keystore JSON and
            passphrase.
          </div>
        </div>
        <div className="px-16 pb-5">
          <div className="flex items-center justify-between gap-20">
            <Button
              label="Delete"
              onClick={handleConfirm}
              variant="danger"
              className="bg-finnieRed text-finnieBlue-light-secondary rounded-md"
            />
            <Button
              label="Cancel"
              onClick={handleClose}
              className="text-white rounded-md"
            />
          </div>
        </div>
      </ModalContent>
    </Modal>
  );
});
