import axios from 'axios';
import { Event } from 'electron';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import {
  QsdmTaskActionEnvelope,
  QsdmTaskActionSubmitResponse,
} from 'models/api/qsdm';

export const submitQsdmTaskAction = async (
  _: Event,
  envelope: QsdmTaskActionEnvelope
): Promise<QsdmTaskActionSubmitResponse> => {
  const response = await axios.post<QsdmTaskActionSubmitResponse>(
    buildQsdmCoreApiUrl('/tasks/actions/submit-signed'),
    envelope,
    { timeout: 10000 }
  );

  return response.data;
};
