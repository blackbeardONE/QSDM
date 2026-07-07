import { Event } from 'electron';

import { claimQsdmReferralReward } from 'main/services/qsdmReferrals';
import {
  QsdmReferralClaimRequest,
  QsdmReferralClaimResponse,
} from 'models/api/qsdm';

export const claimQsdmReferralRewardController = async (
  _event: Event,
  payload?: QsdmReferralClaimRequest
): Promise<QsdmReferralClaimResponse> => claimQsdmReferralReward(payload);
