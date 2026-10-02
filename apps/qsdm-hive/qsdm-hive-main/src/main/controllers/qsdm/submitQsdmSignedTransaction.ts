import axios from 'axios';
import { Event } from 'electron';

import { assertQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import {
  QsdmSignedTransactionEnvelope,
  QsdmSubmitSignedTransactionResponse,
} from 'models/api/qsdm';

export const submitQsdmSignedTransaction = async (
  _: Event,
  envelope: QsdmSignedTransactionEnvelope
): Promise<QsdmSubmitSignedTransactionResponse> => {
  const safety = await assertQsdmCanonicalChainSafety({ forceRefresh: true });
  const verifiedApiUrl = safety.effectiveApiUrl.replace(/\/+$/, '');
  const response = await axios.post<QsdmSubmitSignedTransactionResponse>(
    `${verifiedApiUrl}/wallet/submit-signed`,
    envelope,
    { timeout: 10000 }
  );

  return response.data;
};
