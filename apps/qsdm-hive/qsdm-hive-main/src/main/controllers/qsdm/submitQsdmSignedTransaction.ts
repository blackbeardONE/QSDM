import axios from 'axios';
import { Event } from 'electron';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import {
  QsdmSignedTransactionEnvelope,
  QsdmSubmitSignedTransactionResponse,
} from 'models/api/qsdm';

export const submitQsdmSignedTransaction = async (
  _: Event,
  envelope: QsdmSignedTransactionEnvelope
): Promise<QsdmSubmitSignedTransactionResponse> => {
  const response = await axios.post<QsdmSubmitSignedTransactionResponse>(
    buildQsdmCoreApiUrl('/wallet/submit-signed'),
    envelope,
    { timeout: 10000 }
  );

  return response.data;
};
