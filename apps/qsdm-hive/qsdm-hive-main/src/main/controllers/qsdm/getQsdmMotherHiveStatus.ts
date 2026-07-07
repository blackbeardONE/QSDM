import { getQsdmMotherHiveStatus as readQsdmMotherHiveStatus } from 'main/services/qsdmSystemTasks';
import { QsdmMotherHiveStatusResponse } from 'models/api/qsdm';

export const getQsdmMotherHiveStatus =
  async (): Promise<QsdmMotherHiveStatusResponse> =>
    readQsdmMotherHiveStatus();
