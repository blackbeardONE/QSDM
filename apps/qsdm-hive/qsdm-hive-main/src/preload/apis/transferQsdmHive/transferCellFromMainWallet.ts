import config from 'config';
import { TransferCellParam } from 'models/api';
import type { QsdmSubmitSignedTransactionResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export const transferCellFromMainWallet = async (
  payload: TransferCellParam
): Promise<QsdmSubmitSignedTransactionResponse | void> => {
  return sendMessage(config.endpoints.TRANSFER_CELL_FROM_MAIN_WALLET, payload);
};
