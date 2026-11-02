import axios from 'axios';
import { Event } from 'electron';

import { assertQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import {
  QsdmTaskActionEnvelope,
  QsdmTaskActionSubmitResponse,
} from 'models/api/qsdm';

export const submitQsdmTaskAction = async (
  _: Event,
  envelope: QsdmTaskActionEnvelope
): Promise<QsdmTaskActionSubmitResponse> => {
  const safety = await assertQsdmCanonicalChainSafety({ forceRefresh: true });
  const verifiedApiUrl = safety.effectiveApiUrl.replace(/\/+$/, '');
  const response = await axios.post<QsdmTaskActionSubmitResponse>(
    `${verifiedApiUrl}/tasks/actions/submit-signed`,
    envelope,
    { timeout: 10000 }
  );

  return response.data;
};
