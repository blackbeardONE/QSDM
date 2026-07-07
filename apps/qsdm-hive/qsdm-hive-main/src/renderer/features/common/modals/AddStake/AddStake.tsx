import { create, useModal } from '@ebay/nice-modal-react';
import React, { useMemo, useState } from 'react';
import { useQueryClient } from 'react-query';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { KPLBalanceResponse, TaskMetadata } from 'models';
import { Button, ErrorMessage, LoadingSpinner } from 'renderer/components/ui';
import { QsdmHiveInput } from 'renderer/components/ui/QsdmHiveInput';
import { useCloseWithEsc } from 'renderer/features/common/hooks/useCloseWithEsc';
import { Modal, ModalContent, ModalTopBar } from 'renderer/features/modals';
import { useMainAccount } from 'renderer/features/settings/hooks';
import { useStakeOnTask } from 'renderer/features/tasks';
import { useMyTaskStake } from 'renderer/features/tasks/hooks/useMyTaskStake';
import { useTaskWalletBalance } from 'renderer/features/tasks/hooks/useTaskWalletBalance';
import { isNetworkingTask } from 'renderer/features/tasks/utils';
import { QueryKeys, startTask } from 'renderer/services';
import { Task } from 'renderer/types';
import { Theme } from 'renderer/types/common';
import { getCellFromBaseUnits, getBaseUnitsFromCell } from 'utils';

type PropsType = {
  task: Task;
  metadata?: TaskMetadata | null;
  isPrivate?: boolean;
  kplToken?: any;
};

export const AddStake = create<PropsType>(function AddStake({
  task,
  metadata,
  isPrivate,
  kplToken,
}) {
  const { publicKey, minimumStakeAmount: minStake, isRunning } = task;

  const modal = useModal();

  const [stakeAmount, setStakeAmount] = useState<number>(
    getCellFromBaseUnits(minStake)
  );
  const [error, setError] = useState('');
  const isUsingNetworking = useMemo(
    () => isNetworkingTask(metadata),
    [metadata]
  );

  const stakeOnTaskMutation = useStakeOnTask();

  const stakeAmountInMinUnit =
    task.taskType === 'KPL'
      ? (stakeAmount || 0) * 10 ** (kplToken?.decimals || 9)
      : getBaseUnitsFromCell(stakeAmount || 0);

  const stakeAndRun = async () => {
    try {
      await stakeOnTaskMutation.mutateAsync({
        taskAccountPubKey: publicKey,
        stakeAmount: stakeAmountInMinUnit,
        isNetworkingTask: isUsingNetworking,
        stakePotAccount: task.stakePotAccount,
        taskType: task.taskType,
        mintAddress: task.tokenType,
      });

      if (!isRunning) {
        startTask(publicKey, !!isPrivate);
      }
      await queryCache.invalidateQueries([QueryKeys.TaskList]);
      handleClose();
    } catch (error) {
      console.error('Error staking and running task:', error);
    }
  };

  const {
    accountBalance: taskWalletBalance,
    accountBalanceLoadingError,
    loadingAccountBalance,
  } = useTaskWalletBalance();
  const mainAccountBalance = taskWalletBalance ?? 0;
  const walletBalanceError = accountBalanceLoadingError
    ? accountBalanceLoadingError instanceof Error
      ? accountBalanceLoadingError
      : String(accountBalanceLoadingError)
    : null;
  const { data: mainAccountPubKey } = useMainAccount();

  const queryCache = useQueryClient();

  const kplBalanceListQueryData: KPLBalanceResponse[] =
    queryCache.getQueryData([QueryKeys.KplBalanceList, mainAccountPubKey]) ||
    [];
  const kplBalance =
    kplBalanceListQueryData?.find((balance) => balance.mint === task.tokenType)
      ?.balance || 0;
  const kplBalanceToDisplay = kplBalance / 10 ** (kplToken?.decimals || 9);

  const handleClose = () => {
    if (!stakeOnTaskMutation.isLoading) {
      modal.resolve(true);
      modal.remove();
    }
  };

  useCloseWithEsc({ closeModal: handleClose });

  const { data: alreadyStakedTokensAmount = 0 } = useMyTaskStake(
    task.publicKey,
    task.taskType,
    true
  );

  const mainAccountBalanceInCell = getCellFromBaseUnits(mainAccountBalance);
  const balanceToDisplay =
    task.taskType === 'KPL' ? kplBalanceToDisplay : mainAccountBalanceInCell;
  const minStakeToDisplay =
    task.taskType === 'KPL'
      ? minStake / 10 ** (kplToken?.decimals || 9)
      : getCellFromBaseUnits(minStake);
  const tokenSymbol =
    task.taskType === 'KPL' ? kplToken?.symbol : NATIVE_TOKEN_SYMBOL;

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setError('');
    const stakeToAdd = +e.target.value;
    const stakeToAddInBaseUnits = getBaseUnitsFromCell(stakeToAdd);
    const meetsMinStake =
      stakeToAddInBaseUnits + alreadyStakedTokensAmount >= minStake;
    if (!meetsMinStake) {
      setError(`Min stake: ${minStakeToDisplay} ${tokenSymbol}`);
    } else if (stakeToAdd > balanceToDisplay) {
      setError('Not enough balance');
    }
    setStakeAmount(stakeToAdd);
  };

  const title = stakeOnTaskMutation.isLoading
    ? "Sit tight, we're adding your treasure to the pile."
    : 'Enter the amount you want to add to your stake.';

  const buttonLabel = 'Add Stake';
  const isButtonDisabled =
    !!error ||
    !stakeAmount ||
    !metadata ||
    loadingAccountBalance ||
    !!accountBalanceLoadingError ||
    taskWalletBalance === undefined;

  return (
    <Modal>
      <ModalContent className="w-[600px]" theme={Theme.Dark}>
        <ModalTopBar
          title="Add Stake"
          onClose={handleClose}
          theme="dark"
          closeButtonClasses={
            stakeOnTaskMutation.isLoading
              ? '!text-gray-500 cursor-not-allowed'
              : ''
          }
          closeButtonTooltipContent={
            stakeOnTaskMutation.isLoading
              ? 'The window will close automatically once the operation is complete.'
              : undefined
          }
        />

        <div className="flex flex-col items-center justify-center h-64 gap-5 py-8 text-white">
          <div>{title}</div>
          <div>
            <QsdmHiveInput
              onInputChange={handleInputChange}
              symbol={tokenSymbol}
              initialValue={minStakeToDisplay}
              value={stakeAmount}
            />
            <div className="h-12 -mt-2 -mb-10">
              {(error || walletBalanceError || stakeOnTaskMutation.error) && (
                <ErrorMessage
                  error={
                    error || walletBalanceError || stakeOnTaskMutation.error
                  }
                />
              )}
            </div>
          </div>
          <div className="py-2 text-xs text-finnieTeal-100">{`${balanceToDisplay} ${tokenSymbol} available in your balance`}</div>
          <div className="h-12 mt-3">
            {stakeOnTaskMutation.isLoading ? (
              <LoadingSpinner className="w-10 h-10 mx-auto" />
            ) : (
              <Button
                label={buttonLabel}
                onClick={stakeAndRun}
                disabled={isButtonDisabled || stakeOnTaskMutation.isLoading}
                className="py-5 text-finnieBlue-dark bg-finnieGray-100"
              />
            )}
          </div>
        </div>
      </ModalContent>
    </Modal>
  );
});
