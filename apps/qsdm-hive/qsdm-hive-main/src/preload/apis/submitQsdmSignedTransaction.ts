import config from 'config';
import {
  QsdmSignedTransactionEnvelope,
  QsdmSubmitSignedTransactionResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  envelope: QsdmSignedTransactionEnvelope
): Promise<QsdmSubmitSignedTransactionResponse> =>
  sendMessage(config.endpoints.SUBMIT_QSDM_SIGNED_TRANSACTION, envelope);
