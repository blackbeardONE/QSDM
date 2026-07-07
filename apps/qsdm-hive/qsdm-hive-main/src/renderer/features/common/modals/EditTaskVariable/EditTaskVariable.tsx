import { create, useModal } from '@ebay/nice-modal-react';
import React from 'react';

import { TaskVariableDataWithId } from 'models/api';
import { Button, ErrorMessage } from 'renderer/components/ui';
import { useTaskVariable } from 'renderer/features/common/hooks/useTaskVariable';
import { Modal, ModalContent } from 'renderer/features/modals';
import {
  CloseIcon,
  SettingsIcon,
} from 'renderer/features/settings/components/TaskSettings/TaskExtensionIcons';
import { Theme } from 'renderer/types/common';

const baseInputClassName =
  'px-6 py-2 text-sm rounded-md bg-finnieBlue-light-tertiary focus:ring-2 focus:ring-finnieTeal focus:outline-none focus:bg-finnieBlue-light-secondary';

interface Params {
  taskVariable: TaskVariableDataWithId;
}

export const EditTaskVariable = create<Params>(function EditTaskVariable({
  taskVariable,
}) {
  const modal = useModal();

  const {
    handleEditTaskVariable,
    handleLabelChange,
    handleToolKeyChange,
    label,
    labelError,
    value,
    errorEditingTaskVariable,
  } = useTaskVariable({
    onSuccess: modal.remove,
    taskVariable,
  });

  const SPHERON_STORAGE = 'Spheron_Storage';

  return (
    <Modal>
      <ModalContent
        theme={Theme.Dark}
        className="text-left p-5 pl-10 w-max h-fit rounded text-white flex flex-col gap-4 min-w-[740px]"
      >
        <div className="flex items-center justify-center w-full gap-4 pt-2 text-2xl font-semibold">
          <SettingsIcon className="w-8 h-8" />
          <span>Edit a Task Extension</span>
          <button
            type="button"
            aria-label="Close"
            className="w-8 h-8 ml-auto cursor-pointer hover:text-finnieTeal"
            onClick={modal.remove}
          >
            <CloseIcon className="w-8 h-8" />
          </button>
        </div>

        <p className="mr-12">
          Edit the local value QSDM Hive can pass to QSDM Core tasks.
        </p>
        <div className="flex flex-col mb-2">
          <label className="mb-0.5 text-left" htmlFor="label-input">
            LABEL
          </label>
          <input
            className={`${baseInputClassName} w-56 ${
              taskVariable.label === SPHERON_STORAGE ? 'cursor-not-allowed' : ''
            }`}
            type="text"
            value={label}
            id="label-input"
            onChange={handleLabelChange}
            placeholder="Add Label"
            disabled={taskVariable.label === SPHERON_STORAGE}
          />
          <div className="h-12 -mt-2 -mb-10">
            {labelError && (
              <ErrorMessage error={labelError} className="text-xs" />
            )}
          </div>
        </div>

        <div className="flex flex-col mb-2">
          <label htmlFor="key-input" className="mb-0.5 text-left">
            SECRET OR VALUE
          </label>
          <input
            id="key-input"
            className={`${baseInputClassName} w-full`}
            type="password"
            value={value}
            onChange={handleToolKeyChange}
            placeholder="Paste token, API key, or task value"
            autoComplete="off"
          />
        </div>

        <div className="h-6 -mt-4 -mb-3 text-center">
          {errorEditingTaskVariable && (
            <ErrorMessage
              error={errorEditingTaskVariable}
              className="text-xs"
            />
          )}
        </div>

        <Button
          label="Save Settings"
          onClick={handleEditTaskVariable}
          disabled={!!labelError || !label || !value}
          className="w-56 h-12 m-auto font-semibold bg-finnieGray-tertiary text-finnieBlue-light"
        />
      </ModalContent>
    </Modal>
  );
});
