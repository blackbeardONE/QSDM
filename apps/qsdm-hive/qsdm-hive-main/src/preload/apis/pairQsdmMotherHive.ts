import config from 'config';
import {
  QsdmMotherHivePairRequest,
  QsdmMotherHiveStatusResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export const pairQsdmMotherHive = (
  payload: QsdmMotherHivePairRequest
): Promise<QsdmMotherHiveStatusResponse> =>
  sendMessage(config.endpoints.PAIR_QSDM_MOTHER_HIVE, payload);

export const disconnectQsdmMotherHive =
  (): Promise<QsdmMotherHiveStatusResponse> =>
    sendMessage(config.endpoints.DISCONNECT_QSDM_MOTHER_HIVE, undefined);
