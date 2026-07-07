import { CellBaseUnits } from './storeAllTimeRewards';

export type GetAllTimeRewardsParam = { taskId: string };
export type GetAllTimeRewardsResponse = Record<string, CellBaseUnits>;
