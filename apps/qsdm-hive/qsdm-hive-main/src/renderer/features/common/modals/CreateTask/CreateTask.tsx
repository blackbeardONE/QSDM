import { create, useModal } from '@ebay/nice-modal-react';
import React from 'react';

import { useCloseWithEsc } from 'renderer/features/common/hooks/useCloseWithEsc';
import { Modal, ModalContent, ModalTopBar } from 'renderer/features/modals';
import { openBrowserWindow } from 'renderer/services';

export const CreateTaskModal = create(function CreateTaskModal() {
  const modal = useModal();

  const handleClose = () => {
    modal.remove();
  };

  useCloseWithEsc({ closeModal: handleClose });

  const linkClassNames =
    'text-finnieTeal-700 font-semibold underline inline-block cursor-pointer';

  const handleOpenQsdmDocsWindow = () =>
    openBrowserWindow('https://qsdm.tech/docs');

  const handleOpenDiscordServerWindow = () =>
    openBrowserWindow('https://qsdm.tech');

  return (
    <Modal>
      <ModalContent>
        <ModalTopBar title="Create New Task" onClose={handleClose} />
        <div className="flex flex-col items-center pt-4 text-finnieBlue tracking-finnieSpacing-wider">
          <div className="text-2xl font-semibold  mb-2.5 leading-8">
            Create your own QSDM Hive tasks
          </div>
          <div className="font-normal w-128 mb-6.25">
            The world&apos;s information is at your fingertips with the power of
            QSDM Hive.
          </div>
          <div className="mb-1 font-semibold leading-7">
            Are you a developer?
          </div>
          <div className="font-normal mb-2.5">
            Head over to the{' '}
            <span className={linkClassNames} onClick={handleOpenQsdmDocsWindow}>
              QSDM Hive docs
            </span>{' '}
            to learn how.
          </div>
          <div className="mb-1 font-semibold leading-7">Need a developer?</div>
          <div className="font-normal w-128">
            Check out our{' '}
            <span
              className={linkClassNames}
              onClick={handleOpenDiscordServerWindow}
            >
              support page
            </span>{' '}
            to find developers who are already familiar with QSDM Hive and creating
            Tasks.
          </div>
        </div>
      </ModalContent>
    </Modal>
  );
});
