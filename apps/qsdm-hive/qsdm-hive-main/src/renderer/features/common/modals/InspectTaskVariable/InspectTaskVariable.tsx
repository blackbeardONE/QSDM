import React from 'react';
import { useQuery } from 'react-query';
import { useNavigate } from 'react-router-dom';

import { Task, TaskVariableDataWithId } from 'models';
import { Button, LoadingSpinner } from 'renderer/components/ui';
import { useStartedTasksPubKeys } from 'renderer/features/tasks';
import { getTasksPairedWithVariable, QueryKeys } from 'renderer/services';
import { CloseIcon, ViewIcon } from 'renderer/features/settings/components/TaskSettings/TaskExtensionIcons';
import { AppRoute } from 'renderer/types/routes';

type PropsType = {
  taskVariable: TaskVariableDataWithId;
  onRemove: () => void;
};

export function InspectTaskVariable({ onRemove, taskVariable }: PropsType) {
  const { label, value, id } = taskVariable;
  const maskedValue = value ? `Hidden (${value.length} characters)` : 'Empty';

  const navigate = useNavigate();

  const handleSeeAllTasks = () => {
    onRemove();
    navigate(AppRoute.MyNode);
  };

  const { data: startedTasksPubkeys, isLoading: loadingStartedTasksPubKeys } =
    useStartedTasksPubKeys();

  const {
    data: tasksPairedWithVariable,
    isLoading: loadingTasksPairedWithVariable,
  } = useQuery<Task[]>(
    [QueryKeys.TasksPairedWithVariable, id],
    () => getTasksPairedWithVariable(id),
    { enabled: !!startedTasksPubkeys }
  );

  const tasksPairedWithVariableWhichStarted = tasksPairedWithVariable?.filter(
    ({ publicKey }) => startedTasksPubkeys?.includes(publicKey)
  );

  const isLoading =
    loadingStartedTasksPubKeys || loadingTasksPairedWithVariable;

  const isTaskUsingThisExtension = !!tasksPairedWithVariableWhichStarted?.length;
  return (
    <>
      <div className="flex items-center justify-center w-full gap-4 pt-2 text-2xl font-semibold">
        <ViewIcon className="w-8 h-8" />
        <span>View Task Extension Info</span>
        <button
          type="button"
          aria-label="Close"
          className="w-8 h-8 ml-auto cursor-pointer hover:text-finnieTeal"
          onClick={onRemove}
        >
          <CloseIcon className="w-8 h-8" />
        </button>
      </div>

      <p className="mt-3 mb-6">
        QSDM Hive stores this extension locally and passes it only to tasks that
        explicitly request the paired variable. The value is masked in this view.
      </p>
      <div className="flex">
        <div className="flex flex-col flex-1 mb-2">
          <span className="mb-0.5 text-left">TOOL LABEL</span>

          <div className="w-56 px-6 py-2 mr-6 overflow-hidden text-sm rounded-md bg-finnieBlue-light-tertiary text-ellipsis whitespace-nowrap">
            {label}
          </div>
        </div>

        <div className="flex flex-col flex-[2] mb-2">
          <span className="mb-0.5 text-left">SECRET OR VALUE</span>

          <div className="px-6 py-2 mr-6 overflow-hidden text-sm rounded-md bg-finnieBlue-light-tertiary text-ellipsis w-125 whitespace-nowrap">
            {maskedValue}
          </div>
        </div>
      </div>

      {isTaskUsingThisExtension && (
        <span className="mb-0.5 text-left">TASKS USING THIS TOOL</span>
      )}

      {isLoading ? (
        <div className="flex justify-center">
          <LoadingSpinner />
        </div>
      ) : (
        <div className="flex flex-wrap gap-x-12">
          {tasksPairedWithVariableWhichStarted?.map(
            ({ publicKey, data: { taskName } }) => (
              <div key={publicKey} className="flex items-center gap-4 w-fit">
                <span className="h-2.5 w-2.5 bg-finnieTeal-100" />
                <span>{taskName}</span>
              </div>
            )
          )}
        </div>
      )}

      <Button
        label="See all Tasks"
        className="w-56 h-12 m-auto font-semibold bg-finnieGray-tertiary text-finnieBlue-light"
        onClick={handleSeeAllTasks}
      />
    </>
  );
}
