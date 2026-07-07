import config from 'config';
import {
  QsdmReferralRegisterResponse,
  QsdmReferralRegistrationRequest,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmReferralRegistrationRequest
): Promise<QsdmReferralRegisterResponse> =>
  sendMessage(config.endpoints.REGISTER_QSDM_REFERRAL, payload);
