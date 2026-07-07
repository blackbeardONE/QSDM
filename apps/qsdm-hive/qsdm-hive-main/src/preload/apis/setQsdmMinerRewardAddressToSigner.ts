import config from 'config';
import { QsdmMinerRewardAddressUpdateResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmMinerRewardAddressUpdateResponse> =>
  sendMessage(
    config.endpoints.SET_QSDM_MINER_REWARD_ADDRESS_TO_SIGNER,
    undefined
  );
