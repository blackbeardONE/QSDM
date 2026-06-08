import {
  useMainAccountBalance,
  useStakingAccountBalance,
} from 'renderer/features/settings';

export function useCellToken() {
  const { accountBalance: mainAccountBalance } = useMainAccountBalance();
  const { accountBalance: stakingAccountBalance } = useStakingAccountBalance();
}
