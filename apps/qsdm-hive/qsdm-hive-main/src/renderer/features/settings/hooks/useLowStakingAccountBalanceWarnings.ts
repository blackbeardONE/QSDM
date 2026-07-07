import { useState, useEffect, useMemo } from 'react';

import { getCellFromBaseUnits } from 'utils';

import { useAccountBalance } from './useAccountBalance';
import { useMainAccount } from './useMainAccount';
import { useStakingAccount } from './useStakingAccount';

const CRITICAL_STAKING_ACCOUNT_BALANCE = 0.99;
const MINIMUM_PUBLIC_BALANCE_TO_TRIGGER = 2.1;
// const LOW_STAKING_ACCOUNT_BALANCE = 5;

export const useLowStakingAccountBalanceWarnings = ({
  showCriticalBalanceNotification,
  isEnabled = true,
}: {
  showCriticalBalanceNotification: () => void;
  isEnabled?: boolean;
}) => {
  const { data: stakingPublicKey } = useStakingAccount();
  const { data: mainAccount } = useMainAccount();

  const { accountBalance: stakingAccountBalance } =
    useAccountBalance(stakingPublicKey);

  const { accountBalance: mainAccountBalance } = useAccountBalance(mainAccount);

  const [previousBalance, setPreviousBalance] = useState<number | null>(null);

  const stakingAccountBalanceInCELL = useMemo(
    () => stakingAccountBalance && getCellFromBaseUnits(stakingAccountBalance),
    [stakingAccountBalance]
  );

  const publicAccountBalanceInCELL = useMemo(
    () => mainAccountBalance && getCellFromBaseUnits(mainAccountBalance),
    [mainAccountBalance]
  );

  const displayStakingAlerts =
    (publicAccountBalanceInCELL ?? 0) > MINIMUM_PUBLIC_BALANCE_TO_TRIGGER;

  useEffect(() => {
    if (!isEnabled || !stakingAccountBalanceInCELL || !displayStakingAlerts)
      return;

    if (stakingAccountBalanceInCELL < CRITICAL_STAKING_ACCOUNT_BALANCE) {
      // show critical balance notification only once for the session
      const criticalBalanceNotificationShown = sessionStorage.getItem(
        'criticalBalanceNotificationShown'
      );

      if (criticalBalanceNotificationShown !== 'true') {
        showCriticalBalanceNotification();
      }

      sessionStorage.setItem('criticalBalanceNotificationShown', 'true');
    }

    /*     else if (
      stakingAccountBalanceInCELL <= LOW_STAKING_ACCOUNT_BALANCE &&
      (previousBalance === null ||
        previousBalance - stakingAccountBalanceInCELL >= 1)
    ) {
      addNotification(
        AppNotification.LowStakingAccountBalance,
        AppNotification.LowStakingAccountBalance,
        NotificationPlacement.TopBar
      );
      setPreviousBalance(stakingAccountBalanceInCELL);
    } */
  }, [
    isEnabled,
    stakingAccountBalanceInCELL,
    previousBalance,
    displayStakingAlerts,
    showCriticalBalanceNotification,
  ]);
};
