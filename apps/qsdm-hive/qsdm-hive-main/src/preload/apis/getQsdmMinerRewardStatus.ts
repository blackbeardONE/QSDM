import config from 'config';
import { QsdmMinerRewardStatusResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmMinerRewardStatusResponse> =>
  sendMessage(config.endpoints.GET_QSDM_MINER_REWARD_STATUS, undefined);
