import React from 'react';

import { TaskVariableDataWithId } from 'models/api';
import {
  useDeleteTaskVariable,
  useEditTaskVariable,
  useInspectTaskVariable,
} from 'renderer/features/common/hooks';

import { DeleteIcon, EditIcon, ViewIcon } from './TaskExtensionIcons';

interface Props {
  taskVariable: TaskVariableDataWithId;
}

export function TaskVariableItem({ taskVariable }: Props) {
  const { label } = taskVariable;
  const { showModal: showDeleteModal } = useDeleteTaskVariable(taskVariable);
  const { showModal: showEditModal } = useEditTaskVariable(taskVariable);
  const { showModal: showInspectModal } = useInspectTaskVariable(taskVariable);
  const SPHERON_STORAGE = 'Spheron_Storage';

  const isNotDeletable = label === SPHERON_STORAGE;

  return (
    <div className="flex items-center">
      <div className="px-6 py-2 mr-6 text-sm rounded-md bg-finnieBlue-light-tertiary w-80">
        {label}
      </div>

      <button
        type="button"
        className="mx-2 text-white cursor-pointer hover:text-finnieTeal"
        onClick={showInspectModal}
        data-testid="inspect-task-variable"
        aria-label={`Inspect ${label}`}
      >
        <ViewIcon className="w-4 h-4" />
      </button>
      <button
        type="button"
        className="mx-2 text-white cursor-pointer hover:text-finnieTeal"
        onClick={showEditModal}
        data-testid="edit-task-variable"
        aria-label={`Edit ${label}`}
      >
        <EditIcon className="w-4 h-4" />
      </button>

      {!isNotDeletable && (
        <button
          type="button"
          className="mx-2 cursor-pointer text-finnieRed hover:text-red-300"
          onClick={showDeleteModal}
          data-testid="delete-task-variable"
          aria-label={`Delete ${label}`}
        >
          <DeleteIcon className="w-5 h-5" />
        </button>
      )}
    </div>
  );
}
