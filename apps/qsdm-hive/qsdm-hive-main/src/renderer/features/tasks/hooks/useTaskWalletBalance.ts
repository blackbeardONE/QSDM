import { useMemo } from 'react';
import { useQuery } from 'react-query';

import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import {
  ACCOUNT_BALANCE_DATA_DEFAULT_STALE_TIME,
  ACCOUNT_BALANCE_DATA_REFETCH_INTERVAL,
} from 'config/refetchIntervals';
import { useAccountBalance } from 'renderer/features/settings/hooks/useAccountBalance';
import { useMainAccount } from 'renderer/features/settings/hooks/useMainAccount';
import { getQsdmCellAccount, QueryKeys } from 'renderer/services';
import { getBaseUnitsFromCell } from 'utils';

export const qsdmCellBalanceToBaseUnits = (balance?: number) =>
  typeof balance === 'number' && Number.isFinite(balance)
    ? Math.round(getBaseUnitsFromCell(balance))
    : undefined;

export const useTaskWalletBalance = () => {
  const usesNativeQsdmWallet = QSDM_TASK_RUNTIME_MODE === 'qsdm-native';
  const { data: mainAccountPublicKey } = useMainAccount();
  const legacyBalance = useAccountBalance(
    usesNativeQsdmWallet ? undefined : mainAccountPublicKey
  );
  const nativeBalanceQuery = useQuery(
    [QueryKeys.QsdmCellAccount, 'task-wallet-balance'],
    () => getQsdmCellAccount(),
    {
      enabled: usesNativeQsdmWallet,
      retry: false,
      staleTime: ACCOUNT_BALANCE_DATA_DEFAULT_STALE_TIME,
      refetchInterval: ACCOUNT_BALANCE_DATA_REFETCH_INTERVAL,
    }
  );

  const nativeBalanceError = useMemo(() => {
    if (!usesNativeQsdmWallet || nativeBalanceQuery.isLoading) {
      return undefined;
    }
    if (nativeBalanceQuery.error) {
      return nativeBalanceQuery.error;
    }
    if (!nativeBalanceQuery.data?.configured) {
      return new Error('The active QSDM signer wallet is not configured');
    }
    if (
      !nativeBalanceQuery.data.reachable ||
      nativeBalanceQuery.data.balance === undefined
    ) {
      return new Error(
        nativeBalanceQuery.data.error ||
          'The active QSDM signer balance is unavailable'
      );
    }
    return undefined;
  }, [
    nativeBalanceQuery.data,
    nativeBalanceQuery.error,
    nativeBalanceQuery.isLoading,
    usesNativeQsdmWallet,
  ]);

  if (usesNativeQsdmWallet) {
    return {
      accountBalance: qsdmCellBalanceToBaseUnits(
        nativeBalanceQuery.data?.balance
      ),
      accountBalanceLoadingError: nativeBalanceError,
      loadingAccountBalance: nativeBalanceQuery.isLoading,
      walletAddress: nativeBalanceQuery.data?.address,
      usesNativeQsdmWallet,
    };
  }

  return {
    ...legacyBalance,
    walletAddress: mainAccountPublicKey,
    usesNativeQsdmWallet,
  };
};
