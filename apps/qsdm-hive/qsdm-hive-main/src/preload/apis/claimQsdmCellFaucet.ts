import config from 'config';
import {
  QsdmCellFaucetClaimRequest,
  QsdmCellFaucetClaimResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload?: QsdmCellFaucetClaimRequest
): Promise<QsdmCellFaucetClaimResponse> =>
  sendMessage(config.endpoints.CLAIM_QSDM_CELL_FAUCET, payload);
