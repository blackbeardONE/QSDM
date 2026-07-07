import config from 'config';
import {
  QsdmReferralRewardPoolStatus,
  QsdmReferralStatusResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export const getQsdmReferralStatus = (
  referred: string
): Promise<QsdmReferralStatusResponse> =>
  sendMessage(config.endpoints.GET_QSDM_REFERRAL_STATUS, { referred });

export const getQsdmReferralRewardPoolStatus =
  (): Promise<QsdmReferralRewardPoolStatus> =>
    sendMessage(
      config.endpoints.GET_QSDM_REFERRAL_REWARD_POOL_STATUS,
      undefined
    );
