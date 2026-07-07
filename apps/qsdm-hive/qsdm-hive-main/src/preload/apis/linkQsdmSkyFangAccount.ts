import config from 'config';
import {
  QsdmSkyFangLinkCodeRequest,
  QsdmSkyFangLinkResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmSkyFangLinkCodeRequest
): Promise<QsdmSkyFangLinkResponse> =>
  sendMessage(config.endpoints.LINK_QSDM_SKYFANG_ACCOUNT, payload);
