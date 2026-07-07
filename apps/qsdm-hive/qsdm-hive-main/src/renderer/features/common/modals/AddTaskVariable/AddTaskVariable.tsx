import { create, useModal } from '@ebay/nice-modal-react';
import React from 'react';
import { useQueryClient } from 'react-query';

import { TaskVariableDataWithId } from 'models';
import { Button, ErrorMessage } from 'renderer/components/ui';
import { useCloseWithEsc } from 'renderer/features/common/hooks/useCloseWithEsc';
import { useTaskVariable } from 'renderer/features/common/hooks/useTaskVariable';
import { Modal, ModalContent } from 'renderer/features/modals';
import { QueryKeys } from 'renderer/services';
import {
  CloseIcon,
  SettingsIcon,
} from 'renderer/features/settings/components/TaskSettings/TaskExtensionIcons';
import { Theme } from 'renderer/types/common';

const baseInputClassName =
  'px-6 py-2 text-sm rounded-md bg-finnieBlue-light-tertiary focus:ring-2 focus:ring-finnieTeal focus:outline-none focus:bg-finnieBlue-light-secondary';

interface Props {
  presetLabel: string;
}

export const AddTaskVariable = create(function AddTaskVariable({
  presetLabel,
}: Props) {
  const modal = useModal();

  const queryClient = useQueryClient();

  const onAddTaskVariableSucces = async () => {
    await queryClient.invalidateQueries([QueryKeys.StoredTaskVariables]);
    modal.resolve(true);
    modal.remove();
  };

  const {
    handleAddTaskVariable,
    handleLabelChange,
    handleToolKeyChange,
    label,
    labelError,
    value,
    errorStoringTaskVariable,
    storingTaskVariable,
  } = useTaskVariable({
    onSuccess: onAddTaskVariableSucces,
    taskVariable: { label: presetLabel } as TaskVariableDataWithId,
  });

  const closeModal = () => {
    modal.resolve(false);
    modal.remove();
  };

  useCloseWithEsc({ closeModal });

  return (
    <Modal>
      <ModalContent
        theme={Theme.Dark}
        className="text-left p-5 pl-10 w-max h-fit rounded text-white flex flex-col gap-4 min-w-[740px]"
      >
        <div className="flex items-center justify-center w-full gap-4 pt-2 text-2xl font-semibold">
          <SettingsIcon className="w-8 h-8" />
          <span>Add a Task Extension</span>
          <button
            type="button"
            aria-label="Close"
            className="w-8 h-8 ml-auto cursor-pointer hover:text-finnieTeal"
            onClick={closeModal}
          >
            <CloseIcon className="w-8 h-8" />
          </button>
        </div>

        <p className="mr-12">
          Add a local value QSDM Hive can pass to QSDM Core tasks.
        </p>
        <div className="flex flex-col mb-2">
          <label htmlFor="setting-label" className="mb-0.5 text-left">
            LABEL
          </label>
          <input
            id="setting-label"
            className={`${baseInputClassName} w-56`}
            type="text"
            value={label}
            onChange={handleLabelChange}
            placeholder="Add Label"
          />
          <div className="h-12 -mt-2 -mb-10">
            {labelError && (
              <ErrorMessage error={labelError} className="text-xs" />
            )}
          </div>
        </div>

        <div className="flex flex-col mb-2">
          <label htmlFor="setting-input" className="mb-0.5 text-left">
            SECRET OR VALUE
          </label>
          <input
            id="setting-input"
            className={`${baseInputClassName} w-full`}
            type="password"
            value={value}
            onChange={handleToolKeyChange}
            placeholder="Paste token, API key, or task value"
            autoComplete="off"
          />
        </div>

        <div className="h-6 -mt-4 -mb-3 text-center">
          {errorStoringTaskVariable && (
            <ErrorMessage
              error={errorStoringTaskVariable}
              className="text-xs"
            />
          )}
        </div>

        <Button
          label="Save Settings"
          onClick={() => handleAddTaskVariable()}
          disabled={!!labelError || !label || !value}
          className="w-56 h-12 m-auto font-semibold bg-finnieGray-tertiary text-finnieBlue-light"
          loading={storingTaskVariable}
        />
      </ModalContent>
    </Modal>
  );
});
