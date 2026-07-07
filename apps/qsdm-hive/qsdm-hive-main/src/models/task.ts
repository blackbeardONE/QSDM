import { PublicKey } from 'vendor/qsdm-chain/web3';

interface AuditTriggerState {
  trigger_by: PublicKey;
  slot: number;
  votes: Array<{ is_valid: boolean; voter: PublicKey; slot: number }>;
}
export type Round = string;
export type PublicKeyString = string;
export type StakingKeyString = string;

export type Submission = {
  submission_value: string;
  slot: number;
  reward_amount?: number;
  claimed?: boolean;
  claimed_at?: string;
};

export type SubmissionsPerRound = Record<
  Round,
  Record<PublicKeyString, Submission>
>;

export type AvailableBalances = Record<string, number>;
export type StakeList = Record<StakingKeyString, number>;

export type TaskType = 'CELL' | 'KPL';

export interface QsdmTaskRuntimeManifest {
  kind: 'capability' | 'wasm';
  capability?: string;
  module_url?: string;
  module_sha256?: string;
  abi?: string;
  min_hive_version?: string;
  max_memory_mb?: number;
  max_runtime_seconds?: number;
}

export interface QsdmTaskManifest {
  schema_version: number;
  task_id: string;
  version: number;
  name: string;
  description?: string;
  manager: string;
  active: boolean;
  runtime: QsdmTaskRuntimeManifest;
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

export interface RawTaskData {
  task_id: string;
  is_running?: boolean;

  task_name: string;
  task_manager: PublicKey;
  is_allowlisted: boolean;
  is_active: boolean;
  task_audit_program: string;
  stake_pot_account: PublicKey;
  total_bounty_amount: number;
  bounty_amount_per_round: number;
  current_round: number;
  available_balances: AvailableBalances;
  stake_list: StakeList;
  task_metadata: string;

  task_description: string;
  submissions: SubmissionsPerRound;
  submissions_audit_trigger: Record<string, Record<string, AuditTriggerState>>;
  total_stake_amount: number;
  reward_pool_amount?: number;
  pending_reward_amount?: number;
  total_reward_paid_amount?: number;
  minimum_stake_amount: number;
  ip_address_list: Record<string, string>;
  round_time: number;
  starting_slot: number;
  audit_window: number;
  submission_window: number;
  task_executable_network: 'IPFS' | 'ARWEAVE' | 'QSDM-CAPABILITY' | 'QSDM-WASM';
  distribution_rewards_submission: SubmissionsPerRound;
  distributions_audit_trigger: Record<
    string,
    Record<string, AuditTriggerState>
  >;
  distributions_audit_record: Record<
    string,
    'Uninitialized' | 'PayoutSuccessful' | 'PayoutFailed'
  >;
  task_vars: string;
  qsdm_vars: string;
  is_migrated: boolean;
  migrated_to: string;
  allowed_failed_distributions: number;

  token_type?: PublicKey;
  task_type: TaskType;
  manifest?: QsdmTaskManifest;
  catalog_version?: number;
  catalog_paused?: boolean;
  catalog_published_at?: string;
  catalog_updated_at?: string;
}

export interface Task {
  publicKey: string;
  data: TaskData;
}

type CellBaseUnits = number;

export interface TaskMetadata {
  author: string;
  description: string;
  repositoryUrl: string;
  createdAt: number;
  imageUrl: string;
  migrationDescription: string;
  requirementsTags: RequirementTag[];
  infoUrl?: string | undefined;
  tags?: string[];
}

export interface RequirementTag {
  type: RequirementType;
  value?: string;
  description?: string;
  retrievalInfo?: string;
}

export enum RequirementType {
  GLOBAL_VARIABLE = 'GLOBAL_VARIABLE',
  TASK_VARIABLE = 'TASK_VARIABLE',
  CPU = 'CPU',
  RAM = 'RAM',
  STORAGE = 'STORAGE',
  NETWORK = 'NETWORK',
  ARCHITECTURE = 'ARCHITECTURE',
  OS = 'OS',
  ADDON = 'ADDON',
}

export interface TaskData {
  taskName: string;
  taskManager: string;
  isWhitelisted: boolean;
  isActive: boolean;
  taskAuditProgram: string;
  stakePotAccount: string;
  totalBountyAmount: CellBaseUnits;
  bountyAmountPerRound: CellBaseUnits;
  currentRound: number;
  availableBalances: Record<string, CellBaseUnits>;
  stakeList: Record<string, CellBaseUnits>;
  isRunning: boolean;
  hasError: boolean;
  metadataCID: string;
  minimumStakeAmount: CellBaseUnits;
  roundTime: number;
  startingSlot: number;
  submissions: SubmissionsPerRound;
  distributionsAuditTrigger: Record<string, Record<string, AuditTriggerState>>;
  submissionsAuditTrigger: Record<string, Record<string, AuditTriggerState>>;
  isMigrated: boolean;
  migratedTo: string;
  distributionRewardsSubmission: SubmissionsPerRound;
  taskType?: 'CELL' | 'KPL';
  tokenType?: string;
}

export interface TaskRetryData {
  count: number;
  timestamp: number;
  cancelled: boolean;
  timerReference: number | null | undefined;
}

export enum RetrievalInfoActionType {
  GET = 'GET',
  POST = 'POST',
  BROWSER = 'BROWSER',
}

export type RetrievalInfo = {
  url: string;
  actionType: RetrievalInfoActionType;
  params: string[];
};
