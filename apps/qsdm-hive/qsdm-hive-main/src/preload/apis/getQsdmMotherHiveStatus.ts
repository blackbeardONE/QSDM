import config from 'config';
import { QsdmMotherHiveStatusResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmMotherHiveStatusResponse> =>
  sendMessage(config.endpoints.GET_QSDM_MOTHER_HIVE_STATUS, undefined);
