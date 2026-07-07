import React, { useMemo } from 'react';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { sum } from 'lodash';
import { TaskWithStake } from 'renderer/types';
import { getCellFromBaseUnits } from 'utils';

import { TaskRow } from './TaskRow';

type PropsType = {
  selectedTasks: TaskWithStake[];
  tasksFee: number;
  updateStake: (taskPublicKey: string, newStake: number) => void;
  setIsRunButtonDisabled: (isDisabled: boolean) => void;
};

export function SelectedTasksSummary({
  selectedTasks,
  tasksFee,
  updateStake,
  setIsRunButtonDisabled,
}: PropsType) {
  const listEmpty = selectedTasks.length === 0;
  const totalStakedInCELL = useMemo(() => {
    const totalStakedInBaseUnits = sum(selectedTasks.map((task) => task.stake));
    return getCellFromBaseUnits(totalStakedInBaseUnits);
  }, [selectedTasks]);
  const tasksFeeToDisplay = `~ ${tasksFee.toFixed(2)}`;

  return (
    <div className="w-full h-full bg-finnieBlue-light-secondary py-[28px] rounded-md min-h-[330px]">
      <div className="flex flex-row w-full text-lg text-finnieEmerald-light px-[48px]">
        <div className="w-[70%] pl-10">Task</div>
        <div className="w-[30%] pl-8">Stake</div>
      </div>

      <div className="my-4 min-h-[160px]">
        {listEmpty ? (
          <div className="flex flex-col items-center justify-center pt-[60px]">
            {/* eslint-disable-next-line @cspell/spellchecker */}
            You didn&apos;t select any tasks to run.
          </div>
        ) : (
          selectedTasks.map((task) => (
            <TaskRow
              key={task.publicKey}
              task={task}
              updateStake={updateStake}
              setIsRunButtonDisabled={setIsRunButtonDisabled}
            />
          ))
        )}
      </div>

      <div className="flex flex-row w-full text-lg text-finnieEmerald-light px-12">
        <div className="w-[70%]">
          <div className="mb-1 font-semibold text-finnieOrange">Task Fees</div>
          <div className="text-white">
            {tasksFeeToDisplay} {NATIVE_TOKEN_SYMBOL}
          </div>
        </div>
        <div className="w-[30%]">
          <div className="mb-2 font-semibold text-finnieEmerald-light">
            Total {NATIVE_TOKEN_SYMBOL} staked
          </div>
          <div className="text-white">
            {totalStakedInCELL} {NATIVE_TOKEN_SYMBOL}
          </div>
        </div>
      </div>
    </div>
  );
}
