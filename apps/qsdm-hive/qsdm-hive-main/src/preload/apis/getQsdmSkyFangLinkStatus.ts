import config from 'config';
import { QsdmSkyFangLinkStatusResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmSkyFangLinkStatusResponse> =>
  sendMessage(config.endpoints.GET_QSDM_SKYFANG_LINK_STATUS, undefined);
