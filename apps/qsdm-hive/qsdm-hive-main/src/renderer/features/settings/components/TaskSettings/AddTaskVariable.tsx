import React from 'react';

import { Button, ErrorMessage } from 'renderer/components/ui';
import { useTaskVariable } from 'renderer/features/common/hooks/useTaskVariable';

import { SettingsIcon } from './TaskExtensionIcons';

const baseInputClassName =
  'px-6 py-2 w-[320px] text-sm rounded-md bg-finnieBlue-light-tertiary focus:ring-2 focus:ring-finnieTeal focus:outline-none focus:bg-finnieBlue-light-secondary';

export function AddTaskVariable() {
  const {
    handleAddTaskVariable,
    handleLabelChange,
    handleToolKeyChange,
    label,
    labelError,
    value,
    errorStoringTaskVariable,
  } = useTaskVariable();

  return (
    <div className="flex flex-col gap-4 text-sm w-fit pl-0.5">
      <span className="text-2xl font-semibold text-left">
        Add a Task Extension
      </span>
      <p className="max-w-3xl -mt-2 text-sm text-left text-finnieEmerald-light">
        Store task-specific values locally for QSDM Core tasks. Use clear labels
        like GITHUB_TOKEN, ANTHROPIC_API_KEY, or any variable name requested by a
        task.
      </p>

      <div className="flex flex-wrap items-stretch gap-4">
        <div className="flex flex-col w-full md:w-auto">
          <label htmlFor="toolLabel" className="mb-1 text-left">
            LABEL
          </label>
          <input
            className={`${baseInputClassName}`}
            type="text"
            value={label}
            onChange={handleLabelChange}
            placeholder="Example: ANTHROPIC_API_KEY"
            id="toolLabel"
          />
          <div className="h-12 -mt-2 -mb-10">
            {labelError && (
              <ErrorMessage className="text-xs" error={labelError} />
            )}
          </div>
        </div>

        <div className="flex flex-col w-full md:flex-grow md:w-auto">
          <label htmlFor="toolKey" className="mb-1 text-left">
            SECRET OR VALUE
          </label>
          <input
            id="toolKey"
            className={`${baseInputClassName}`}
            type="password"
            value={value}
            onChange={handleToolKeyChange}
            placeholder="Paste token, API key, or task value"
            autoComplete="off"
          />
          <div className="h-12 -mt-2 -mb-10">
            {errorStoringTaskVariable && (
              <ErrorMessage
                error={errorStoringTaskVariable}
                className="text-xs"
              />
            )}
          </div>
        </div>

        <div className="flex flex-col justify-end">
          <Button
            label="Add"
            icon={<SettingsIcon className="w-5 h-5" />}
            onClick={() => handleAddTaskVariable()}
            disabled={!!labelError || !label || !value}
            className="font-semibold bg-white text-finnieBlue-light text-[14px] leading-[14px] min-w-[200px] h-9 self-end"
          />
        </div>
      </div>
    </div>
  );
}
