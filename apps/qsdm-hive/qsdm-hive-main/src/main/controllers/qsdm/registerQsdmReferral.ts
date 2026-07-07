import { Event } from 'electron';

import { registerQsdmReferral } from 'main/services/qsdmReferrals';
import {
  QsdmReferralRegisterResponse,
  QsdmReferralRegistrationRequest,
} from 'models/api/qsdm';

export const registerQsdmReferralController = async (
  _event: Event,
  payload: QsdmReferralRegistrationRequest
): Promise<QsdmReferralRegisterResponse> => registerQsdmReferral(payload);
