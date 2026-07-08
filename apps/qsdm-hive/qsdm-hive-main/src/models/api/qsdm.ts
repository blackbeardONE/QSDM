import type { QsdmCoreConnectionMode } from '../../config/qsdm';
import type { RawTaskData } from '../task';

export type QsdmCanonicalChainState =
  | 'canonical'
  | 'gateway-fallback'
  | 'unsafe'
  | 'unreachable';

export type QsdmCanonicalChainReason =
  | 'canonical-source-unavailable'
  | 'status-unavailable'
  | 'genesis-unavailable'
  | 'genesis-mismatch'
  | 'state-root-mismatch'
  | 'height-lag'
  | 'height-ahead'
  | 'common-block-unavailable'
  | 'common-block-mismatch'
  | 'isolated-node';

export interface QsdmCanonicalChainSafety {
  safe: boolean;
  state: QsdmCanonicalChainState;
  reason?: QsdmCanonicalChainReason;
  detail?: string;
  configuredApiUrl: string;
  effectiveApiUrl: string;
  canonicalApiUrl: string;
  usingGatewayFallback: boolean;
  localTip?: number;
  canonicalTip?: number;
  heightDelta?: number;
  peers?: number;
  commonHeight?: number;
  localBlockHash?: string;
  canonicalBlockHash?: string;
  genesisHash?: string;
  checkedAt: string;
}

export interface QsdmNodeStatusResponse {
  chain_tip?: number;
  peers?: number;
  mining?: {
    min_enroll_stake_dust?: number;
    enrollment_contract?: string;
    signed_enrollment_required?: boolean;
    signed_enrollment_activation_height?: number;
    deferred_bond_from_rewards?: boolean;
    deferred_bond_activation_height?: number;
    deferred_bond_work_difficulty?: number;
  };
  [key: string]: unknown;
}

