import { Event } from 'electron';

import {
  disconnectQsdmMotherHiveRelay,
  pairQsdmMotherHiveRelay,
} from 'main/services/qsdmMotherHiveRelayConfig';
import { getQsdmMotherHiveStatus } from 'main/services/qsdmSystemTasks';
import {
  QsdmMotherHivePairRequest,
  QsdmMotherHiveStatusResponse,
} from 'models/api/qsdm';

export const pairQsdmMotherHive = async (
  _event: Event,
  payload: QsdmMotherHivePairRequest
): Promise<QsdmMotherHiveStatusResponse> => {
  pairQsdmMotherHiveRelay(payload.pairingCode);
  return getQsdmMotherHiveStatus();
};

export const disconnectQsdmMotherHive =
  async (): Promise<QsdmMotherHiveStatusResponse> => {
    disconnectQsdmMotherHiveRelay();
    return getQsdmMotherHiveStatus();
  };
