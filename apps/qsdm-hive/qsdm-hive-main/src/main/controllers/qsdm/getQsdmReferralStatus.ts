import { Event } from 'electron';

import {
  getQsdmReferralRewardPoolStatus,
  getQsdmReferralStatus,
} from 'main/services/qsdmReferrals';
import {
  QsdmReferralRewardPoolStatus,
  QsdmReferralStatusResponse,
} from 'models/api/qsdm';

export const getQsdmReferralStatusController = async (
  _event: Event,
  payload: { referred: string }
): Promise<QsdmReferralStatusResponse> =>
  getQsdmReferralStatus(payload.referred);

export const getQsdmReferralRewardPoolStatusController = async (
  _event: Event
): Promise<QsdmReferralRewardPoolStatus> =>
  getQsdmReferralRewardPoolStatus();
