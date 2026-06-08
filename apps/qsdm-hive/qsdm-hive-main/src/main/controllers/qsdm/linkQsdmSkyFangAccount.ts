import { Event } from 'electron';

import { linkSkyFangWalletByCode } from 'main/services/skyFangWalletLink';
import {
  QsdmSkyFangLinkCodeRequest,
  QsdmSkyFangLinkResponse,
} from 'models/api/qsdm';

export const linkQsdmSkyFangAccount = async (
  _event: Event,
  payload: QsdmSkyFangLinkCodeRequest
): Promise<QsdmSkyFangLinkResponse> => linkSkyFangWalletByCode(payload);
