type TaskBalanceEligibilityInput = {
  taskType?: string;
  nativeBalance: number;
  tokenBalance: number;
  stakeAmount: number;
  existingStake: number;
  nativeFeeReserve: number;
  waiveNativeFeeReserve?: boolean;
};

export const getTaskBalanceEligibility = ({
  taskType,
  nativeBalance,
  tokenBalance,
  stakeAmount,
  existingStake,
  nativeFeeReserve,
  waiveNativeFeeReserve = false,
}: TaskBalanceEligibilityInput) => {
  const isKplTask = taskType === 'KPL';
  const hasExistingStake = existingStake > 0;
  const stakingBalance = isKplTask ? tokenBalance : nativeBalance;
  const effectiveNativeFeeReserve = waiveNativeFeeReserve
    ? 0
    : nativeFeeReserve;
  const requiredNativeBalance =
    isKplTask || hasExistingStake
      ? effectiveNativeFeeReserve
      : stakeAmount + effectiveNativeFeeReserve;

  return {
    isKplTask,
    hasEnoughBalanceForStaking:
      hasExistingStake || stakingBalance >= stakeAmount,
    hasEnoughNativeTokens: nativeBalance >= requiredNativeBalance,
    requiredNativeBalance,
  };
};