export interface QsdmCoreStatusResponse {
  apiUrl: string;
  coreApiUrl?: string;
  configuredCoreApiUrl?: string;
  effectiveCoreApiUrl?: string;
  coreConnectionMode?: QsdmCoreConnectionMode;
  configuredCoreConnectionMode?: QsdmCoreConnectionMode;
  gatewayApiUrl?: string;
  canonicalApiUrl?: string;
  canonicalSafety?: QsdmCanonicalChainSafety;
  dashboardUrl: string;
  walletAddress?: string;
  tokenSymbol: string;
  protocolSymbol: string;
  runtimeMode: string;
  healthy: boolean;
  connectionState?: 'online' | 'degraded' | 'offline';
  lastSuccessfulAt?: string;
  consecutiveFailures?: number;
  health?: unknown;
  status?: QsdmNodeStatusResponse;
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

export interface QsdmSignerWalletCreateRequest {
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
  enrollment?: {
    configured: boolean;
    eligible: boolean;
    ready: boolean;
    nodeId?: string;
    phase?: 'active' | 'pending_unbond' | 'revoked';
    requiredStakeCell: number;
    bondedStakeCell?: number;
    bondMode?: 'upfront' | 'mining_rewards';
    bondRemainingCell?: number;
    fullyBonded?: boolean;
    deferredBondAvailable?: boolean;
    balanceCell?: number;
    contract?: string;
    computeBackend?: 'cpu-reference' | 'cuda';
    gpuComputeActive?: boolean;
    tensorCoreForkActive?: boolean;
    tensorCoreForkHeight?: number;
    error?: string;
    gpu?: {
      uuid: string;
      name: string;
      computeCapability: string;
      driverVersion: string;
      cudaVersion: string;
      architecture: string;
    };
  };
}

export interface QsdmMinerRewardAddressUpdateResponse
  extends QsdmMinerRewardStatusResponse {
  updated: boolean;
  backupPath?: string;
  requiresMinerRestart: boolean;
}

export interface QsdmMotherHiveWorkerStatus {
  workerId: string;
  hostname: string;
  online: boolean;
  cpuThreads: number;
  ramMiB: number;
  gpuCount: number;
  gpuMemoryMiB: number;
  completedJobs: number;
  lastSeenAt?: string;
}

export interface QsdmMotherHiveStatusResponse {
  configured: boolean;
  connected: boolean;
  role: 'qsdm-hive-mother';
  relayUrl?: string;
  relayId?: string;
  workers: QsdmMotherHiveWorkerStatus[];
  onlineWorkers: number;
  pooledCpuThreads: number;
  pooledRamMiB: number;
  pooledGpuCount: number;
  pooledGpuMemoryMiB: number;
  activeJobs: number;
  applicationJobs: {
    queued: number;
    leased: number;
    completed: number;
    cancelled: number;
    expired: number;
  };
  computeGateway: {
    endpoint: string;
    tokenFile?: string;
    online: boolean;
    protocol: 'qsdm-compute-gateway/v1';
  };
  verifiedReceipts: {
    cpu: number;
    gpu: number;
    ram: number;
  };
  relayPolicy?: {
    cpuPercent: number;
    gpuPercent: number;
    ramPercent: number;
  };
  revenuePolicy: {
    contributorPercent: number;
    motherHivePercent: number;
    ecosystemPercent: number;
    ecosystemWalletAddress?: string;
    contributorWalletAddress?: string;
    motherHiveWalletAddress?: string;
    relaySettlementId?: string;
    relayPublicKey?: string;
    settlementActive: boolean;
    settlementReason: string;
  };
  workloadMode: 'qsdm-approved-distributed-jobs';
  detail?: string;
  checkedAt: string;
}

export interface QsdmMotherHivePairRequest {
  pairingCode: string;
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
  skyfang_stake_cell?: number;
  skyFangStakeCell?: number;
  in_game_stake_cell?: number;
  inGameStakeCell?: number;
  game_stake_cell?: number;
  gameStakeCell?: number;
  total_game_stake_cell?: number;
  totalGameStakeCell?: number;
  hiveStakeCell?: number;
  totalStakeCell?: number;
  rewardRateCell?: number;
  rewardModel?: string;
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
  status: 'funded' | 'already_funded' | 'already_claimed' | string;
  amount_granted: number;
  balance_before: number;
  balance_after: number;
  target_balance: number;
  source: string;
  treasury_address?: string;
  transaction_id?: string;
  checked_at: string;
}

export interface QsdmReferralRegistrationRequest {
  referrer: string;
  referralCode: string;
  installId?: string;
}

export interface QsdmReferralRegistrationRecord {
  id: string;
  referrer: string;
  referred: string;
  referral_code: string;
  install_id?: string;
  signature: string;
  public_key: string;
  registered_at: string;
  last_activity_at?: string;
}

export interface QsdmReferralRegisterResponse {
  status: 'registered' | 'already_registered' | string;
  registered: boolean;
  registration: QsdmReferralRegistrationRecord;
  message: string;
}

export interface QsdmReferralClaimReceipt {
  tx_id: string;
  referrer: string;
  referred: string;
  amount: number;
  status: 'pending' | 'claimed' | string;
  reason?: string;
  created_at: string;
  completed_at?: string;
}

export interface QsdmReferralStatusResponse {
  registered: boolean;
  qualified: boolean;
  claimable: boolean;
  claimed: boolean;
  registration?: QsdmReferralRegistrationRecord;
  claim?: QsdmReferralClaimReceipt;
  activity_nonce: number;
  min_referred_account_nonce: number;
  message: string;
}

export interface QsdmReferralRewardPoolStatus {
  enabled: boolean;
  funded: boolean;
  claims_enabled?: boolean;
  claimable?: boolean;
  pool_address: string;
  balance: number;
  reward_per_qualified_referral: number;
  min_referred_account_nonce?: number;
  registrations?: number;
  qualified?: number;
  claimed?: number;
  pending_claims?: number;
  ledger_configured?: boolean;
  funding_method?: string;
  message?: string;
}

export interface QsdmReferralClaimRequest {
  referrer?: string;
  referred?: string;
}

export interface QsdmReferralClaimResponse {
  status: 'claimed' | 'already_claimed' | 'pending' | string;
  tx_id?: string;
  referrer: string;
  referred: string;
  amount: number;
  pool_address: string;
  receipt: QsdmReferralClaimReceipt;
  message: string;
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
  | 'migrate'
  | 'catalog-register'
  | 'catalog-update'
  | 'catalog-pause'
  | 'catalog-resume';

export type QsdmTaskCatalogOperation = 'publish' | 'pause' | 'resume';

export interface QsdmTaskCatalogDraft {
  task_id: string;
  name: string;
  description?: string;
  active: boolean;
  runtime: {
    kind: 'capability';
    capability: string;
    min_hive_version?: string;
    max_memory_mb?: number;
    max_runtime_seconds?: number;
  };
  minimum_stake_amount?: number;
  reward_per_round?: number;
  round_time: number;
  submission_window?: number;
  audit_window?: number;
  metadata_url?: string;
  source_url?: string;
  icon_url?: string;
  tags?: string[];
  authorized_relay_ids?: string[];
}

export interface QsdmTaskCatalogManageRequest {
  operation: QsdmTaskCatalogOperation;
  taskId: string;
  draft?: QsdmTaskCatalogDraft;
}

export interface QsdmTaskCatalogManageResponse
  extends QsdmTaskActionSubmitResponse {
  operation: QsdmTaskCatalogOperation;
  catalogAction: Extract<
    QsdmTaskAction,
    'catalog-register' | 'catalog-update' | 'catalog-pause' | 'catalog-resume'
  >;
  catalogVersion?: number;
  created?: boolean;
}

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
  manifest?: RawTaskData['manifest'];
  catalog_paused?: boolean;
  catalog_published_at?: string;
  catalog_updated_at?: string;
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
  catalog_source?: string;
  catalog_state_root?: string;
  tasks: RawTaskData[];
}

export interface QsdmTaskResponse {
  runtime: 'qsdm-native' | string;
  configured: boolean;
  source?: string;
  catalog_source?: string;
  catalog_state_root?: string;
  task: RawTaskData;
}
