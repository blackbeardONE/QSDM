import type { RawTaskData } from '../task';

export interface QsdmCoreStatusResponse {
  apiUrl: string;
  coreApiUrl?: string;
  gatewayApiUrl?: string;
  dashboardUrl: string;
  walletAddress?: string;
  tokenSymbol: string;
  protocolSymbol: string;
  runtimeMode: string;
  healthy: boolean;
  health?: unknown;
  status?: unknown;
  taskRpcHealthy?: boolean;
  taskRpcHealth?: unknown;
  taskRpcStatus?: unknown;
  taskRpcError?: string;
  taskSigner?: QsdmTaskActionSignerStatus;
  error?: string;
  checkedAt: string;
}

export interface QsdmTaskActionSignerStatus {
  mode: string;
  configured: boolean;
  ready: boolean;
  localLoopEnabled: boolean;
  sender?: string;
  cliPath?: string;
  keystorePath?: string;
  passphraseFile?: string;
  checks: {
    sender: boolean;
    cliMode: boolean;
    cliPath: boolean;
    keystore: boolean;
    passphrase: boolean;
  };
  reason?: string;
}

export interface QsdmSignerWalletImportRequest {
  keystoreJson: string;
  passphrase: string;
}

export interface QsdmSignerWalletImportResponse {
  address: string;
  publicKey: string;
  keystorePath: string;
  passphraseFile: string;
}

export interface QsdmSignerWalletBackupResponse {
  exported: boolean;
  address?: string;
  keystoreBackupPath?: string;
  passphraseBackupPath?: string;
}

export interface QsdmWalletBalanceResponse {
  address: string;
  balance: number;
  source?: string;
}

export interface QsdmWalletNonceResponse {
  sender: string;
  nonce: number;
  next: number;
}

export interface QsdmMiningAccountResponse {
  address: string;
  balance: number;
  nonce: number;
  present: boolean;
}

export interface QsdmMinerRewardStatusResponse {
  configured: boolean;
  taskId: string;
  rewardAddress?: string;
  rewardAddressSource?: 'env' | 'miner-config' | 'signer-fallback';
  signerAddress?: string;
  rewardAddressMatchesSigner: boolean;
  configPath?: string;
  balanceCell?: number;
  baselineCell?: number;
  earnedCell?: number;
  earnedDenomination?: number;
  warning?: string;
  error?: string;
  checkedAt: string;
}

export interface QsdmMinerRewardAddressUpdateResponse
  extends QsdmMinerRewardStatusResponse {
  updated: boolean;
  backupPath?: string;
  requiresMinerRestart: boolean;
}

export interface QsdmSkyFangLinkStatusResponse {
  configured: boolean;
  linked: boolean;
  address?: string;
  account?: string;
  username?: string;
  player?: string;
  linkedAt?: string;
  site?: string;
  detail?: string;
  checkedAt: string;
}

export interface QsdmSkyFangLinkCodeRequest {
  code: string;
  baseUrl?: string;
}

export interface QsdmSkyFangLinkResponse {
  ok: boolean;
  code: string;
  address: string;
  publicKey: string;
  account?: string;
  username?: string;
  player?: string;
  linkedAt?: string;
  site?: string;
  checkedAt: string;
}

export interface QsdmCellAccountRequest {
  address?: string;
}

export interface QsdmCellAccountResponse {
  configured: boolean;
  reachable: boolean;
  apiUrl: string;
  coreApiUrl?: string;
  gatewayApiUrl?: string;
  dashboardUrl: string;
  tokenSymbol: string;
  address?: string;
  balance?: number;
  balanceSource?: string;
  nonce?: number;
  nextNonce?: number;
  miningAccount?: QsdmMiningAccountResponse;
  error?: string;
  checkedAt: string;
}

export interface QsdmCellFaucetClaimRequest {
  address?: string;
  amount?: number;
}

export interface QsdmCellFaucetClaimResponse {
  address: string;
  status: 'funded' | 'already_funded' | string;
  amount_granted: number;
  balance_before: number;
  balance_after: number;
  target_balance: number;
  source: string;
  checked_at: string;
}

export interface QsdmSignedTransactionEnvelope {
  id: string;
  sender: string;
  recipient: string;
  amount: number;
  fee: number;
  geotag: string;
  parent_cells: string[];
  timestamp: string;
  signature: string;
  public_key: string;
  nonce?: number;
}

export interface QsdmSubmitSignedTransactionResponse {
  transaction_id: string;
  status: 'accepted' | 'duplicate' | string;
  broadcast?: 'p2p' | 'local-only' | string;
}

export type QsdmTaskAction =
  | 'start'
  | 'stop'
  | 'stake'
  | 'fund'
  | 'unstake'
  | 'submit'
  | 'claim'
  | 'withdraw'
  | 'migrate';

export interface QsdmTaskActionEnvelope {
  id: string;
  sender: string;
  task_id: string;
  action: QsdmTaskAction | string;
  amount?: number;
  payload?: string;
  nonce?: number;
  timestamp: string;
  signature: string;
  public_key: string;
}

export interface QsdmTaskActionSubmitResponse {
  action_id: string;
  status: 'accepted' | 'duplicate' | string;
  sender: string;
  task_id: string;
  action: QsdmTaskAction | string;
  client_nonce?: number;
  last_nonce?: number;
  mempool_submitted?: boolean;
  mempool_status?: 'submitted' | 'duplicate' | 'not_configured' | string;
  mempool_error?: string;
}

export interface QsdmTaskStateParticipant {
  sender: string;
  running: boolean;
  stake: number;
  last_action?: string;
  last_action_id?: string;
  last_action_at?: string;
  submission_count: number;
  claim_count: number;
  pending_reward_amount: number;
  total_reward_claimed_amount: number;
}

export interface QsdmTaskState {
  task_id: string;
  total_stake_amount: number;
  reward_pool_amount: number;
  pending_reward_amount: number;
  total_reward_paid_amount: number;
  running_count: number;
  last_action?: string;
  last_action_id?: string;
  last_action_at?: string;
  participants: Record<string, QsdmTaskStateParticipant>;
  submissions: Record<string, Record<string, unknown>>;
}

export interface QsdmTaskStateResponse {
  runtime: 'qsdm-native' | string;
  configured: boolean;
  source?: string;
  state_root: string;
  task: QsdmTaskState;
}

export interface QsdmSignedCellLoopRequest {
  taskId?: string;
  fundAmount?: number;
  stakeAmount?: number;
  rewardAmount?: number;
  waitSeconds?: number;
  skipFund?: boolean;
}

export interface QsdmSignedCellLoopActionResult {
  action: QsdmTaskAction;
  action_id: string;
  status: string;
  mempool_status?: string;
  mempool_error?: string;
  nonce_before?: number;
  nonce_after?: number;
  balance_after?: number;
}

export interface QsdmSignedCellLoopResponse {
  apiUrl: string;
  taskId: string;
  sender: string;
  actions: QsdmSignedCellLoopActionResult[];
  finalBalance?: number;
  finalNonce?: number;
  taskState?: QsdmTaskState;
}

export interface QsdmTasksListResponse {
  runtime: 'qsdm-native' | string;
  configured: boolean;
  source?: string;
  tasks: RawTaskData[];
}

export interface QsdmTaskResponse {
  runtime: 'qsdm-native' | string;
  configured: boolean;
  source?: string;
  task: RawTaskData;
}
