import config from 'config';
import { QsdmCoreStatusResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmCoreStatusResponse> =>
  sendMessage(config.endpoints.GET_QSDM_CORE_STATUS, undefined);
