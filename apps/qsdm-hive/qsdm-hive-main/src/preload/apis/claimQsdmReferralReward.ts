import config from 'config';
import {
  QsdmReferralClaimRequest,
  QsdmReferralClaimResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload?: QsdmReferralClaimRequest
): Promise<QsdmReferralClaimResponse> =>
  sendMessage(config.endpoints.CLAIM_QSDM_REFERRAL_REWARD, payload);
