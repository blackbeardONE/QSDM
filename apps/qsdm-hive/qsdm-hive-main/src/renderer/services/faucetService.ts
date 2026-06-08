import { StatusResponse, ValidationStatus } from 'renderer/types';

export const getFaucetStatus = async (walletAddress: string) => {
  if (!walletAddress) return undefined;

  return {
    discordValidation: ValidationStatus.CLAIMED,
    emailValidation: ValidationStatus.CLAIMED,
    githubValidation: ValidationStatus.CLAIMED,
    twitterValidation: ValidationStatus.CLAIMED,
    walletAddress,
  } satisfies Omit<StatusResponse, 'referral'>;
};

type TriggerRedemptionResponseType = {
  message: string;
};

type TriggerRedemptionParams = {
  stakingWallet: string;
  mainWallet: string;
};

export async function triggerRedemption(
  _params: TriggerRedemptionParams
): Promise<TriggerRedemptionResponseType> {
  const result = await window.main.claimQsdmCellFaucet();
  const granted = Number(result.amount_granted || 0);

  return {
    message:
      result.status === 'already_funded'
        ? `This wallet already has at least ${result.target_balance} CELL.`
        : `Claimed ${granted.toLocaleString()} CELL from the local QSDM faucet.`,
  };
}

export async function getOnboardingTaskIds() {
  const brandingConfig = await window.main.getBrandingConfig();
  const onboardingTaskID = brandingConfig?.onboardingTaskID;

  return onboardingTaskID ? [onboardingTaskID] : [];
}
