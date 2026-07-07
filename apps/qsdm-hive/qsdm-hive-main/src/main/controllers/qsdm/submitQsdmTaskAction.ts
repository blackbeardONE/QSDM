import axios from 'axios';
import { Event } from 'electron';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import { assertQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import {
  QsdmTaskActionEnvelope,
  QsdmTaskActionSubmitResponse,
} from 'models/api/qsdm';

export const submitQsdmTaskAction = async (
  _: Event,
  envelope: QsdmTaskActionEnvelope
): Promise<QsdmTaskActionSubmitResponse> => {
  await assertQsdmCanonicalChainSafety();
  const response = await axios.post<QsdmTaskActionSubmitResponse>(
    buildQsdmCoreApiUrl('/tasks/actions/submit-signed'),
    envelope,
    { timeout: 10000 }
  );

  return response.data;
};
