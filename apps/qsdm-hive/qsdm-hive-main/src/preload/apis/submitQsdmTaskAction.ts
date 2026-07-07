import config from 'config';
import {
  QsdmTaskActionEnvelope,
  QsdmTaskActionSubmitResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  envelope: QsdmTaskActionEnvelope
): Promise<QsdmTaskActionSubmitResponse> =>
  sendMessage(config.endpoints.SUBMIT_QSDM_TASK_ACTION, envelope);
