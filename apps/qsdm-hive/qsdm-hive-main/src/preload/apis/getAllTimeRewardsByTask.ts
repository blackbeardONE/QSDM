import config from 'config';
import { GetAllTimeRewardsParam, CellBaseUnits } from 'models/api';
import sendMessage from 'preload/sendMessage';

export default (payload: GetAllTimeRewardsParam): Promise<CellBaseUnits> =>
  sendMessage(config.endpoints.GET_ALL_TIME_REWARDS_BY_TASK, payload);
