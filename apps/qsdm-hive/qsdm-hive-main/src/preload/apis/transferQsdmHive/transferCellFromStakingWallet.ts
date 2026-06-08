import config from 'config';
import { TransferCellParam } from 'models/api';
import sendMessage from 'preload/sendMessage';

export const transferCellFromStakingWallet = async (
  payload: TransferCellParam
): Promise<void> =>
  sendMessage(config.endpoints.TRANSFER_CELL_FROM_STAKING_WALLET, payload);
