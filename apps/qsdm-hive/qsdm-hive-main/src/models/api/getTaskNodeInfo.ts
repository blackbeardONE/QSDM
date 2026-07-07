export interface GetTaskNodeInfoResponse {
  totalStaked: Record<string, number>;
  pendingRewards: Record<string, number>;
  allTimeRewards: Record<string, number>;
}
