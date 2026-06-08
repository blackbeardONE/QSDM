import React from 'react';
import { useMutation, useQueryClient } from 'react-query';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { QSDM_HIVE_INTERNAL_TASK_ID } from 'config/qsdmSystemTasks';
import { Button, ErrorMessage } from 'renderer/components/ui';
import { useMyTaskStake } from 'renderer/features/tasks/hooks/useMyTaskStake';
import { QueryKeys, withdrawStake } from 'renderer/services';

import { CountQsdmHive } from '../CountQsdmHive';
import { InfoBox } from '../InfoBox';

export function InternalStakeInfoBox() {
  const queryClient = useQueryClient();
  const {
    data: internalStake = 0,
    isLoading,
    refetch,
  } = useMyTaskStake(QSDM_HIVE_INTERNAL_TASK_ID, 'CELL', false, true);

  const {
    mutate: recoverStake,
    isLoading: isRecovering,
    isSuccess,
    error,
  } = useMutation(
    () => withdrawStake(QSDM_HIVE_INTERNAL_TASK_ID, 'CELL'),
    {
      onSuccess: async () => {
        await queryClient.invalidateQueries([
          QueryKeys.TaskStake,
          QSDM_HIVE_INTERNAL_TASK_ID,
        ]);
        await queryClient.invalidateQueries([QueryKeys.taskNodeInfo]);
        await queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
        await queryClient.invalidateQueries([QueryKeys.availableTaskList]);
        await queryClient.invalidateQueries([QueryKeys.myTaskList]);
        await refetch();
      },
    }
  );

  if (isLoading || internalStake <= 0) {
    return null;
  }

  return (
    <InfoBox className="flex flex-col justify-center min-h-[168px] xl:p-4 overflow-hidden gap-3">
      <div className="flex flex-col gap-1">
        <span className="text-sm text-green-2">Internal Stake</span>
        <div className="flex flex-col items-start justify-center bg-purple-5 p-2 rounded-md">
          <span className="text-sm">
            <CountQsdmHive
              value={internalStake}
              ticker={NATIVE_TOKEN_SYMBOL}
            />
          </span>
        </div>
      </div>
      <p className="text-xs leading-snug text-white/80">
        Reserved by Hive's signed CELL loop. This is hidden from task totals.
      </p>
      <Button
        label={isSuccess ? 'Recovery Submitted' : 'Recover Stake'}
        className="w-full h-9 bg-white text-purple-3 hover:text-green-2 disabled:text-white/80"
        loading={isRecovering}
        disabled={isRecovering || isSuccess}
        onClick={() => recoverStake()}
      />
      {error ? (
        <ErrorMessage
          error={error as Error}
          className="py-0 text-xs leading-snug"
        />
      ) : null}
    </InfoBox>
  );
}
