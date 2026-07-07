import React from 'react';
import { useMutation, useQueryClient } from 'react-query';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { CRITICAL_MAIN_ACCOUNT_BALANCE } from 'config/node';
import { DelegateStakeParam, NetworkErrors } from 'models';
import { useMainAccount } from 'renderer/features/settings/hooks';
import { useConfirmModal } from 'renderer/features/shared';
import { QueryKeys } from 'renderer/services';
import { stakeOnTask } from 'renderer/services/api/k2-actions';
import { getCellFromBaseUnits } from 'utils';
import { Icon, WarningTriangleLine } from 'vendor/qsdm-styleguide';

import { useTaskWalletBalance } from './useTaskWalletBalance';

export const StakingErrors = {
  FAILED_TO_LOAD_ACCOUNT_BALANCE: 'Failed to load account balance',
  ACCOUNT_BALANCE_IS_LOADING: 'Account balance is still loading',
  ACCOUNT_BALANCE_NOT_AVAILABLE: 'Account balance is not available',
  STAKING_CANCELLED_BY_USER: 'Staking cancelled by user',
};

interface Params {
  skipIfItIsAlreadyStaked?: boolean;
}

export function useStakeOnTask({ skipIfItIsAlreadyStaked }: Params = {}) {
  const { data: mainAccountPubKey } = useMainAccount();
  const {
    accountBalance: mainAccountBalance,
    accountBalanceLoadingError,
    loadingAccountBalance,
    usesNativeQsdmWallet,
  } = useTaskWalletBalance();
  const { showModal: showLowMainAccountBalanceWarningModal } = useConfirmModal({
    header: 'Low Main Account Balance',
    icon: <Icon source={WarningTriangleLine} className="w-8 h-8" />,
    content: (
      <span className="text-finnieOrange">
        Your main account balance is low. We will use
        <br /> staking account balance to stake on this task. Continue?
      </span>
    ),
  });

  const queryClient = useQueryClient();

  const mutation = useMutation<void, Error, DelegateStakeParam>(
    async ({
      taskAccountPubKey,
      stakeAmount,
      isNetworkingTask,
      stakePotAccount,
      taskType,
      mintAddress,
    }) => {
      if (accountBalanceLoadingError) {
        throw new Error(StakingErrors.FAILED_TO_LOAD_ACCOUNT_BALANCE);
      }

      if (loadingAccountBalance) {
        throw new Error(StakingErrors.ACCOUNT_BALANCE_IS_LOADING);
      }

      if (mainAccountBalance === undefined) {
        throw new Error(StakingErrors.ACCOUNT_BALANCE_NOT_AVAILABLE);
      }

      const requiredNativeBalance =
        taskType === 'KPL'
          ? CRITICAL_MAIN_ACCOUNT_BALANCE
          : stakeAmount + CRITICAL_MAIN_ACCOUNT_BALANCE;
      if (usesNativeQsdmWallet && mainAccountBalance < requiredNativeBalance) {
        throw new Error(
          `Insufficient ${NATIVE_TOKEN_SYMBOL} in the active QSDM signer. Required: ${getCellFromBaseUnits(
            requiredNativeBalance
          )} ${NATIVE_TOKEN_SYMBOL}; available: ${getCellFromBaseUnits(
            mainAccountBalance
          )} ${NATIVE_TOKEN_SYMBOL}.`
        );
      }

      if (
        !usesNativeQsdmWallet &&
        mainAccountBalance < stakeAmount + CRITICAL_MAIN_ACCOUNT_BALANCE &&
        taskType !== 'KPL'
      ) {
        const confirmationResult =
          await showLowMainAccountBalanceWarningModal();
        if (!confirmationResult) {
          throw new Error(StakingErrors.STAKING_CANCELLED_BY_USER);
        }

        // use staking wallet
        await stakeOnTask({
          taskAccountPubKey,
          stakeAmount,
          isNetworkingTask,
          useStakingWallet: true,
          stakePotAccount,
          skipIfItIsAlreadyStaked,
          taskType,
          mintAddress,
        });
      } else {
        await stakeOnTask({
          taskAccountPubKey,
          stakeAmount,
          isNetworkingTask,
          stakePotAccount,
          skipIfItIsAlreadyStaked,
          taskType,
          mintAddress,
        });
      }
      queryClient.invalidateQueries([QueryKeys.TaskStake, taskAccountPubKey]);
      queryClient.invalidateQueries([
        QueryKeys.KplBalanceList,
        mainAccountPubKey,
      ]);
      queryClient.invalidateQueries([QueryKeys.taskNodeInfo]);
      queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
    },
    {
      onError: (error, { taskAccountPubKey }) => {
        console.error('Error while staking:', error);

        const couldNotConfirmTransaction = error.message
          .toLowerCase()
          .includes(NetworkErrors.TRANSACTION_TIMEOUT);

        if (couldNotConfirmTransaction) {
          queryClient.invalidateQueries([
            QueryKeys.TaskStake,
            taskAccountPubKey,
          ]);
          queryClient.invalidateQueries([
            QueryKeys.KplBalanceList,
            mainAccountPubKey,
          ]);
          queryClient.invalidateQueries([QueryKeys.taskNodeInfo]);
        }
      },
    }
  );

  return mutation;
}
