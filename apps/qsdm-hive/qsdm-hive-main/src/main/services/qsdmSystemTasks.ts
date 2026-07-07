import { ChildProcess, execFile, spawn } from 'child_process';
import { createHash, createHmac, randomBytes } from 'crypto';
import { EventEmitter } from 'events';
import fs from 'fs';
import os from 'os';
import path from 'path';

import axios from 'axios';
import cryptoRandomString from 'crypto-random-string';

import {
  buildQsdmCoreApiUrl,
  buildQsdmTaskReadUrls,
  getQsdmCoreConnectionMode,
  getQsdmRuntimeCoreApiUrl,
  QSDM_CANONICAL_API_URL,
  QSDM_CELL_DECIMALS,
} from 'config/qsdm';
import {
  getQsdmEdgeWorkerResource,
  isQsdmEdgeWorkerSystemTaskId,
  isQsdmMinerSystemTaskId,
  isQsdmMotherHiveSystemTaskId,
  isQsdmSkyFangLinkSystemTaskId,
  isQsdmSystemTaskId,
  QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT,
  QSDM_EDGE_WORKER_GPU_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_GPU_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID,
  QSDM_EDGE_WORKER_RAM_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_RAM_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID,
  QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
  QSDM_MINER_MIN_STAKE_AMOUNT,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT,
  QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID,
  QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT,
  QSDM_SKYFANG_LINK_BASE_REWARD_CELL,
  QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
  QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE,
  QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL,
  QSDM_SKYFANG_LINK_REWARD_CELL,
  QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID,
  QSDM_SYSTEM_TASK_IDS,
  QsdmEdgeWorkerResource,
} from 'config/qsdmSystemTasks';
import { getAppDataPath } from 'main/node/helpers/getAppDataPath';
import { qsdmGetFirstJson, qsdmGetJson } from 'main/services/qsdmHttpRead';
import {
  assertQsdmMinerEnrollmentReady,
  prepareQsdmMinerV2Config,
} from 'main/services/qsdmMinerEnrollment';
import {
  getDefaultEdgeRelayTokenFile,
  getDefaultEdgeRelayURL,
} from 'main/services/qsdmMotherHiveRelayConfig';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import { RawTaskData, RequirementType, TaskMetadata } from 'models';
import {
  QsdmMiningAccountResponse,
  QsdmMotherHiveStatusResponse,
  QsdmTaskActionSubmitResponse,
} from 'models/api/qsdm';
import { PublicKey } from 'vendor/qsdm-chain/web3';

export {
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT,
  QSDM_EDGE_WORKER_GPU_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_GPU_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID,
  QSDM_EDGE_WORKER_RAM_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_RAM_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID,
  QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_MINER_MIN_STAKE_AMOUNT,
  QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT,
  QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT,
  QSDM_SKYFANG_LINK_BASE_REWARD_CELL,
  QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
  QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE,
  QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL,
  QSDM_SKYFANG_LINK_REWARD_CELL,
  QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID,
  QSDM_SYSTEM_TASK_IDS,
} from 'config/qsdmSystemTasks';

export {
  getDefaultEdgeRelayTokenFile,
  getDefaultEdgeRelayURL,
} from 'main/services/qsdmMotherHiveRelayConfig';

const QSDM_MINER_MANAGER = new PublicKey('QsdmSystemMinerManager');
const QSDM_MINER_STAKE_POT = new PublicKey('QsdmSystemMinerStakePot');
const QSDM_EDGE_WORKER_MANAGER = new PublicKey('QsdmEdgeWorkerManager');
const QSDM_EDGE_WORKER_STAKE_POT = new PublicKey('QsdmEdgeWorkerStakePot');
const QSDM_EDGE_WORKER_GPU_MANAGER = new PublicKey('QsdmEdgeWorkerGpuManager');
const QSDM_EDGE_WORKER_GPU_STAKE_POT = new PublicKey(
  'QsdmEdgeWorkerGpuStakePot'
);
const QSDM_EDGE_WORKER_RAM_MANAGER = new PublicKey('QsdmEdgeWorkerRamManager');
const QSDM_EDGE_WORKER_RAM_STAKE_POT = new PublicKey(
  'QsdmEdgeWorkerRamStakePot'
);
const QSDM_MOTHER_HIVE_MANAGER = new PublicKey('QsdmMotherHiveManager');
const QSDM_MOTHER_HIVE_STAKE_POT = new PublicKey('QsdmMotherHiveStakePot');
const QSDM_SKYFANG_LINK_MANAGER = new PublicKey('QsdmSkyFangLinkManager');
const QSDM_SKYFANG_LINK_STAKE_POT = new PublicKey('QsdmSkyFangLinkStakePot');
const QSDM_GITHUB_TREE_BASE = 'https://github.com/blackbeardONE/QSDM/tree/main';
const QSDM_GITHUB_BLOB_BASE = 'https://github.com/blackbeardONE/QSDM/blob/main';
const QSDM_HIVE_SYSTEM_TASK_SOURCE_URL = `${QSDM_GITHUB_TREE_BASE}/apps`;
const QSDM_MINER_SOURCE_URL = `${QSDM_GITHUB_TREE_BASE}/QSDM/source/cmd/qsdmminer-console`;
const QSDM_EDGE_WORKER_INFO_URL = 'https://qsdm.tech/docs/#/qsdm-hive';
const QSDM_MINER_INFO_URL = 'https://qsdm.tech/docs/#/miner-quickstart';
const QSDM_SKYFANG_LINK_INFO_URL = 'https://qsdm.tech/docs/#/sky-fang-online';

const EMPTY_AUDIT_TRIGGERS = {};
const EMPTY_SUBMISSIONS = {};
const QSDM_TASK_ACTION_COMMIT_POLL_MS = 2000;
const QSDM_TASK_ACTION_COMMIT_TIMEOUT_MS = 120000;
const QSDM_CELL_DENOMINATION_FACTOR = 10 ** QSDM_CELL_DECIMALS;
const QSDM_CELL_DENOMINATION_NORMALIZATION_LIMIT = 1_000_000;

type QsdmStatusResponse = {
  chain_tip?: number;
};

type QsdmTaskStateResponse = {
  task?: Partial<RawTaskData>;
};

type QsdmNativeTaskClock = {
  round: number;
  slot: number;
  roundTimeBlocks: number;
  startingSlot: number;
};

type EdgeWorkerOutputState = {
  pending: string;
  taskId: string;
  resource: QsdmEdgeWorkerResource;
  task: Pick<
    RawTaskData,
    | 'round_time'
    | 'starting_slot'
    | 'bounty_amount_per_round'
    | 'reward_pool_amount'
  >;
  submittedRounds: Set<number>;
  submitting: boolean;
};

type SkyFangLinkOutputState = {
  pending: string;
  task: Pick<
    RawTaskData,
    | 'round_time'
    | 'starting_slot'
    | 'bounty_amount_per_round'
    | 'reward_pool_amount'
  >;
  submittedRounds: Set<number>;
  submitting: boolean;
};

type SkyFangLinkStatusResponse = {
  ok?: boolean;
  linked?: boolean;
  address?: string;
  linked_at?: string;
  site?: string;
  account?: string;
  username?: string;
  player?: string;
  skyfang_stake_cell?: number;
  in_game_stake_cell?: number;
  game_stake_cell?: number;
  total_game_stake_cell?: number;
  hive_stake_cell?: number;
  total_stake_cell?: number;
};

export type QsdmSkyFangWalletLinkGateResult = {
  ok: boolean;
  sender: string;
  linkedAt?: string;
  site?: string;
  account?: string;
  username?: string;
  player?: string;
  skyFangStakeCell?: number;
  inGameStakeCell?: number;
  gameStakeCell?: number;
  totalGameStakeCell?: number;
  hiveStakeCell?: number;
  totalStakeCell?: number;
  rewardRateCell?: number;
  rewardModel?: string;
  detail?: string;
};

export type QsdmMinerProcessInfo = {
  pid: number;
  executablePath?: string;
  commandLine?: string;
  startTime?: string;
};

export type QsdmMinerRewardAddressInfo = {
  address: string;
  source: 'env' | 'miner-config' | 'signer-fallback';
  signer?: string;
  configPath: string;
};

export type QsdmMinerRewardAddressUpdateResult = QsdmMinerRewardAddressInfo & {
  updated: boolean;
  backupPath?: string;
  requiresMinerRestart: boolean;
};

export const isQsdmMinerSystemTask = (taskId?: string | null) =>
  isQsdmMinerSystemTaskId(taskId);

export const isQsdmEdgeWorkerSystemTask = (taskId?: string | null) =>
  isQsdmEdgeWorkerSystemTaskId(taskId);

export const isQsdmMotherHiveSystemTask = (taskId?: string | null) =>
  isQsdmMotherHiveSystemTaskId(taskId);

export const isQsdmSkyFangLinkSystemTask = (taskId?: string | null) =>
  isQsdmSkyFangLinkSystemTaskId(taskId);

export const isQsdmSystemTask = (taskId?: string | null) =>
  isQsdmSystemTaskId(taskId);

export const createQsdmMinerSystemTask = (
  overrides: Partial<RawTaskData> = {}
): RawTaskData => ({
  task_id: QSDM_MINER_SYSTEM_TASK_ID,
  task_name: 'QSDM Miner',
  task_manager: QSDM_MINER_MANAGER,
  is_allowlisted: true,
  is_active: true,
  task_audit_program: QSDM_MINER_SYSTEM_TASK_ID,
  stake_pot_account: QSDM_MINER_STAKE_POT,
  total_bounty_amount: 0,
  bounty_amount_per_round: 0,
  current_round: 0,
  available_balances: {},
  stake_list: {},
  task_metadata: QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  task_description:
    'Built-in QSDM protocol miner for NVIDIA operators. This Hive release uses the packaged CUDA SHA3 solver on NVIDIA Turing or newer hardware and fails closed if GPU proof computation is unavailable. The protocol bond may be prepaid or filled from mining earnings, so a new miner can begin with 0 CELL. Rewards come from accepted protocol proofs; there is no separate Hive bounty. A Sky Fang account is not required for mining.',
  submissions: EMPTY_SUBMISSIONS,
  submissions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
  total_stake_amount: 0,
  reward_pool_amount: 0,
  pending_reward_amount: 0,
  total_reward_paid_amount: 0,
  ip_address_list: {},
  round_time: 60,
  starting_slot: 0,
  audit_window: 0,
  submission_window: 0,
  task_executable_network: 'ARWEAVE',
  distribution_rewards_submission: EMPTY_SUBMISSIONS,
  distributions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
  distributions_audit_record: {},
  task_vars: JSON.stringify({
    qsdm_system_task: true,
    no_expiry: true,
    reward_source: 'protocol-mining-emission',
    hive_task_bounty: false,
  }),
  qsdm_vars: '{}',
  is_migrated: false,
  migrated_to: '',
  allowed_failed_distributions: 0,
  ...overrides,
  // The validator catalog may lag behind the bundled client during a
  // rollout. Never let an older catalog restore a separate Hive stake for
  // protocol mining; qsdm/enroll/v2 owns the slashable bond.
  minimum_stake_amount: QSDM_MINER_MIN_STAKE_AMOUNT,
  task_type: 'CELL',
});

type QsdmEdgeWorkerTaskDefinition = {
  taskId: string;
  metadataId: string;
  name: string;
  manager: PublicKey;
  stakePot: PublicKey;
  resource: QsdmEdgeWorkerResource;
  description: string;
  rewardPerRound: number;
  rewardPoolTarget: number;
};

const QSDM_EDGE_WORKER_TASK_DEFINITIONS: Record<
  QsdmEdgeWorkerResource,
  QsdmEdgeWorkerTaskDefinition
> = {
  cpu: {
    taskId: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
    metadataId: QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
    name: 'QSDM Edge Worker CPU',
    manager: QSDM_EDGE_WORKER_MANAGER,
    stakePot: QSDM_EDGE_WORKER_STAKE_POT,
    resource: 'cpu',
    description:
      'Share bounded CPU capacity through a QSDM Relay to Mother Hive. Verified relay jobs produce signed receipts; rewards are paid only while the task reward pool is funded.',
    rewardPerRound: QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
    rewardPoolTarget: QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
  },
  gpu: {
    taskId: QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
    metadataId: QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID,
    name: 'QSDM Edge Worker GPU',
    manager: QSDM_EDGE_WORKER_GPU_MANAGER,
    stakePot: QSDM_EDGE_WORKER_GPU_STAKE_POT,
    resource: 'gpu',
    description:
      'Share bounded NVIDIA CUDA capacity with a QSDM edge-compute pool. This is separate from QSDM protocol mining and rewards only verifiable completed GPU jobs from a funded task pool.',
    rewardPerRound: QSDM_EDGE_WORKER_GPU_REWARD_PER_ROUND_CELL,
    rewardPoolTarget: QSDM_EDGE_WORKER_GPU_REWARD_POOL_TARGET_CELL,
  },
  ram: {
    taskId: QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
    metadataId: QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID,
    name: 'QSDM Edge Worker RAM',
    manager: QSDM_EDGE_WORKER_RAM_MANAGER,
    stakePot: QSDM_EDGE_WORKER_RAM_STAKE_POT,
    resource: 'ram',
    description:
      'Share bounded memory through a QSDM Relay to Mother Hive for memory-backed jobs. Verified relay receipts are required and rewards are paid only from a funded task pool.',
    rewardPerRound: QSDM_EDGE_WORKER_RAM_REWARD_PER_ROUND_CELL,
    rewardPoolTarget: QSDM_EDGE_WORKER_RAM_REWARD_POOL_TARGET_CELL,
  },
};

const createQsdmResourceWorkerSystemTask = (
  definition: QsdmEdgeWorkerTaskDefinition,
  overrides: Partial<RawTaskData> = {}
): RawTaskData => {
  const task: RawTaskData = {
    task_id: definition.taskId,
    task_name: definition.name,
    task_manager: definition.manager,
    is_allowlisted: true,
    is_active: true,
    task_audit_program: definition.taskId,
    stake_pot_account: definition.stakePot,
    total_bounty_amount: definition.rewardPoolTarget,
    bounty_amount_per_round: definition.rewardPerRound,
    current_round: 0,
    available_balances: {},
    stake_list: {},
    task_metadata: definition.metadataId,
    task_description: definition.description,
    submissions: EMPTY_SUBMISSIONS,
    submissions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
    total_stake_amount: 0,
    reward_pool_amount: 0,
    pending_reward_amount: 0,
    total_reward_paid_amount: 0,
    minimum_stake_amount: QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT,
    ip_address_list: {},
    round_time: 60,
    starting_slot: 0,
    audit_window: 0,
    submission_window: 0,
    task_executable_network: 'ARWEAVE',
    distribution_rewards_submission: EMPTY_SUBMISSIONS,
    distributions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
    distributions_audit_record: {},
    task_vars: JSON.stringify({
      qsdm_system_task: true,
      no_expiry: true,
      resource_worker: definition.resource,
      cpu_worker: definition.resource === 'cpu',
      pooled_compute: true,
      coordinator_receipts: true,
      relay_receipts: true,
      mother_hive: true,
      reward_source: 'funded-pool',
      reward_per_round_cell: definition.rewardPerRound,
      reward_pool_target_cell: definition.rewardPoolTarget,
    }),
    qsdm_vars: '{}',
    is_migrated: false,
    migrated_to: '',
    allowed_failed_distributions: 0,
    ...overrides,
    task_type: 'CELL',
  };

  return {
    ...task,
    total_bounty_amount:
      Number(task.total_bounty_amount) > 0
        ? task.total_bounty_amount
        : definition.rewardPoolTarget,
    bounty_amount_per_round:
      Number(task.bounty_amount_per_round) > 0
        ? task.bounty_amount_per_round
        : definition.rewardPerRound,
  };
};

export const createQsdmEdgeWorkerSystemTask = (
  overrides: Partial<RawTaskData> = {}
) =>
  createQsdmResourceWorkerSystemTask(
    QSDM_EDGE_WORKER_TASK_DEFINITIONS.cpu,
    overrides
  );

export const createQsdmGPUWorkerSystemTask = (
  overrides: Partial<RawTaskData> = {}
) =>
  createQsdmResourceWorkerSystemTask(
    QSDM_EDGE_WORKER_TASK_DEFINITIONS.gpu,
    overrides
  );

export const createQsdmRAMWorkerSystemTask = (
  overrides: Partial<RawTaskData> = {}
) =>
  createQsdmResourceWorkerSystemTask(
    QSDM_EDGE_WORKER_TASK_DEFINITIONS.ram,
    overrides
  );

export const createQsdmMotherHiveSystemTask = (
  overrides: Partial<RawTaskData> = {}
): RawTaskData => ({
  task_id: QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
  task_name: 'Mother Hive Task',
  task_manager: QSDM_MOTHER_HIVE_MANAGER,
  is_allowlisted: true,
  is_active: true,
  task_audit_program: QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
  stake_pot_account: QSDM_MOTHER_HIVE_STAKE_POT,
  total_bounty_amount: 0,
  bounty_amount_per_round: 0,
  current_round: 0,
  available_balances: {},
  stake_list: {},
  task_metadata: QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID,
  task_description:
    'Run this QSDM Hive in Mother Hive mode for a paired Relay. It inventories and acknowledges pooled CPU, NVIDIA GPU, and RAM for QSDM-approved distributed workloads. Pooled resources are schedulable capacity, not transparent operating-system devices. The target revenue split is 70% contributors, 15% Mother Hive operator, and 15% CELL ecosystem reserve; automatic settlement stays disabled until worker wallets and Relay receipts are chain-verifiable.',
  submissions: EMPTY_SUBMISSIONS,
  submissions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
  total_stake_amount: 0,
  reward_pool_amount: 0,
  pending_reward_amount: 0,
  total_reward_paid_amount: 0,
  minimum_stake_amount: QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT,
  ip_address_list: {},
  round_time: 60,
  starting_slot: 0,
  audit_window: 0,
  submission_window: 0,
  task_executable_network: 'ARWEAVE',
  distribution_rewards_submission: EMPTY_SUBMISSIONS,
  distributions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
  distributions_audit_record: {},
  task_vars: JSON.stringify({
    qsdm_system_task: true,
    no_expiry: true,
    mother_hive_role: true,
    qsdm_hive_only: true,
    pooled_compute_consumer: true,
    workload_mode: 'qsdm-approved-distributed-jobs',
    contributor_share_percent: QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT,
    mother_hive_share_percent: QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT,
    ecosystem_share_percent: QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT,
    settlement_active: false,
    settlement_requirement:
      'wallet-bound workers, chain-verifiable Relay receipts, and funded workload escrow',
  }),
  qsdm_vars: '{}',
  is_migrated: false,
  migrated_to: '',
  allowed_failed_distributions: 0,
  ...overrides,
  task_type: 'CELL',
});

export const createQsdmSkyFangLinkSystemTask = (
  overrides: Partial<RawTaskData> = {}
): RawTaskData => {
  const task: RawTaskData = {
    task_id: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
    task_name: 'QSDM Sky Fang Link',
    task_manager: QSDM_SKYFANG_LINK_MANAGER,
    is_allowlisted: true,
    is_active: true,
    task_audit_program: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
    stake_pot_account: QSDM_SKYFANG_LINK_STAKE_POT,
    total_bounty_amount: QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
    bounty_amount_per_round: QSDM_SKYFANG_LINK_REWARD_CELL,
    current_round: 0,
    available_balances: {},
    stake_list: {},
    task_metadata: QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID,
    task_description:
      'Ongoing QSDM Hive task for proving your active QSDM wallet is linked to Sky Fang. Rewards are stake-weighted from the combined CELL staked in Hive for this task and CELL stake reported by Sky Fang when available.',
    submissions: EMPTY_SUBMISSIONS,
    submissions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
    total_stake_amount: 0,
    reward_pool_amount: 0,
    pending_reward_amount: 0,
    total_reward_paid_amount: 0,
    minimum_stake_amount: QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT,
    ip_address_list: {},
    round_time: 3600,
    starting_slot: 0,
    audit_window: 0,
    submission_window: 0,
    task_executable_network: 'ARWEAVE',
    distribution_rewards_submission: EMPTY_SUBMISSIONS,
    distributions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
    distributions_audit_record: {},
    task_vars: JSON.stringify({
      qsdm_system_task: true,
      no_expiry: true,
      skyfang_wallet_link: true,
      reward_model: 'skyfang-hive-combined-stake-v1',
      reward_source: 'funded-pool',
      base_reward_cell: QSDM_SKYFANG_LINK_BASE_REWARD_CELL,
      hive_stake_reward_rate: QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE,
      skyfang_stake_reward_rate: QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
      max_reward_per_round_cell: QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL,
      reward_pool_target_cell: QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
      skyfang_base_url: 'https://skyfang.xyz',
    }),
    qsdm_vars: '{}',
    is_migrated: false,
    migrated_to: '',
    allowed_failed_distributions: 0,
    ...overrides,
    task_type: 'CELL',
  };

  return {
    ...task,
    total_bounty_amount:
      Number(task.total_bounty_amount) > 0
        ? task.total_bounty_amount
        : QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
    bounty_amount_per_round:
      Number(task.bounty_amount_per_round) > 0
        ? task.bounty_amount_per_round
        : QSDM_SKYFANG_LINK_BASE_REWARD_CELL,
  };
};

export const getQsdmSystemTaskById = (
  taskId: string,
  overrides: Partial<RawTaskData> = {}
): RawTaskData | null => {
  if (isQsdmMinerSystemTask(taskId)) {
    return createQsdmMinerSystemTask(overrides);
  }

  if (isQsdmEdgeWorkerSystemTask(taskId)) {
    const resource = getQsdmEdgeWorkerResource(taskId) || 'cpu';
    return createQsdmResourceWorkerSystemTask(
      QSDM_EDGE_WORKER_TASK_DEFINITIONS[resource],
      overrides
    );
  }

  if (isQsdmMotherHiveSystemTask(taskId)) {
    return createQsdmMotherHiveSystemTask(overrides);
  }

  if (isQsdmSkyFangLinkSystemTask(taskId)) {
    return createQsdmSkyFangLinkSystemTask(overrides);
  }

  return null;
};

export const mergeQsdmSystemTasks = (tasks: RawTaskData[]) => {
  const systemTasksById = tasks.reduce((acc, task) => {
    if (QSDM_SYSTEM_TASK_IDS.includes(task.task_id)) {
      acc[task.task_id] = task;
    }
    return acc;
  }, {} as Record<string, RawTaskData>);
  const withoutSystemTasks = tasks.filter(
    (task) => !QSDM_SYSTEM_TASK_IDS.includes(task.task_id)
  );

  return [
    createQsdmMinerSystemTask(systemTasksById[QSDM_MINER_SYSTEM_TASK_ID]),
    createQsdmEdgeWorkerSystemTask(
      systemTasksById[QSDM_EDGE_WORKER_SYSTEM_TASK_ID]
    ),
    createQsdmGPUWorkerSystemTask(
      systemTasksById[QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID]
    ),
    createQsdmRAMWorkerSystemTask(
      systemTasksById[QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID]
    ),
    createQsdmMotherHiveSystemTask(
      systemTasksById[QSDM_MOTHER_HIVE_SYSTEM_TASK_ID]
    ),
    createQsdmSkyFangLinkSystemTask(
      systemTasksById[QSDM_SKYFANG_LINK_SYSTEM_TASK_ID]
    ),
    ...withoutSystemTasks,
  ];
};

export const getQsdmSystemTaskMetadata = (
  metadataCID: string
): TaskMetadata | null => {
  if (isQsdmMinerSystemTask(metadataCID)) {
    return {
      author: 'QSDM',
      description:
        'Run the official QSDM CUDA protocol miner as an opt-in, permanent CELL task. NVIDIA proof solving and v2 attestation require Turing-or-newer hardware. Rewards come from accepted protocol proofs, not a separate Hive bounty. A zero-balance signer can build the bond from mining rewards. Sky Fang is a separate integration and is not required for mining.',
      repositoryUrl: QSDM_MINER_SOURCE_URL,
      createdAt: 0,
      imageUrl: QSDM_MINER_SYSTEM_TASK_ID,
      migrationDescription: '',
      requirementsTags: [
        {
          type: RequirementType.OS,
          value: process.platform,
          description:
            'Uses the local QSDM miner binary installed on this machine.',
        },
        {
          type: RequirementType.ARCHITECTURE,
          value: 'NVIDIA Turing+ / CUDA CC 7.5+',
          description:
            'Requires an NVIDIA GPU visible to nvidia-smi. The packaged CUDA SHA3 solver performs current protocol proof work; the future Tensor-Core fork remains a separate consensus activation.',
        },
        {
          type: RequirementType.NETWORK,
          value: 'QSDM Core',
          description:
            'Submits mining work to your configured QSDM validator or gateway.',
        },
        {
          type: RequirementType.ADDON,
          value: 'Protocol mining emission',
          description:
            'Rewards are paid by QSDM Core mining emission for accepted proofs. This miner task does not use an extra Hive-funded bounty pool.',
        },
      ],
      infoUrl: QSDM_MINER_INFO_URL,
      tags: [
        'QSDM',
        'CELL',
        'Miner',
        'NVIDIA GPU Required',
        'Protocol Emission',
        'No Hive Bounty',
        'CC 7.5+',
        'No Expiry',
      ],
    };
  }

  if (isQsdmEdgeWorkerSystemTask(metadataCID)) {
    const resource = getQsdmEdgeWorkerResource(metadataCID) || 'cpu';
    const metadataByResource: Record<
      QsdmEdgeWorkerResource,
      Pick<TaskMetadata, 'description' | 'requirementsTags' | 'tags'>
    > = {
      cpu: {
        description:
          'Share bounded CPU capacity directly or through a laboratory Relay. The Relay applies its CPU policy, aggregates authenticated receipts from outbound-only agents, and Mother Hive submits verified work for funded-pool rewards.',
        requirementsTags: [
          {
            type: RequirementType.CPU,
            value: 'Any modern CPU',
            description:
              'Runs bounded Relay-issued CPU jobs. Both the agent contribution and Relay policy cap the work.',
          },
          {
            type: RequirementType.NETWORK,
            value: 'QSDM Edge Pool',
            description:
              'Can run locally, or Mother Hive can consume authenticated aggregate receipts from a configured Relay.',
          },
          {
            type: RequirementType.ADDON,
            value: 'Funded reward pool',
            description:
              'Rewards are available only when a task sponsor has funded the pool; the participant wallet is not silently used to fund itself.',
          },
        ],
        tags: [
          'QSDM',
          'CELL',
          'CPU',
          'Edge Worker',
          'Pooled Compute',
          'No Expiry',
        ],
      },
      gpu: {
        description:
          'Share bounded NVIDIA CUDA capacity directly or through a laboratory Relay. GPU Edge Worker is shared compute, not QSDM protocol mining; the Relay GPU policy limits accepted work before Mother Hive submits funded-pool rewards.',
        requirementsTags: [
          {
            type: RequirementType.ARCHITECTURE,
            value: 'NVIDIA Turing+ / CUDA CC 7.5+',
            description:
              'Requires a supported NVIDIA GPU, working driver/nvidia-smi, and the signed QSDM CUDA helper.',
          },
          {
            type: RequirementType.NETWORK,
            value: 'QSDM Edge Pool',
            description:
              'A Relay aggregates authenticated GPU receipts from outbound-only laboratory agents for Mother Hive.',
          },
          {
            type: RequirementType.ADDON,
            value: 'Separate from mining',
            description:
              'This task shares GPU compute capacity. QSDM Miner remains the protocol-emission mining path.',
          },
        ],
        tags: [
          'QSDM',
          'CELL',
          'GPU',
          'CUDA',
          'Edge Worker',
          'Pooled Compute',
          'No Expiry',
        ],
      },
      ram: {
        description:
          'Share bounded RAM directly or through a laboratory Relay. Memory is used only by fixed QSDM jobs, wiped after each job, policy-limited by the Relay, and represented by authenticated receipts for Mother Hive.',
        requirementsTags: [
          {
            type: RequirementType.ADDON,
            value: 'Configurable RAM limit',
            description:
              'The agent never leases more memory than its configured MiB ceiling and wipes the work buffer after each job.',
          },
          {
            type: RequirementType.NETWORK,
            value: 'QSDM Edge Pool',
            description:
              'Computer A aggregates authenticated RAM receipts from outbound-only laboratory agents.',
          },
          {
            type: RequirementType.ADDON,
            value: 'No remote shell',
            description:
              'The agent executes fixed resource jobs only; Relays cannot send commands, scripts, or arbitrary executables.',
          },
        ],
        tags: [
          'QSDM',
          'CELL',
          'RAM',
          'Memory',
          'Edge Worker',
          'Pooled Compute',
          'No Expiry',
        ],
      },
    };
    const metadata = metadataByResource[resource];
    return {
      author: 'QSDM',
      description: metadata.description,
      repositoryUrl: QSDM_HIVE_SYSTEM_TASK_SOURCE_URL,
      createdAt: 0,
      imageUrl: QSDM_EDGE_WORKER_TASK_DEFINITIONS[resource].taskId,
      migrationDescription: '',
      requirementsTags: [
        {
          type: RequirementType.OS,
          value: process.platform,
          description:
            'Runs through the bounded QSDM Hive resource-worker runtime.',
        },
        ...metadata.requirementsTags,
      ],
      infoUrl: QSDM_EDGE_WORKER_INFO_URL,
      tags: metadata.tags,
    };
  }

  if (isQsdmMotherHiveSystemTask(metadataCID)) {
    return {
      author: 'QSDM',
      description:
        'Activate the Mother Hive role inside QSDM Hive. A paired Relay reports authenticated Agent capacity and receipts; this task keeps the Hive-to-Relay acknowledgement active and displays pooled CPU, NVIDIA GPU, and RAM available to QSDM-approved distributed workloads.',
      repositoryUrl: QSDM_HIVE_SYSTEM_TASK_SOURCE_URL,
      createdAt: 0,
      imageUrl: QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
      migrationDescription: '',
      requirementsTags: [
        {
          type: RequirementType.OS,
          value: 'QSDM Hive',
          description:
            'Mother Hive is a role of this QSDM Hive installation, not another client or application.',
        },
        {
          type: RequirementType.NETWORK,
          value: 'Paired QSDM Relay',
          description:
            'Use Edge Control to pair a Relay and connect this QSDM Hive as its Mother Hive.',
        },
        {
          type: RequirementType.ADDON,
          value: 'Approved distributed workloads',
          description:
            'Remote resources are scheduled through bounded QSDM jobs. They do not appear as transparent local RAM, CPU cores, or GPU devices to arbitrary programs.',
        },
        {
          type: RequirementType.ADDON,
          value: '70 / 15 / 15 revenue policy',
          description:
            'Target gross workload revenue allocation: 70% contributors, 15% Mother Hive operator, 15% ecosystem reserve. Settlement remains disabled until worker wallets and Relay receipts are chain-verifiable.',
        },
      ],
      infoUrl: QSDM_EDGE_WORKER_INFO_URL,
      tags: [
        'QSDM',
        'CELL',
        'Mother Hive',
        'Relay',
        'Pooled Compute',
        'No Expiry',
      ],
    };
  }

  if (isQsdmSkyFangLinkSystemTask(metadataCID)) {
    return {
      author: 'QSDM',
      description:
        'Link your active QSDM wallet to a Sky Fang account. This task is the account-eligibility gate for Sky Fang CELL rewards and an ongoing stake-weighted integration task: rewards scale from CELL staked in Hive for this task plus CELL stake reported by Sky Fang when available.',
      repositoryUrl: QSDM_HIVE_SYSTEM_TASK_SOURCE_URL,
      createdAt: 0,
      imageUrl: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      migrationDescription: '',
      requirementsTags: [
        {
          type: RequirementType.NETWORK,
          value: 'Sky Fang account',
          description:
            'Log in to Sky Fang and link the same QSDM wallet address used by this Hive signer.',
        },
        {
          type: RequirementType.ADDON,
          value: 'QSDM Hive wallet',
          description:
            'Hive signs the wallet-link challenge locally and later verifies the linked address through Sky Fang status.',
        },
        {
          type: RequirementType.OS,
          value: process.platform,
          description:
            'No GPU or miner is required. This is an account-linking, eligibility, and integration reward task.',
        },
        {
          type: RequirementType.ADDON,
          value: 'Stake-weighted CELL reward',
          description:
            'Hive displays Hive task stake, Sky Fang stake, and combined stake. The verifier submits one proof per QSDM round while the wallet remains linked.',
        },
      ],
      infoUrl: QSDM_SKYFANG_LINK_INFO_URL,
      tags: [
        'QSDM',
        'CELL',
        'Sky Fang',
        'Wallet Link',
        'Mandatory',
        'Stake Weighted',
        'Ongoing',
        'No GPU',
      ],
    };
  }

  return null;
};

export const createQsdmEdgeWorkerScript = () => `
const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const https = require('https');
const { execFile } = require('child_process');

const taskId = process.env.QSDM_EDGE_TASK_ID || '${QSDM_EDGE_WORKER_SYSTEM_TASK_ID}';
const resource = process.env.QSDM_EDGE_RESOURCE || 'cpu';
const sender = process.env.QSDM_TASK_ACTION_SENDER || 'unknown';
const intervalMs = Math.max(10000, Number(process.env.QSDM_EDGE_WORKER_INTERVAL_MS || 60000));
const iterations = Math.max(1000, Number(process.env.QSDM_EDGE_WORKER_ITERATIONS || 50000));
const ramMiB = Math.max(8, Math.min(1024, Number(process.env.QSDM_EDGE_WORKER_RAM_MIB || 64)));
const gpuUnits = Math.max(1000, Math.min(100000000, Number(process.env.QSDM_EDGE_WORKER_GPU_UNITS || 5000000)));
const gpuHelperPath = process.env.QSDM_EDGE_GPU_HELPER || '';
const relayUrl = process.env.QSDM_EDGE_RELAY_URL || process.env.QSDM_EDGE_POOL_URL || 'http://127.0.0.1:7740';
const relayTokenFile = process.env.QSDM_EDGE_RELAY_TOKEN_FILE || process.env.QSDM_EDGE_POOL_TOKEN_FILE || '';
const relayRequired = Boolean(relayTokenFile);
const maxRuntimeMs = Math.max(300000, Number(process.env.QSDM_EDGE_WORKER_MAX_RUNTIME_MS || 21600000));
const maxRssMb = Math.max(ramMiB + 128, Number(process.env.QSDM_EDGE_WORKER_MAX_RSS_MB || ramMiB + 256));
const startedAtMs = Date.now();
let round = 0;
let stopping = false;
let timer = null;
let working = false;
let lastPoolProofId = '';

function sha256(value) {
  return crypto.createHash('sha256').update(value).digest('hex');
}

function rssMb() {
  return Math.round(process.memoryUsage().rss / 1024 / 1024);
}

function recycle(reason) {
  if (stopping) return true;
  stopping = true;
  if (timer) clearInterval(timer);
  console.log('QSDM Edge Worker recycling reason=' + reason + ' runtime_ms=' + (Date.now() - startedAtMs) + ' rss_mb=' + rssMb());
  process.exit(0);
  return true;
}

function shouldRecycle() {
  if (Date.now() - startedAtMs >= maxRuntimeMs) {
    return recycle('max-runtime');
  }
  if (rssMb() >= maxRssMb) {
    return recycle('max-rss');
  }
  return false;
}

function emitProof(payload) {
  console.log('QSDM_EDGE_PROOF ' + JSON.stringify(payload));
}

function loadRelayToken() {
  if (!relayTokenFile || !fs.existsSync(relayTokenFile)) return null;
  const raw = fs.readFileSync(relayTokenFile, 'utf8').trim();
  if (/^[0-9a-fA-F]{64,}$/.test(raw) && raw.length % 2 === 0) {
    return Buffer.from(raw, 'hex');
  }
  return raw.length >= 32 ? Buffer.from(raw, 'utf8') : null;
}

function verifyPoolProof(token, proof) {
  if (!proof || typeof proof !== 'object' || !Array.isArray(proof.receipt_ids)) return false;
  if (!/^[0-9a-f]{64}$/i.test(String(proof.signature || ''))) return false;
  const canonical = [
    proof.version,
    proof.proof_id,
    proof.coordinator_id,
    proof.resource,
    proof.window_start,
    proof.window_end,
    proof.worker_count,
    proof.job_count,
    proof.total_units,
    proof.total_memory_mib || 0,
    proof.receipt_root,
    proof.receipt_ids.join(','),
  ].map((value) => String(value)).join('\\n');
  const expected = crypto.createHmac('sha256', token).update(canonical).digest();
  const provided = Buffer.from(proof.signature, 'hex');
  return provided.length === expected.length && crypto.timingSafeEqual(provided, expected);
}

function getRelayProof() {
  const token = loadRelayToken();
  if (!token) return Promise.resolve(null);
  return new Promise((resolve, reject) => {
    const target = new URL('/v1/proofs/latest?resource=' + encodeURIComponent(resource), relayUrl);
    const timestamp = String(Math.floor(Date.now() / 1000));
    const nonce = crypto.randomBytes(16).toString('hex');
    const workerId = ('hive-' + sender.slice(0, 24)).replace(/[^A-Za-z0-9._-]/g, '-');
    const bodyHash = sha256('');
    const canonical = ['GET', '/v1/proofs/latest', timestamp, nonce, workerId, bodyHash].join('\\n');
    const signature = crypto.createHmac('sha256', token).update(canonical).digest('hex');
    const transport = target.protocol === 'https:' ? https : http;
    const request = transport.request(target, {
      method: 'GET',
      headers: {
        'X-QSDM-Worker-ID': workerId,
        'X-QSDM-Timestamp': timestamp,
        'X-QSDM-Nonce': nonce,
        'X-QSDM-Signature': signature,
      },
      timeout: 5000,
    }, (response) => {
      let body = '';
      response.setEncoding('utf8');
      response.on('data', (chunk) => {
        if (body.length < 131072) body += chunk;
      });
      response.on('end', () => {
        if (response.statusCode === 404) return resolve(null);
        if (response.statusCode !== 200) return reject(new Error('relay HTTP ' + response.statusCode));
        try {
          const proof = JSON.parse(body);
          if (!verifyPoolProof(token, proof)) {
            return reject(new Error('relay proof signature did not verify'));
          }
          return resolve(proof);
        } catch (error) {
          return reject(error);
        }
      });
    });
    request.on('timeout', () => request.destroy(new Error('relay timeout')));
    request.on('error', reject);
    request.end();
  });
}

function cpuProof(seed) {
  let digest = seed;
  for (let index = 0; index < iterations; index += 1) digest = sha256(digest);
  return {
    algorithm: 'sha256-iterated',
    units: iterations,
    digest,
  };
}

function ramProof(seed) {
  const bytes = ramMiB * 1024 * 1024;
  const buffer = Buffer.allocUnsafe(bytes);
  const pattern = crypto.createHash('sha256').update(seed).digest();
  for (let offset = 0; offset < buffer.length; offset += pattern.length) {
    pattern.copy(buffer, offset, 0, Math.min(pattern.length, buffer.length - offset));
  }
  const digest = crypto.createHash('sha256').update(buffer).digest('hex');
  buffer.fill(0);
  return {
    algorithm: 'ram-buffer-hash-v1',
    units: bytes,
    memory_mib: ramMiB,
    digest,
  };
}

function gpuProof(seed) {
  if (!gpuHelperPath || !fs.existsSync(gpuHelperPath)) {
    return Promise.reject(new Error('trusted GPU helper is not installed'));
  }
  return new Promise((resolve, reject) => {
    execFile(gpuHelperPath, ['--seed', seed, '--units', String(gpuUnits), '--json'], {
      windowsHide: true,
      timeout: 120000,
      maxBuffer: 65536,
    }, (error, stdout) => {
      if (error) return reject(error);
      try {
        const result = JSON.parse(stdout);
        const seedBytes = Buffer.from(seed, 'hex');
        const values = Buffer.alloc(24);
        values.writeBigUInt64LE(BigInt(gpuUnits), 0);
        values.writeBigUInt64LE(BigInt('0x' + result.xor_value), 8);
        values.writeBigUInt64LE(BigInt('0x' + result.sum_value), 16);
        const digest = crypto.createHash('sha256')
          .update('qsdm-edge-gpu-v1')
          .update(seedBytes)
          .update(values)
          .digest('hex');
        return resolve({
          algorithm: 'cuda-splitmix64-v1',
          units: gpuUnits,
          digest,
          gpu_name: result.gpu_name,
          gpu_uuid: result.gpu_uuid,
          helper_duration_ms: result.duration_ms,
        });
      } catch (parseError) {
        return reject(parseError);
      }
    });
  });
}

async function computeProof() {
  if (stopping || working || shouldRecycle()) return;
  working = true;
  round += 1;
  const startedAt = new Date().toISOString();
  const seed = [taskId, sender, round, Date.now(), process.pid].join(':');
  try {
    const poolProof = await getRelayProof().catch((error) => {
      if (relayRequired) throw error;
      console.log('QSDM Edge Worker relay unavailable resource=' + resource + ' error=' + error.message + '; using direct local work');
      return null;
    });
    if (poolProof && poolProof.proof_id) {
      if (poolProof.proof_id !== lastPoolProofId) {
        lastPoolProofId = poolProof.proof_id;
        emitProof({
          source: 'qsdm-edge-relay',
          worker_kind: resource + '-relay-v1',
          resource,
          round,
          slot: Math.floor(Date.now() / 1000),
          submission_value: poolProof.receipt_root,
          proof: poolProof,
        });
      }
      return;
    }

    if (relayRequired) {
      console.log('QSDM Mother Hive waiting for a verified relay receipt resource=' + resource);
      return;
    }

    const seedHash = sha256(seed);
    const proof = resource === 'ram'
      ? ramProof(seed)
      : resource === 'gpu'
      ? await gpuProof(seedHash)
      : cpuProof(seed);
    emitProof({
      source: 'qsdm-edge-worker-' + resource,
      worker_kind: resource + '-local-v1',
      resource,
      round,
      slot: Math.floor(Date.now() / 1000),
      submission_value: proof.digest,
      proof: {
        ...proof,
        seed_hash: seedHash,
        started_at: startedAt,
        completed_at: new Date().toISOString(),
      },
    });
  } catch (error) {
    console.error('QSDM Edge Worker proof failed resource=' + resource + ' error=' + error.message);
  } finally {
    working = false;
    shouldRecycle();
  }
}

console.log('QSDM Edge Worker started task=' + taskId + ' resource=' + resource + ' sender=' + sender + ' interval_ms=' + intervalMs + ' max_runtime_ms=' + maxRuntimeMs + ' max_rss_mb=' + maxRssMb);
computeProof();
timer = setInterval(() => { computeProof(); }, intervalMs);

function shutdown(signal) {
  stopping = true;
  if (timer) clearInterval(timer);
  console.log('QSDM Edge Worker stopping signal=' + signal);
  process.exit(0);
}

process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));
`;

export const createQsdmMotherHiveScript = () => `
const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const https = require('https');
const os = require('os');

const relayUrl = process.env.QSDM_EDGE_RELAY_URL || 'http://127.0.0.1:7740';
const tokenFile = process.env.QSDM_EDGE_RELAY_TOKEN_FILE || '';
const intervalMs = Math.max(5000, Number(process.env.QSDM_MOTHER_HIVE_INTERVAL_MS || 15000));
const maxRuntimeMs = Math.max(300000, Number(process.env.QSDM_MOTHER_HIVE_MAX_RUNTIME_MS || 21600000));
const startedAt = Date.now();
let timer = null;
let stopping = false;
let checking = false;

function loadToken() {
  if (!tokenFile || !fs.existsSync(tokenFile)) throw new Error('Relay pairing credential is missing');
  const raw = fs.readFileSync(tokenFile, 'utf8').trim();
  if (/^[0-9a-fA-F]{64,}$/.test(raw) && raw.length % 2 === 0) return Buffer.from(raw, 'hex');
  const token = Buffer.from(raw, 'utf8');
  if (token.length < 32) throw new Error('Relay pairing credential is invalid');
  return token;
}

function requestStatus() {
  return new Promise((resolve, reject) => {
    const token = loadToken();
    const target = new URL('/v1/status', relayUrl);
    if (target.protocol !== 'http:' && target.protocol !== 'https:') return reject(new Error('Relay URL must use HTTP or HTTPS'));
    const timestamp = String(Math.floor(Date.now() / 1000));
    const nonce = crypto.randomBytes(16).toString('hex');
    const workerId = ('hive-' + os.hostname()).replace(/[^A-Za-z0-9._-]/g, '-').slice(0, 64);
    const bodyHash = crypto.createHash('sha256').update('').digest('hex');
    const canonical = ['GET', target.pathname, timestamp, nonce, workerId, bodyHash].join('\\n');
    const signature = crypto.createHmac('sha256', token).update(canonical).digest('hex');
    const transport = target.protocol === 'https:' ? https : http;
    const request = transport.request(target, {
      method: 'GET',
      headers: {
        'X-QSDM-Worker-ID': workerId,
        'X-QSDM-Timestamp': timestamp,
        'X-QSDM-Nonce': nonce,
        'X-QSDM-Signature': signature,
      },
      timeout: 5000,
    }, (response) => {
      let body = '';
      response.setEncoding('utf8');
      response.on('data', (chunk) => { if (body.length < 262144) body += chunk; });
      response.on('end', () => {
        if (response.statusCode !== 200) return reject(new Error('Relay HTTP ' + response.statusCode));
        try { return resolve(JSON.parse(body)); } catch (error) { return reject(error); }
      });
    });
    request.on('timeout', () => request.destroy(new Error('Relay timeout')));
    request.on('error', reject);
    request.end();
  });
}

async function heartbeat() {
  if (stopping || checking) return;
  if (Date.now() - startedAt >= maxRuntimeMs) return shutdown('max-runtime');
  checking = true;
  try {
    const status = await requestStatus();
    console.log('QSDM_MOTHER_HIVE_STATUS ' + JSON.stringify({
      relay_id: status.relay_id || status.coordinator_id,
      workers: Array.isArray(status.workers) ? status.workers.length : 0,
      active_jobs: Number(status.active_leases || 0),
      receipt_counts: status.receipt_counts || {},
      checked_at: new Date().toISOString(),
    }));
  } catch (error) {
    console.error('QSDM Hive Mother mode is waiting for Relay: ' + error.message);
  } finally {
    checking = false;
  }
}

function shutdown(signal) {
  if (stopping) return;
  stopping = true;
  if (timer) clearInterval(timer);
  console.log('QSDM Hive Mother mode stopping signal=' + signal);
  process.exit(0);
}

console.log('QSDM Hive Mother mode started relay=' + relayUrl);
heartbeat();
timer = setInterval(heartbeat, intervalMs);
process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));
`;

const createSubmissionDigest = (payload: Record<string, unknown>) =>
  createHash('sha256').update(JSON.stringify(payload)).digest('hex');

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const roundCellAmount = (amount: number) =>
  Math.round(amount * 1_000_000_000) / 1_000_000_000;

const getPositiveNumber = (value: unknown, fallback = 0) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
};

const getNonNegativeNumber = (value: unknown, fallback = 0) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
};

const getCaseInsensitiveRecordValue = (
  record: Record<string, unknown> | undefined,
  key: string
) => {
  if (!record || !key) {
    return undefined;
  }

  if (typeof record[key] !== 'undefined') {
    return record[key];
  }

  const normalizedKey = key.toLowerCase();
  const matchedEntry = Object.entries(record).find(
    ([candidate]) => candidate.toLowerCase() === normalizedKey
  );

  return matchedEntry?.[1];
};

export const normalizeQsdmSystemTaskCoreCellAmount = (
  value: unknown,
  fallback = 0
) => {
  const amount = getPositiveNumber(value, fallback);
  if (amount <= 0) {
    return 0;
  }

  const coreAmount =
    amount >= QSDM_CELL_DENOMINATION_NORMALIZATION_LIMIT
      ? amount / QSDM_CELL_DENOMINATION_FACTOR
      : amount;

  return roundCellAmount(coreAmount);
};

const getQsdmTaskParticipantStakeCell = (
  task: Partial<RawTaskData>,
  sender: string
) => {
  const normalizedSender = sender.toLowerCase();
  const participants = ((task as any).participants || {}) as Record<
    string,
    Record<string, unknown>
  >;

  for (const [participantKey, participant] of Object.entries(participants)) {
    if (!isRecord(participant)) {
      continue;
    }
    const participantSender = String(participant.sender || participantKey);
    if (participantSender.toLowerCase() === normalizedSender) {
      return normalizeQsdmSystemTaskCoreCellAmount(participant.stake);
    }
  }

  const stakeList = (task.stake_list || {}) as Record<string, unknown>;
  return normalizeQsdmSystemTaskCoreCellAmount(
    getCaseInsensitiveRecordValue(stakeList, sender)
  );
};

const getSkyFangInGameStakeCell = (status: SkyFangLinkStatusResponse) =>
  normalizeQsdmSystemTaskCoreCellAmount(
    getNonNegativeNumber(status.skyfang_stake_cell) ||
      getNonNegativeNumber(status.in_game_stake_cell) ||
      getNonNegativeNumber(status.game_stake_cell) ||
      getNonNegativeNumber(status.total_game_stake_cell)
  );

const getSkyFangCombinedStakeCell = (
  liveTask: Partial<RawTaskData>,
  status: SkyFangLinkStatusResponse,
  sender: string
) =>
  roundCellAmount(
    getQsdmTaskParticipantStakeCell(liveTask, sender) +
      getSkyFangInGameStakeCell(status)
  );

const calculateQsdmSkyFangLinkRewardCell = ({
  hiveStakeCell,
  skyFangStakeCell,
}: {
  hiveStakeCell: number;
  skyFangStakeCell: number;
}) => {
  const uncappedReward =
    QSDM_SKYFANG_LINK_BASE_REWARD_CELL +
    Math.max(0, hiveStakeCell) * QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE +
    Math.max(0, skyFangStakeCell) * QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE;

  return roundCellAmount(
    Math.min(QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL, uncappedReward)
  );
};

const getQsdmNativeTaskClock = async (
  task: Pick<RawTaskData, 'round_time' | 'starting_slot'>
): Promise<QsdmNativeTaskClock> => {
  const response = await qsdmGetJson<QsdmStatusResponse>(
    buildQsdmCoreApiUrl('/status'),
    {
      timeout: 4000,
    }
  );
  const slot = Math.max(0, Math.floor(Number(response.chain_tip) || 0));
  const roundTimeBlocks = Math.max(1, Math.floor(Number(task.round_time) || 1));
  const startingSlot = Math.max(0, Math.floor(Number(task.starting_slot) || 0));
  const round =
    slot >= startingSlot
      ? Math.floor((slot - startingSlot) / roundTimeBlocks)
      : 0;

  return {
    round,
    slot,
    roundTimeBlocks,
    startingSlot,
  };
};

const delay = (ms: number) =>
  new Promise((resolve) => {
    setTimeout(resolve, ms);
  });

const getQsdmTaskActionCommittedNonce = async (sender: string) => {
  const url = new URL(buildQsdmCoreApiUrl('/mining/account'));
  url.searchParams.set('address', sender);
  const response = await qsdmGetJson<QsdmMiningAccountResponse>(
    url.toString(),
    { timeout: 4000 }
  );
  const nonce = Number(response.nonce);
  return Number.isFinite(nonce) ? nonce : undefined;
};

const waitForQsdmTaskActionCommit = async (
  response: QsdmTaskActionSubmitResponse,
  context: string,
  taskId = QSDM_EDGE_WORKER_SYSTEM_TASK_ID
) => {
  const sender = getQsdmTaskActionSender();
  const signedNonce = Number(response.client_nonce);
  if (!sender || !Number.isFinite(signedNonce)) {
    return true;
  }

  const targetNonce = signedNonce + 1;
  const deadline = Date.now() + QSDM_TASK_ACTION_COMMIT_TIMEOUT_MS;

  while (Date.now() <= deadline) {
    try {
      const committedNonce = await getQsdmTaskActionCommittedNonce(sender);
      if (committedNonce !== undefined && committedNonce >= targetNonce) {
        return true;
      }
    } catch {
      // Keep polling; transient local-core/API errors are common during restarts.
    }

    await delay(QSDM_TASK_ACTION_COMMIT_POLL_MS);
  }

  writeTaskLog(
    taskId,
    `${context} deferred: QSDM Core did not confirm nonce ${signedNonce} before timeout.`
  );
  return false;
};

const getQsdmEdgeWorkerLiveTask = async (
  taskId = QSDM_EDGE_WORKER_SYSTEM_TASK_ID
) => {
  const response = await qsdmGetFirstJson<QsdmTaskStateResponse>(
    buildQsdmTaskReadUrls(`/tasks/${encodeURIComponent(taskId)}/state`),
    { timeout: 4000 }
  );

  return response.task || {};
};

const ensureQsdmEdgeWorkerRewardPool = async (
  taskId: string,
  rewardAmount: number,
  taskState?: Partial<RawTaskData>
) => {
  const resource = getQsdmEdgeWorkerResource(taskId) || 'cpu';
  const definition = QSDM_EDGE_WORKER_TASK_DEFINITIONS[resource];
  const targetPool = roundCellAmount(
    Math.max(definition.rewardPoolTarget, rewardAmount)
  );
  if (targetPool <= 0 || rewardAmount <= 0) {
    return false;
  }

  let task: Partial<RawTaskData>;
  try {
    task = taskState || (await getQsdmEdgeWorkerLiveTask(taskId));
  } catch (error: any) {
    writeTaskLog(
      taskId,
      `Could not check ${definition.name} reward pool: ${
        error?.message || error
      }`
    );
    return false;
  }

  const currentPool = normalizeQsdmSystemTaskCoreCellAmount(
    task.reward_pool_amount
  );
  if (currentPool >= rewardAmount) {
    return true;
  }

  if (process.env.QSDM_EDGE_WORKER_ALLOW_SIGNER_POOL_FUNDING !== '1') {
    writeTaskLog(
      taskId,
      `${definition.name} reward pool has ${currentPool} CELL but needs ${rewardAmount} CELL. Waiting for sponsor funding; participant self-funding is disabled.`
    );
    return false;
  }

  const topUpAmount = roundCellAmount(
    Math.max(rewardAmount, targetPool - currentPool)
  );
  if (topUpAmount <= 0) {
    return false;
  }

  try {
    const response = await submitQsdmTaskActionIntent({
      taskId,
      action: 'fund',
      amount: topUpAmount,
      payload: {
        source: `qsdm-edge-worker-${resource}`,
        reason: `operator-authorized ${resource} worker reward pool funding`,
        reward_per_round_cell: rewardAmount,
        reward_pool_target_cell: targetPool,
      },
    });
    const committed = await waitForQsdmTaskActionCommit(
      response,
      `${definition.name} reward pool seed`,
      taskId
    );
    if (!committed) {
      return false;
    }
    writeTaskLog(
      taskId,
      `Seeded ${definition.name} reward pool with ${topUpAmount} CELL`
    );
    return true;
  } catch (error: any) {
    writeTaskLog(
      taskId,
      `Could not seed ${definition.name} reward pool: ${
        error?.message || error
      }`
    );
    return false;
  }
};

const hasQsdmEdgeWorkerRoundSubmission = (
  task: Partial<RawTaskData>,
  sender: string,
  round: number
) => {
  const submissions = task.submissions;
  const bySender = submissions?.[String(round)];
  return isRecord(bySender) && Boolean(bySender[sender]);
};

const claimQsdmEdgeWorkerReward = async (taskId: string, round: number) => {
  const resource = getQsdmEdgeWorkerResource(taskId) || 'cpu';
  try {
    const response = await submitQsdmTaskActionIntent({
      taskId,
      action: 'claim',
      payload: {
        source: `qsdm-edge-worker-${resource}`,
        round: 0,
        latest_submitted_round: round,
      },
    });
    await waitForQsdmTaskActionCommit(
      response,
      'Edge Worker reward claim',
      taskId
    );
    writeTaskLog(
      taskId,
      `Claimed ${resource.toUpperCase()} Edge Worker reward round=${round}`
    );
  } catch (error: any) {
    writeTaskLog(
      taskId,
      `${resource.toUpperCase()} Edge Worker reward claim deferred: ${
        error?.message || error
      }`
    );
  }
};

const submitQsdmEdgeWorkerProof = async (
  payload: Record<string, unknown>,
  state: EdgeWorkerOutputState
) => {
  if (state.submitting) {
    writeTaskLog(
      state.taskId,
      `Skipping ${state.resource.toUpperCase()} Edge Worker proof submit: previous proof action is still waiting for QSDM Core confirmation.`
    );
    return;
  }

  const sender = getQsdmTaskActionSender();
  if (!sender) {
    writeTaskLog(
      state.taskId,
      'Skipping Edge Worker proof submit: QSDM_TASK_ACTION_SENDER is not configured.'
    );
    return;
  }

  let clock: QsdmNativeTaskClock;
  try {
    clock = await getQsdmNativeTaskClock(state.task);
  } catch (error: any) {
    writeTaskLog(
      state.taskId,
      `Skipping Edge Worker proof submit: QSDM Core status unavailable: ${
        error?.message || error
      }`
    );
    return;
  }

  if (state.submittedRounds.has(clock.round)) {
    writeTaskLog(
      state.taskId,
      `Skipping Edge Worker proof submit: round=${clock.round} already submitted for slot=${clock.slot}.`
    );
    return;
  }

  let liveTask: Partial<RawTaskData>;
  try {
    liveTask = await getQsdmEdgeWorkerLiveTask(state.taskId);
  } catch (error: any) {
    writeTaskLog(
      state.taskId,
      `Skipping Edge Worker proof submit: task state unavailable: ${
        error?.message || error
      }`
    );
    return;
  }

  if (hasQsdmEdgeWorkerRoundSubmission(liveTask, sender, clock.round)) {
    writeTaskLog(
      state.taskId,
      `Skipping Edge Worker proof submit: round=${clock.round} already exists on QSDM Core.`
    );
    state.submittedRounds.add(clock.round);
    return;
  }

  const rewardAmount = normalizeQsdmSystemTaskCoreCellAmount(
    state.task.bounty_amount_per_round
  );
  if (rewardAmount <= 0) {
    writeTaskLog(
      state.taskId,
      'Skipping Edge Worker proof submit: configured reward amount is zero.'
    );
    return;
  }

  state.submitting = true;
  const poolReady = await ensureQsdmEdgeWorkerRewardPool(
    state.taskId,
    rewardAmount,
    liveTask
  );
  if (!poolReady) {
    writeTaskLog(
      state.taskId,
      'Skipping Edge Worker proof submit: reward pool is not ready yet; keeping worker active and retrying instead of submitting unpaid work.'
    );
    state.submitting = false;
    return;
  }

  const hardenedPayload = {
    ...payload,
    local_worker_round: payload.round,
    round: clock.round,
    slot: clock.slot,
    reward_amount: rewardAmount,
    qsdm_round_unit: 'block-height',
    qsdm_round_time_blocks: clock.roundTimeBlocks,
    qsdm_starting_slot: clock.startingSlot,
    client_submission_digest: createSubmissionDigest(payload),
  };

  try {
    const response = await submitQsdmTaskActionIntent({
      taskId: state.taskId,
      action: 'submit',
      payload: hardenedPayload,
    });
    writeTaskLog(
      state.taskId,
      `Submitted ${state.resource.toUpperCase()} Edge Worker proof round=${
        clock.round
      } slot=${clock.slot} reward=${rewardAmount}`
    );
    state.submittedRounds.add(clock.round);
    if (rewardAmount > 0) {
      const committed = await waitForQsdmTaskActionCommit(
        response,
        'Edge Worker proof submit',
        state.taskId
      );
      if (committed) {
        await claimQsdmEdgeWorkerReward(state.taskId, clock.round);
      }
    }
  } catch (error: any) {
    writeTaskLog(
      state.taskId,
      `${state.resource.toUpperCase()} Edge Worker proof submit failed: ${
        error?.message || error
      }`
    );
  } finally {
    state.submitting = false;
  }
};

const handleQsdmEdgeWorkerOutput = (
  data: Buffer,
  state: EdgeWorkerOutputState
) => {
  const text = data.toString();
  writeTaskLog(state.taskId, text.trimEnd());

  state.pending += text;
  const lines = state.pending.split(/\r?\n/);
  state.pending = lines.pop() || '';

  lines.forEach((line) => {
    const marker = 'QSDM_EDGE_PROOF ';
    if (!line.startsWith(marker)) {
      return;
    }

    try {
      const payload = JSON.parse(line.slice(marker.length));
      if (!isRecord(payload)) {
        throw new Error('Edge Worker proof payload is not an object');
      }
      if (payload.resource && payload.resource !== state.resource) {
        throw new Error(
          `Edge Worker proof resource ${payload.resource} does not match ${state.resource}`
        );
      }
      submitQsdmEdgeWorkerProof(payload, state);
    } catch (error: any) {
      writeTaskLog(
        state.taskId,
        `Could not parse Edge Worker proof line: ${error?.message || error}`
      );
    }
  });
};

const getSkyFangBaseUrl = () =>
  (process.env.QSDM_SKYFANG_BASE_URL || 'https://skyfang.xyz').replace(
    /\/+$/,
    ''
  );

const getSkyFangLinkDashboardUrl = () =>
  `${getSkyFangBaseUrl()}/login?next=/dashboard/qsdm`;

const buildSkyFangLinkRequiredMessage = (sender: string) =>
  `Sky Fang account is not linked to the active QSDM wallet yet. Open ${getSkyFangLinkDashboardUrl()}, sign in, and link ${sender}.`;

const createQsdmSkyFangLinkScript = () => `
const intervalMs = Math.max(15000, Number(process.env.QSDM_SKYFANG_LINK_INTERVAL_MS || 30000));
const sender = process.env.QSDM_TASK_ACTION_SENDER || '';
let stopping = false;

function emitCheck() {
  if (stopping) return;
  console.log('QSDM_SKYFANG_LINK_CHECK ' + JSON.stringify({
    source: 'qsdm-skyfang-wallet-link',
    sender,
    checked_at: new Date().toISOString(),
  }));
}

console.log('QSDM Sky Fang Link verifier started sender=' + sender + ' interval_ms=' + intervalMs);
emitCheck();
const timer = setInterval(emitCheck, intervalMs);

function shutdown(signal) {
  stopping = true;
  clearInterval(timer);
  console.log('QSDM Sky Fang Link verifier stopping signal=' + signal);
  process.exit(0);
}

process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));
`;

const getSkyFangLinkDirectory = () =>
  path.join(getAppDataPath(), 'namespace', QSDM_SKYFANG_LINK_SYSTEM_TASK_ID);

const getSkyFangLinkScriptPath = () =>
  path.join(getSkyFangLinkDirectory(), 'skyfang-link-worker.js');

const ensureSkyFangLinkScript = () => {
  const workerScriptPath = getSkyFangLinkScriptPath();
  fs.mkdirSync(path.dirname(workerScriptPath), { recursive: true });
  fs.writeFileSync(workerScriptPath, createQsdmSkyFangLinkScript(), 'utf8');
  return workerScriptPath;
};

const getQsdmSkyFangLinkLiveTask = async () => {
  const response = await qsdmGetFirstJson<QsdmTaskStateResponse>(
    buildQsdmTaskReadUrls(
      `/tasks/${encodeURIComponent(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID)}/state`
    ),
    { timeout: 4000 }
  );

  return response.task || {};
};

export const getRewardedQsdmTaskSubmissionForSender = (
  task: Partial<RawTaskData>,
  sender: string
): { round: number; rewardAmount: number; claimed: boolean } | null => {
  const submissions = task.submissions || {};
  const rewarded = Object.entries(submissions)
    .map(([roundKey, bySender]) => {
      if (!isRecord(bySender)) {
        return null;
      }
      const submission = getCaseInsensitiveRecordValue(
        bySender as Record<string, unknown>,
        sender
      );
      if (!isRecord(submission)) {
        return null;
      }
      const submissionRecord = submission as Record<string, unknown>;
      const rewardAmount = getPositiveNumber(submission.reward_amount);
      if (rewardAmount <= 0) {
        return null;
      }
      const submissionRound =
        typeof submissionRecord.round !== 'undefined'
          ? Number(submissionRecord.round)
          : Number(roundKey);

      return {
        round: Number.isFinite(submissionRound) ? submissionRound : 0,
        rewardAmount,
        claimed: Boolean(submission.claimed),
      };
    })
    .filter(Boolean) as {
    round: number;
    rewardAmount: number;
    claimed: boolean;
  }[];

  return rewarded.sort((left, right) => right.round - left.round)[0] || null;
};

export const getRewardedQsdmTaskSubmissionForSenderRound = (
  task: Partial<RawTaskData>,
  sender: string,
  round: number
): { round: number; rewardAmount: number; claimed: boolean } | null => {
  const submissions = task.submissions || {};
  const bySender = getCaseInsensitiveRecordValue(
    submissions as Record<string, unknown>,
    String(round)
  );
  if (!isRecord(bySender)) {
    return null;
  }

  const submission = getCaseInsensitiveRecordValue(
    bySender as Record<string, unknown>,
    sender
  );
  if (!isRecord(submission)) {
    return null;
  }

  const rewardAmount = getPositiveNumber(submission.reward_amount);
  if (rewardAmount <= 0) {
    return null;
  }

  const submissionRound =
    typeof submission.round !== 'undefined' ? Number(submission.round) : round;

  return {
    round: Number.isFinite(submissionRound) ? submissionRound : round,
    rewardAmount,
    claimed: Boolean(submission.claimed),
  };
};

export const hasRewardedQsdmTaskSubmissionForSender = (
  task: Partial<RawTaskData>,
  sender: string
) => {
  return Boolean(getRewardedQsdmTaskSubmissionForSender(task, sender));
};

const getSkyFangLinkedWalletStatus = async (sender: string) => {
  const response = await axios.get<SkyFangLinkStatusResponse>(
    `${getSkyFangBaseUrl()}/api/qsdm/link-status`,
    {
      params: { address: sender },
      timeout: 15000,
    }
  );

  return response.data;
};

export const verifyQsdmSkyFangWalletLinked =
  async (): Promise<QsdmSkyFangWalletLinkGateResult> => {
    const sender = getQsdmTaskActionSender();
    if (!sender) {
      return {
        ok: false,
        sender: '',
        detail:
          'QSDM signer is not configured. Import or activate a QSDM wallet in Settings > Wallet, then link it to Sky Fang before starting the miner.',
      };
    }

    let status: SkyFangLinkStatusResponse;
    try {
      status = await getSkyFangLinkedWalletStatus(sender);
    } catch (error: any) {
      return {
        ok: false,
        sender,
        detail: `Could not verify the Sky Fang wallet link for ${sender}. Open ${getSkyFangLinkDashboardUrl()} and link this wallet, then try again. Detail: ${
          error?.message || error
        }`,
      };
    }

    if (status.ok === false) {
      return {
        ok: false,
        sender,
        detail: buildSkyFangLinkRequiredMessage(sender),
      };
    }

    const linkedAddress = (status.address || '').trim();
    if (!status.linked) {
      return {
        ok: false,
        sender,
        detail: buildSkyFangLinkRequiredMessage(sender),
      };
    }

    if (linkedAddress && linkedAddress.toLowerCase() !== sender.toLowerCase()) {
      return {
        ok: false,
        sender,
        detail: `Sky Fang is linked to ${linkedAddress}, but the active Hive wallet is ${sender}. Switch to the linked QSDM wallet or relink Sky Fang to the active wallet before starting the miner.`,
      };
    }

    const skyFangStakeCell = getSkyFangInGameStakeCell(status);

    return {
      ok: true,
      sender,
      linkedAt: status.linked_at,
      site: status.site || getSkyFangBaseUrl(),
      account: status.account,
      username: status.username,
      player: status.player,
      skyFangStakeCell,
      inGameStakeCell: skyFangStakeCell,
      gameStakeCell: skyFangStakeCell,
      totalGameStakeCell: skyFangStakeCell,
      rewardRateCell: QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
      rewardModel: 'skyfang-hive-combined-stake-v1',
    };
  };

export const requireQsdmSkyFangWalletLinkedForSkyFangLink = async () => {
  const gate = await verifyQsdmSkyFangWalletLinked();
  if (!gate.ok) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang wallet link verifier gate failed: ${gate.detail}`
    );
    throw new Error(
      gate.detail || buildSkyFangLinkRequiredMessage(gate.sender)
    );
  }

  writeTaskLog(
    QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
    `Sky Fang wallet link verified for ${gate.sender}; starting stake-weighted proof submitter.`
  );
  return gate;
};

const ensureQsdmSkyFangLinkRewardPool = async (
  rewardAmount: number,
  taskState?: Partial<RawTaskData>
) => {
  const targetPool = roundCellAmount(
    Math.max(QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL, rewardAmount)
  );
  if (targetPool <= 0 || rewardAmount <= 0) {
    return false;
  }

  let task: Partial<RawTaskData>;
  try {
    task = taskState || (await getQsdmSkyFangLinkLiveTask());
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Could not check Sky Fang Link reward pool: ${error?.message || error}`
    );
    return false;
  }

  const currentPool = normalizeQsdmSystemTaskCoreCellAmount(
    task.reward_pool_amount
  );
  if (currentPool >= rewardAmount) {
    return true;
  }

  const topUpAmount = roundCellAmount(
    Math.max(rewardAmount, targetPool - currentPool)
  );
  if (topUpAmount <= 0) {
    return false;
  }

  try {
    const response = await submitQsdmTaskActionIntent({
      taskId: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      action: 'fund',
      amount: topUpAmount,
      payload: {
        source: 'qsdm-skyfang-wallet-link',
        reason: 'seed Sky Fang stake-weighted reward pool',
        reward_model: 'skyfang-hive-combined-stake-v1',
        reward_per_round_cell: rewardAmount,
        reward_pool_target_cell: targetPool,
      },
    });
    const committed = await waitForQsdmTaskActionCommit(
      response,
      'Sky Fang Link reward pool seed',
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID
    );
    if (!committed) {
      return false;
    }
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Seeded Sky Fang Link reward pool with ${topUpAmount} CELL`
    );
    return true;
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Could not seed Sky Fang Link reward pool: ${error?.message || error}`
    );
    return false;
  }
};

const claimQsdmSkyFangLinkReward = async (round: number) => {
  try {
    const response = await submitQsdmTaskActionIntent({
      taskId: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      action: 'claim',
      payload: {
        source: 'qsdm-skyfang-wallet-link',
        round,
      },
    });
    const committed = await waitForQsdmTaskActionCommit(
      response,
      'Sky Fang Link reward claim',
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID
    );
    if (!committed) {
      return false;
    }
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Claimed Sky Fang Link reward round=${round}`
    );
    return true;
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang Link reward claim deferred: ${error?.message || error}`
    );
    return false;
  }
};

const submitQsdmSkyFangLinkProof = async (state: SkyFangLinkOutputState) => {
  if (state.submitting) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      'Skipping Sky Fang Link check: previous proof action is still waiting for QSDM Core confirmation.'
    );
    return;
  }

  const sender = getQsdmTaskActionSender();
  if (!sender) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      'Skipping Sky Fang Link check: QSDM_TASK_ACTION_SENDER is not configured.'
    );
    return;
  }

  let clock: QsdmNativeTaskClock;
  try {
    clock = await getQsdmNativeTaskClock(state.task);
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Skipping Sky Fang Link proof submit: QSDM Core status unavailable: ${
        error?.message || error
      }`
    );
    return;
  }

  if (state.submittedRounds.has(clock.round)) {
    return;
  }

  let liveTask: Partial<RawTaskData>;
  try {
    liveTask = await getQsdmSkyFangLinkLiveTask();
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Skipping Sky Fang Link check: task state unavailable: ${
        error?.message || error
      }`
    );
    return;
  }

  let status: SkyFangLinkStatusResponse;
  try {
    status = await getSkyFangLinkedWalletStatus(sender);
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang Link status unavailable. Open ${getSkyFangBaseUrl()}/login?next=/dashboard/qsdm and link this wallet. Detail: ${
        error?.message || error
      }`
    );
    return;
  }

  const linkedAddress = (status.address || sender).toLowerCase();
  if (!status.linked || linkedAddress !== sender.toLowerCase()) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang account is not linked to the active Hive wallet yet. Open ${getSkyFangBaseUrl()}/login?next=/dashboard/qsdm, sign in, and link ${sender}.`
    );
    return;
  }

  const currentRoundSubmission = getRewardedQsdmTaskSubmissionForSenderRound(
    liveTask,
    sender,
    clock.round
  );
  if (currentRoundSubmission) {
    if (currentRoundSubmission.claimed) {
      state.submittedRounds.add(clock.round);
      writeTaskLog(
        QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
        `Sky Fang Link reward is already claimed for round=${clock.round}; waiting for the next round.`
      );
      return;
    }

    state.submitting = true;
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang Link proof exists for round=${clock.round} but is not claimed yet; retrying claim.`
    );
    const claimed = await claimQsdmSkyFangLinkReward(clock.round);
    if (claimed) {
      state.submittedRounds.add(clock.round);
    }
    state.submitting = false;
    return;
  }

  const hiveStakeCell = getQsdmTaskParticipantStakeCell(liveTask, sender);
  const skyFangStakeCell = getSkyFangInGameStakeCell(status);
  const combinedStakeCell = getSkyFangCombinedStakeCell(
    liveTask,
    status,
    sender
  );
  const rewardAmount = calculateQsdmSkyFangLinkRewardCell({
    hiveStakeCell,
    skyFangStakeCell,
  });
  if (rewardAmount <= 0) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Skipping Sky Fang Link proof submit: reward formula returned 0 for hive_stake=${hiveStakeCell} skyfang_stake=${skyFangStakeCell}.`
    );
    return;
  }

  state.submitting = true;
  const poolReady = await ensureQsdmSkyFangLinkRewardPool(
    rewardAmount,
    liveTask
  );
  if (!poolReady) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      'Sky Fang Link reward pool is not ready yet; keeping verifier active instead of submitting an unpaid proof.'
    );
    state.submitting = false;
    return;
  }

  const payload = {
    source: 'qsdm-skyfang-wallet-link',
    submission_value: createSubmissionDigest({
      sender,
      site: status.site || getSkyFangBaseUrl(),
      linked_at: status.linked_at || '',
      hive_stake_cell: hiveStakeCell,
      skyfang_stake_cell: skyFangStakeCell,
      combined_stake_cell: combinedStakeCell,
      reward_amount: rewardAmount,
      reward_model: 'skyfang-hive-combined-stake-v1',
    }),
    round: clock.round,
    slot: clock.slot,
    reward_amount: rewardAmount,
    qsdm_round_unit: 'block-height',
    qsdm_round_time_blocks: clock.roundTimeBlocks,
    qsdm_starting_slot: clock.startingSlot,
    one_time_reward: false,
    reward_model: 'skyfang-hive-combined-stake-v1',
    base_reward_cell: QSDM_SKYFANG_LINK_BASE_REWARD_CELL,
    hive_stake_reward_rate: QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE,
    skyfang_stake_reward_rate: QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
    max_reward_per_round_cell: QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL,
    hive_stake_cell: hiveStakeCell,
    skyfang_stake_cell: skyFangStakeCell,
    combined_stake_cell: combinedStakeCell,
    linked_wallet_address: sender,
    skyfang_account: status.username || status.account || status.player || '',
    skyfang_site: status.site || getSkyFangBaseUrl(),
    skyfang_linked_at: status.linked_at || '',
  };

  try {
    const response = await submitQsdmTaskActionIntent({
      taskId: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      action: 'submit',
      payload,
    });
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Submitted Sky Fang Link proof round=${clock.round} slot=${clock.slot} hive_stake=${hiveStakeCell} skyfang_stake=${skyFangStakeCell} combined_stake=${combinedStakeCell} reward=${rewardAmount}`
    );
    if (rewardAmount > 0) {
      const committed = await waitForQsdmTaskActionCommit(
        response,
        'Sky Fang Link proof submit',
        QSDM_SKYFANG_LINK_SYSTEM_TASK_ID
      );
      if (committed) {
        const claimed = await claimQsdmSkyFangLinkReward(clock.round);
        if (claimed) {
          state.submittedRounds.add(clock.round);
          writeTaskLog(
            QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
            `Sky Fang Link reward claimed for round=${clock.round}; verifier will submit again next round.`
          );
        }
      }
    }
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang Link proof submit failed: ${error?.message || error}`
    );
  } finally {
    state.submitting = false;
  }
};

const handleQsdmSkyFangLinkOutput = (
  data: Buffer,
  state: SkyFangLinkOutputState
) => {
  const text = data.toString();
  writeTaskLog(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID, text.trimEnd());

  state.pending += text;
  const lines = state.pending.split(/\r?\n/);
  state.pending = lines.pop() || '';

  lines.forEach((line) => {
    const marker = 'QSDM_SKYFANG_LINK_CHECK ';
    if (!line.startsWith(marker)) {
      return;
    }

    submitQsdmSkyFangLinkProof(state).catch((error: any) => {
      state.submitting = false;
      writeTaskLog(
        QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
        `Sky Fang Link check crashed: ${error?.message || error}`
      );
    });
  });
};

const findFromAncestors = (
  relativePath: string,
  startDirectory = process.cwd()
): string | null => {
  let current = startDirectory;

  for (let i = 0; i < 8; i += 1) {
    const candidate = path.resolve(current, relativePath);
    if (fs.existsSync(candidate)) {
      return candidate;
    }

    const parent = path.dirname(current);
    if (parent === current) {
      break;
    }
    current = parent;
  }

  return null;
};

type MinerExecutableCandidateOptions = {
  platform?: NodeJS.Platform;
  resourcesPath?: string;
  executablePath?: string;
  workingDirectory?: string;
  env?: NodeJS.ProcessEnv;
};

export const getMinerExecutableCandidates = ({
  platform = process.platform,
  resourcesPath = (process as NodeJS.Process & { resourcesPath?: string })
    .resourcesPath,
  executablePath = process.execPath,
  workingDirectory = process.cwd(),
  env = process.env,
}: MinerExecutableCandidateOptions = {}) => {
  const isWindows = platform === 'win32';
  const platformPath = isWindows ? path.win32 : path.posix;
  const executableName = isWindows
    ? 'qsdmminer-console.exe'
    : 'qsdmminer-console';
  const nativePlatformDirectory = isWindows ? 'windows' : platform;
  return [
    env.QSDM_MINER_EXE,
    resourcesPath
      ? platformPath.join(resourcesPath, 'miner', executableName)
      : '',
    platformPath.join(
      platformPath.dirname(executablePath),
      'resources',
      'miner',
      executableName
    ),
    platformPath.join(
      workingDirectory,
      'native',
      nativePlatformDirectory,
      'x64',
      executableName
    ),
    findFromAncestors(
      platformPath.join('QSDM', 'source', executableName),
      workingDirectory
    ),
  ].filter(Boolean) as string[];
};

export const getMinerExecutablePath = () => {
  const minerExecutable = getMinerExecutableCandidates().find((candidate) =>
    fs.existsSync(candidate)
  );

  if (!minerExecutable) {
    throw new Error(
      `QSDM Miner is missing from this ${process.platform} Hive installation. Reinstall the latest QSDM Hive build or set QSDM_MINER_EXE to qsdmminer-console.`
    );
  }

  return minerExecutable;
};

type QsdmMinerLaunchArgsOptions = {
  configPath: string;
  logPath: string;
  env?: NodeJS.ProcessEnv;
};

const isEnabledEnvironmentFlag = (value?: string) =>
  /^(1|true|yes|on)$/i.test(value?.trim() || '');

// The Hive miner uses the packaged CUDA proof solver. --idle-only watches the
// same GPU and can intentionally pause proof work while another application is
// active, so retain it as an explicit operator opt-in rather than a default.
export const buildQsdmMinerLaunchArgs = ({
  configPath,
  logPath,
  env = process.env,
}: QsdmMinerLaunchArgsOptions) => {
  const args = [
    `--config=${configPath}`,
    `--log-file=${logPath}`,
    '--log-size-mb=10',
    '--log-keep=5',
    '--plain',
    '--compute-backend=cuda',
  ];

  if (isEnabledEnvironmentFlag(env.QSDM_MINER_IDLE_ONLY)) {
    args.splice(1, 0, '--idle-only', '--idle-threshold=10', '--idle-grace=60s');
  }

  return args;
};

const getMinerConfigPath = () =>
  process.env.QSDM_MINER_CONFIG ||
  path.join(os.homedir(), '.qsdm', 'miner.toml');

const getMinerLogPath = () =>
  process.env.QSDM_MINER_LOG || path.join(os.homedir(), '.qsdm', 'miner.log');

export const resolveQsdmMinerValidatorBaseUrl = ({
  runtimeApiUrl = getQsdmRuntimeCoreApiUrl(),
  canonicalApiUrl = QSDM_CANONICAL_API_URL,
  configuredUrl = process.env.QSDM_MINER_VALIDATOR_URL,
}: {
  runtimeApiUrl?: string;
  canonicalApiUrl?: string;
  configuredUrl?: string;
} = {}) => {
  const explicitUrl = configuredUrl?.trim();
  const connectionMode = getQsdmCoreConnectionMode(runtimeApiUrl);
  const targetUrl =
    explicitUrl ||
    (connectionMode === 'custom' ? runtimeApiUrl : canonicalApiUrl);

  return targetUrl.replace(/\/api\/v1\/?$/i, '').replace(/\/+$/, '');
};

const readMinerConfigStringValue = (config: string, key: string) => {
  const escapedKey = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const matcher = new RegExp(
    `^\\s*${escapedKey}\\s*=\\s*(?:"([^"]+)"|'([^']+)'|([^#\\r\\n]+))`,
    'm'
  );
  const match = config.match(matcher);
  return (match?.[1] || match?.[2] || match?.[3] || '').trim();
};

const escapeTomlString = (value: string) =>
  value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');

const setMinerConfigStringValue = (
  config: string,
  key: string,
  value: string
) => {
  const escapedKey = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const line = `${key} = "${escapeTomlString(value)}"`;
  const matcher = new RegExp(
    `^\\s*${escapedKey}\\s*=\\s*(?:"[^"]*"|'[^']*'|[^#\\r\\n]*)`,
    'm'
  );

  if (matcher.test(config)) {
    return config.replace(matcher, line);
  }

  const separator =
    config.trim().length > 0 && !config.endsWith('\n') ? '\n' : '';
  return `${config}${separator}${line}\n`;
};

export const getQsdmMinerRewardAddressInfo =
  (): QsdmMinerRewardAddressInfo | null => {
    const signer = getQsdmTaskActionSender();
    const configPath = getMinerConfigPath();
    const envAddress = process.env.QSDM_MINER_REWARD_ADDRESS?.trim();
    if (envAddress) {
      return {
        address: envAddress,
        source: 'env',
        signer,
        configPath,
      };
    }

    if (!fs.existsSync(configPath)) {
      return signer
        ? {
            address: signer,
            source: 'signer-fallback',
            signer,
            configPath,
          }
        : null;
    }

    try {
      const config = fs.readFileSync(configPath, 'utf8');
      const configAddress = readMinerConfigStringValue(
        config,
        'reward_address'
      );
      if (configAddress) {
        return {
          address: configAddress,
          source: 'miner-config',
          signer,
          configPath,
        };
      }
    } catch {
      // Fall through to the active signer as the safest visible default.
    }

    return signer
      ? {
          address: signer,
          source: 'signer-fallback',
          signer,
          configPath,
        }
      : null;
  };

export const getQsdmMinerRewardAddress = () =>
  getQsdmMinerRewardAddressInfo()?.address || '';

export const setQsdmMinerRewardAddressToSigner =
  (): QsdmMinerRewardAddressUpdateResult => {
    const signer = getQsdmTaskActionSender();
    if (!signer) {
      throw new Error(
        'QSDM signer is not configured. Import or create a QSDM wallet before aligning miner rewards.'
      );
    }

    const envAddress = process.env.QSDM_MINER_REWARD_ADDRESS?.trim();
    if (envAddress && envAddress.toLowerCase() !== signer.toLowerCase()) {
      throw new Error(
        'QSDM_MINER_REWARD_ADDRESS is set in the environment and overrides miner.toml. Clear that environment variable or set it to the Hive signer address.'
      );
    }

    const configPath = getMinerConfigPath();
    fs.mkdirSync(path.dirname(configPath), { recursive: true });

    const previousConfig = fs.existsSync(configPath)
      ? fs.readFileSync(configPath, 'utf8')
      : '';
    const currentAddress = readMinerConfigStringValue(
      previousConfig,
      'reward_address'
    );
    const currentValidator = readMinerConfigStringValue(
      previousConfig,
      'validator_url'
    );
    const desiredValidator = resolveQsdmMinerValidatorBaseUrl();
    let updatedConfig = setMinerConfigStringValue(
      previousConfig,
      'reward_address',
      signer
    );
    if (currentValidator !== desiredValidator) {
      updatedConfig = setMinerConfigStringValue(
        updatedConfig,
        'validator_url',
        desiredValidator
      );
    }

    let backupPath: string | undefined;
    if (
      fs.existsSync(configPath) &&
      (currentAddress !== signer || currentValidator !== desiredValidator)
    ) {
      backupPath = `${configPath}.bak-${Date.now()}`;
      fs.copyFileSync(configPath, backupPath);
    }

    if (updatedConfig !== previousConfig) {
      fs.writeFileSync(configPath, updatedConfig, 'utf8');
    }

    const info = getQsdmMinerRewardAddressInfo();
    if (!info) {
      throw new Error('Unable to read QSDM miner reward address after update.');
    }

    return {
      ...info,
      updated: updatedConfig !== previousConfig,
      backupPath,
      requiresMinerRestart: updatedConfig !== previousConfig,
    };
  };

const runProcessProbe = (command: string, args: string[]) =>
  new Promise<string>((resolve, reject) => {
    execFile(
      command,
      args,
      {
        windowsHide: true,
        timeout: 5000,
        maxBuffer: 1024 * 1024,
      },
      (error, stdout) => {
        if (error) {
          reject(error);
          return;
        }
        resolve(stdout.toString());
      }
    );
  });

const normalizeExecutablePath = (value?: string) =>
  (value || '').trim().replace(/\//g, '\\').toLowerCase();

const sortMinerProcessesByPreference = (processes: QsdmMinerProcessInfo[]) => {
  const knownExecutables = getMinerExecutableCandidates().map(
    normalizeExecutablePath
  );

  return [...processes].sort((left, right) => {
    const leftKnown =
      left.executablePath &&
      knownExecutables.includes(normalizeExecutablePath(left.executablePath));
    const rightKnown =
      right.executablePath &&
      knownExecutables.includes(normalizeExecutablePath(right.executablePath));

    if (leftKnown !== rightKnown) {
      return leftKnown ? -1 : 1;
    }

    const leftStarted = Date.parse(left.startTime || '');
    const rightStarted = Date.parse(right.startTime || '');
    if (Number.isFinite(leftStarted) && Number.isFinite(rightStarted)) {
      return leftStarted - rightStarted;
    }

    return left.pid - right.pid;
  });
};

const parseWindowsMinerProcesses = (output: string): QsdmMinerProcessInfo[] => {
  const trimmed = output.trim();
  if (!trimmed) {
    return [];
  }

  const parsed = JSON.parse(trimmed);
  const records = (Array.isArray(parsed) ? parsed : [parsed]) as Record<
    string,
    unknown
  >[];

  return records
    .map((record) => ({
      pid: Number(record.ProcessId || record.Id),
      executablePath:
        typeof (record.ExecutablePath || record.Path) === 'string'
          ? ((record.ExecutablePath || record.Path) as string)
          : undefined,
      commandLine:
        typeof record.CommandLine === 'string' ? record.CommandLine : undefined,
      startTime:
        typeof record.StartTime === 'string' ? record.StartTime : undefined,
    }))
    .filter((record) => Number.isFinite(record.pid) && record.pid > 0);
};

const getWindowsMinerProcesses = async () => {
  const output = await runProcessProbe('powershell.exe', [
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-Command',
    "$ErrorActionPreference='SilentlyContinue'; Get-Process -Name qsdmminer,qsdmminer-console -ErrorAction SilentlyContinue | Select-Object Id,Path,ProcessName,StartTime | ConvertTo-Json -Compress; exit 0",
  ]);

  return parseWindowsMinerProcesses(output);
};

const parseUnixMinerProcesses = (output: string): QsdmMinerProcessInfo[] =>
  output
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => /\bqsdmminer(?:-console)?\b/.test(line))
    .map((line) => {
      const match = line.match(/^(\d+)\s+(\S+)\s*(.*)$/);
      return {
        pid: match ? Number(match[1]) : 0,
        executablePath: match?.[2],
        commandLine: match?.[3],
      };
    })
    .filter((record) => Number.isFinite(record.pid) && record.pid > 0);

const getUnixMinerProcesses = async () => {
  const output = await runProcessProbe('ps', ['-eo', 'pid=,comm=,args=']);
  return parseUnixMinerProcesses(output);
};

export const getQsdmMinerSystemProcesses = async (): Promise<
  QsdmMinerProcessInfo[]
> => {
  try {
    return process.platform === 'win32'
      ? sortMinerProcessesByPreference(await getWindowsMinerProcesses())
      : sortMinerProcessesByPreference(await getUnixMinerProcesses());
  } catch (error: any) {
    writeTaskLog(
      QSDM_MINER_SYSTEM_TASK_ID,
      `Could not probe existing QSDM miner process: ${error?.message || error}`
    );
    return [];
  }
};

export const isMinerProcessFromCandidates = (
  processInfo: QsdmMinerProcessInfo,
  candidates: string[]
) => {
  const executablePath = processInfo.executablePath
    ? normalizeExecutablePath(processInfo.executablePath)
    : '';
  const commandLine = normalizeExecutablePath(processInfo.commandLine || '');

  return candidates.some((candidate) => {
    const normalized = normalizeExecutablePath(candidate);
    return (
      (!!executablePath && executablePath === normalized) ||
      (!!commandLine && commandLine.includes(normalized))
    );
  });
};

// AppImage mount paths change on every Hive launch. A miner inherited from a
// previous mount is still ours when it is the console miner using Hive's
// config and the required CUDA backend. Enrollment is checked separately
// before this process can be adopted into the active wallet session.
export const isPortableHiveMinerProcess = (
  processInfo: QsdmMinerProcessInfo,
  configPath = getMinerConfigPath()
) => {
  const executablePath = normalizeExecutablePath(processInfo.executablePath);
  const commandLine = normalizeExecutablePath(processInfo.commandLine);
  const executableName = executablePath.split('\\').pop() || '';
  const consoleMinerPattern =
    /(^|[\\\s"])(qsdmminer-console)(?:\.exe)?(?=$|[\s"])/;
  const normalizedConfigPath = normalizeExecutablePath(configPath);

  return (
    (executableName === 'qsdmminer-console' ||
      executableName === 'qsdmminer-console.exe' ||
      consoleMinerPattern.test(commandLine)) &&
    commandLine.includes(`--config=${normalizedConfigPath}`) &&
    commandLine.includes('--compute-backend=cuda')
  );
};

const getManagedQsdmMinerSystemProcesses = async () => {
  const candidates = getMinerExecutableCandidates().filter((candidate) =>
    fs.existsSync(candidate)
  );
  return (await getQsdmMinerSystemProcesses()).filter(
    (processInfo) =>
      isMinerProcessFromCandidates(processInfo, candidates) ||
      (process.platform !== 'win32' && isPortableHiveMinerProcess(processInfo))
  );
};

export const getQsdmMinerSystemProcessInfo =
  async (): Promise<QsdmMinerProcessInfo | null> =>
    (await getManagedQsdmMinerSystemProcesses())[0] || null;

export const stopExtraQsdmMinerSystemProcesses = async (keepPid?: number) => {
  const processes = await getManagedQsdmMinerSystemProcesses();
  const selectedKeepPid = keepPid || processes[0]?.pid;
  const extras = processes.filter(
    (processInfo) => processInfo.pid !== selectedKeepPid
  );

  extras.forEach((processInfo) => {
    try {
      process.kill(processInfo.pid, 'SIGTERM');
      writeTaskLog(
        QSDM_MINER_SYSTEM_TASK_ID,
        `Stopped duplicate QSDM miner process pid=${processInfo.pid}`
      );
    } catch (error: any) {
      writeTaskLog(
        QSDM_MINER_SYSTEM_TASK_ID,
        `Could not stop duplicate QSDM miner process pid=${processInfo.pid}: ${
          error?.message || error
        }`
      );
    }
  });
};

const isPidAlive = (pid: number) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error: any) {
    // On Windows, a process can be alive but protected from the current user.
    // process.kill(pid, 0) reports EPERM in that case; only ESRCH means absent.
    return error?.code === 'EPERM';
  }
};

const createAdoptedProcessChild = (
  taskId: string,
  processInfo: QsdmMinerProcessInfo
): ChildProcess => {
  const child = new EventEmitter() as ChildProcess;
  let settled = false;
  let monitor: NodeJS.Timeout | null = null;

  const emitExitOnce = () => {
    if (settled) {
      return;
    }
    settled = true;
    if (monitor) {
      clearInterval(monitor);
      monitor = null;
    }
    child.emit('exit', null, null);
    child.emit('close', null, null);
  };

  Object.assign(child, {
    pid: processInfo.pid,
    spawnargs: [],
    spawnfile: processInfo.executablePath || 'qsdmminer.exe',
    stdin: null,
    stdout: null,
    stderr: null,
    stdio: [null, null, null],
    connected: false,
    killed: false,
    exitCode: null,
    signalCode: null,
    kill: (signal?: NodeJS.Signals | number) => {
      (child as any).killed = true;
      try {
        process.kill(
          processInfo.pid,
          typeof signal === 'string' ? signal : 'SIGTERM'
        );
        emitExitOnce();
        return true;
      } catch (error: any) {
        writeTaskLog(
          taskId,
          `Could not stop adopted process ${processInfo.pid}: ${
            error?.message || error
          }`
        );
        emitExitOnce();
        return false;
      }
    },
  });

  monitor = setInterval(() => {
    if (!isPidAlive(processInfo.pid)) {
      writeTaskLog(
        taskId,
        `Adopted QSDM miner process ${processInfo.pid} exited`
      );
      emitExitOnce();
    }
  }, 10000);
  monitor.unref();

  return child;
};

export const adoptQsdmMinerSystemProcess = (
  processInfo: QsdmMinerProcessInfo
): {
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
} => {
  const logPath = getMinerLogPath();
  const executablePath = processInfo.executablePath || getMinerExecutablePath();

  fs.mkdirSync(path.dirname(logPath), { recursive: true });
  fs.mkdirSync(
    path.join(getAppDataPath(), 'namespace', QSDM_MINER_SYSTEM_TASK_ID),
    { recursive: true }
  );

  writeTaskLog(
    QSDM_MINER_SYSTEM_TASK_ID,
    `Adopting existing QSDM Miner process pid=${processInfo.pid} path="${
      processInfo.executablePath || 'unknown'
    }"`
  );

  return {
    child: createAdoptedProcessChild(QSDM_MINER_SYSTEM_TASK_ID, processInfo),
    secret: cryptoRandomString({ length: 20 }),
    logPath,
    executablePath,
  };
};

export const startOrAdoptQsdmMinerSystemProcess = async (): Promise<{
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
}> => {
  const processInfo = await getQsdmMinerSystemProcessInfo();
  if (processInfo) {
    await assertQsdmMinerEnrollmentReady();
    await stopExtraQsdmMinerSystemProcesses(processInfo.pid);
    return adoptQsdmMinerSystemProcess(processInfo);
  }

  const unmanagedProcesses = await getQsdmMinerSystemProcesses();
  if (unmanagedProcesses.length > 0) {
    writeTaskLog(
      QSDM_MINER_SYSTEM_TASK_ID,
      `Ignoring ${unmanagedProcesses.length} legacy or externally managed miner process(es); Hive only adopts its packaged CUDA miner.`
    );
  }

  try {
    return await startQsdmMinerSystemProcess();
  } catch (error) {
    // A previous AppImage miner can become visible between the initial probe
    // and spawn, or can outlive an unclean Electron shutdown. If the new
    // process loses that single-instance race, adopt the compatible process
    // instead of reporting a false configuration failure while it mines.
    const collisionProcess = await getQsdmMinerSystemProcessInfo();
    if (!collisionProcess) {
      throw error;
    }

    await assertQsdmMinerEnrollmentReady();
    await stopExtraQsdmMinerSystemProcesses(collisionProcess.pid);
    writeTaskLog(
      QSDM_MINER_SYSTEM_TASK_ID,
      `Miner launch collided with an existing compatible process; adopting pid=${collisionProcess.pid}`
    );
    return adoptQsdmMinerSystemProcess(collisionProcess);
  }
};

const writeTaskLog = (taskId: string, message: string) => {
  if (!message) {
    return;
  }

  const taskDir = path.join(getAppDataPath(), 'namespace', taskId);
  fs.mkdirSync(taskDir, { recursive: true });
  fs.appendFileSync(
    path.join(taskDir, 'task.log'),
    `[${new Date().toISOString()}] ${message}\n`
  );
};

const getEdgeWorkerDirectory = (taskId: string) =>
  path.join(getAppDataPath(), 'namespace', taskId);

const getEdgeWorkerScriptPath = (taskId: string) =>
  path.join(getEdgeWorkerDirectory(taskId), 'edge-worker.js');

const ensureEdgeWorkerScript = (taskId: string) => {
  const workerScriptPath = getEdgeWorkerScriptPath(taskId);
  fs.mkdirSync(path.dirname(workerScriptPath), { recursive: true });
  fs.writeFileSync(workerScriptPath, createQsdmEdgeWorkerScript(), 'utf8');
  return workerScriptPath;
};

const getMotherHiveDirectory = () =>
  path.join(getAppDataPath(), 'namespace', QSDM_MOTHER_HIVE_SYSTEM_TASK_ID);

const ensureMotherHiveScript = () => {
  const scriptPath = path.join(getMotherHiveDirectory(), 'mother-hive.js');
  fs.mkdirSync(path.dirname(scriptPath), { recursive: true });
  fs.writeFileSync(scriptPath, createQsdmMotherHiveScript(), 'utf8');
  return scriptPath;
};

export const assertQsdmMotherHiveConfigured = () => {
  const tokenFile = getDefaultEdgeRelayTokenFile();
  if (!tokenFile || !fs.existsSync(tokenFile)) {
    throw new Error(
      'Mother Hive Task requires a paired Relay. Open QSDM Edge Control on the Relay computer and choose "Use this QSDM Hive as Mother Hive" first.'
    );
  }
  return tokenFile;
};

type EdgeRelayStatusResponse = {
  relay_id?: string;
  coordinator_id?: string;
  active_leases?: number;
  workers?: Array<{
    worker_id?: string;
    hostname?: string;
    last_seen_at?: string;
    completed_jobs?: number;
    capabilities?: {
      cpu_threads?: number;
      ram_mib?: number;
      resources?: string[];
      gpus?: Array<{
        memory_mib?: number;
      }>;
    };
  }>;
  receipt_counts?: {
    cpu?: number;
    gpu?: number;
    ram?: number;
  };
  policy?: {
    cpu_percent?: number;
    gpu_percent?: number;
    ram_percent?: number;
  };
};

const motherHiveRevenuePolicy = () => ({
  contributorPercent: QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT,
  motherHivePercent: QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT,
  ecosystemPercent: QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT,
  ecosystemWalletAddress: undefined,
  settlementActive: false,
  settlementReason:
    'Settlement is locked: Relay jobs are verified locally, but QSDM Core does not yet verify a Relay public-key signature, bind receipt owners to payout wallets, reject receipt reuse globally, or enforce the 70/15/15 split from funded workload escrow.',
});

const loadEdgeRelayMotherToken = (tokenFile: string) => {
  const raw = fs.readFileSync(tokenFile, 'utf8').trim();
  if (/^[0-9a-fA-F]{64,}$/.test(raw) && raw.length % 2 === 0) {
    return Buffer.from(raw, 'hex');
  }
  const token = Buffer.from(raw, 'utf8');
  if (token.length < 32) {
    throw new Error('Relay Mother Hive credential is invalid');
  }
  return token;
};

const motherHiveWorkerId = () =>
  `hive-${os.hostname()}`.replace(/[^A-Za-z0-9._-]/g, '-').slice(0, 64);

const readEdgeRelayStatus = async (): Promise<EdgeRelayStatusResponse> => {
  const relayUrl = getDefaultEdgeRelayURL();
  const tokenFile = getDefaultEdgeRelayTokenFile();
  if (!tokenFile || !fs.existsSync(tokenFile)) {
    throw new Error(
      'QSDM Hive is not paired with a Relay. Open Edge Control on the Relay computer and connect this QSDM Hive as Mother Hive.'
    );
  }
  const target = new URL('/v1/status', relayUrl);
  if (target.protocol !== 'http:' && target.protocol !== 'https:') {
    throw new Error('Relay URL must use HTTP or HTTPS');
  }
  const token = loadEdgeRelayMotherToken(tokenFile);
  const timestamp = String(Math.floor(Date.now() / 1000));
  const nonce = randomBytes(16).toString('hex');
  const workerId = motherHiveWorkerId();
  const bodyHash = createHash('sha256').update('').digest('hex');
  const canonical = [
    'GET',
    target.pathname,
    timestamp,
    nonce,
    workerId,
    bodyHash,
  ].join('\n');
  const signature = createHmac('sha256', token).update(canonical).digest('hex');
  const response = await axios.get<EdgeRelayStatusResponse>(target.href, {
    headers: {
      'X-QSDM-Worker-ID': workerId,
      'X-QSDM-Timestamp': timestamp,
      'X-QSDM-Nonce': nonce,
      'X-QSDM-Signature': signature,
    },
    timeout: 5000,
    maxContentLength: 256 * 1024,
    responseType: 'json',
  });
  return response.data;
};

export const getQsdmMotherHiveStatus =
  async (): Promise<QsdmMotherHiveStatusResponse> => {
    const relayUrl = getDefaultEdgeRelayURL();
    const tokenFile = getDefaultEdgeRelayTokenFile();
    const base: QsdmMotherHiveStatusResponse = {
      configured: Boolean(tokenFile && fs.existsSync(tokenFile)),
      connected: false,
      role: 'qsdm-hive-mother',
      relayUrl,
      workers: [],
      onlineWorkers: 0,
      pooledCpuThreads: 0,
      pooledRamMiB: 0,
      pooledGpuCount: 0,
      pooledGpuMemoryMiB: 0,
      activeJobs: 0,
      verifiedReceipts: { cpu: 0, gpu: 0, ram: 0 },
      revenuePolicy: motherHiveRevenuePolicy(),
      workloadMode: 'qsdm-approved-distributed-jobs',
      checkedAt: new Date().toISOString(),
    };

    try {
      const status = await readEdgeRelayStatus();
      const now = Date.now();
      const workers = (status.workers || []).map((worker) => {
        const resources = worker.capabilities?.resources || [];
        const gpus = resources.includes('gpu')
          ? worker.capabilities?.gpus || []
          : [];
        const lastSeen = Date.parse(worker.last_seen_at || '');
        return {
          workerId: worker.worker_id || 'unknown',
          hostname: worker.hostname || worker.worker_id || 'Agent',
          online: Number.isFinite(lastSeen) && now - lastSeen <= 120000,
          cpuThreads: resources.includes('cpu')
            ? Math.max(0, Number(worker.capabilities?.cpu_threads) || 0)
            : 0,
          ramMiB: resources.includes('ram')
            ? Math.max(0, Number(worker.capabilities?.ram_mib) || 0)
            : 0,
          gpuCount: gpus.length,
          gpuMemoryMiB: gpus.reduce(
            (total, gpu) => total + Math.max(0, Number(gpu.memory_mib) || 0),
            0
          ),
          completedJobs: Math.max(0, Number(worker.completed_jobs) || 0),
          lastSeenAt: worker.last_seen_at,
        };
      });
      const onlineWorkers = workers.filter((worker) => worker.online);
      return {
        ...base,
        connected: true,
        relayId: status.relay_id || status.coordinator_id,
        workers,
        onlineWorkers: onlineWorkers.length,
        pooledCpuThreads: onlineWorkers.reduce(
          (total, worker) => total + worker.cpuThreads,
          0
        ),
        pooledRamMiB: onlineWorkers.reduce(
          (total, worker) => total + worker.ramMiB,
          0
        ),
        pooledGpuCount: onlineWorkers.reduce(
          (total, worker) => total + worker.gpuCount,
          0
        ),
        pooledGpuMemoryMiB: onlineWorkers.reduce(
          (total, worker) => total + worker.gpuMemoryMiB,
          0
        ),
        activeJobs: Math.max(0, Number(status.active_leases) || 0),
        verifiedReceipts: {
          cpu: Math.max(0, Number(status.receipt_counts?.cpu) || 0),
          gpu: Math.max(0, Number(status.receipt_counts?.gpu) || 0),
          ram: Math.max(0, Number(status.receipt_counts?.ram) || 0),
        },
        relayPolicy: {
          cpuPercent: Math.max(0, Number(status.policy?.cpu_percent) || 0),
          gpuPercent: Math.max(0, Number(status.policy?.gpu_percent) || 0),
          ramPercent: Math.max(0, Number(status.policy?.ram_percent) || 0),
        },
        detail:
          onlineWorkers.length > 0
            ? 'Pooled resources are ready for QSDM-approved distributed workloads.'
            : 'Relay is connected, but no Agent has checked in during the last two minutes.',
        checkedAt: new Date().toISOString(),
      };
    } catch (error: any) {
      return {
        ...base,
        detail: error?.message || 'Relay status is unavailable.',
        checkedAt: new Date().toISOString(),
      };
    }
  };

const getQsdmEdgeGPUHelperPath = () => {
  const executableName =
    process.platform === 'win32'
      ? 'qsdm-edge-gpu-helper.exe'
      : 'qsdm-edge-gpu-helper';
  const candidates = [
    process.env.QSDM_EDGE_GPU_HELPER,
    process.resourcesPath
      ? path.join(process.resourcesPath, 'edge', executableName)
      : '',
    path.join(
      path.dirname(process.execPath),
      'resources',
      'edge',
      executableName
    ),
    path.join(path.dirname(process.execPath), executableName),
  ].filter(Boolean) as string[];
  return candidates.find((candidate) => fs.existsSync(candidate)) || '';
};

export const startQsdmEdgeWorkerSystemProcess = (
  taskId = QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  taskInfo: Pick<
    RawTaskData,
    | 'round_time'
    | 'starting_slot'
    | 'bounty_amount_per_round'
    | 'reward_pool_amount'
  > = getQsdmSystemTaskById(taskId) || createQsdmEdgeWorkerSystemTask()
): {
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
} => {
  const resource = getQsdmEdgeWorkerResource(taskId);
  if (!resource) {
    throw new Error(`Unsupported QSDM Edge Worker task ${taskId}`);
  }
  const gpuHelperPath = getQsdmEdgeGPUHelperPath();
  if (resource === 'gpu' && !gpuHelperPath) {
    throw new Error(
      'QSDM Edge Worker GPU requires the packaged CUDA helper. Reinstall the latest QSDM Hive build or configure QSDM_EDGE_GPU_HELPER.'
    );
  }
  const executablePath = process.execPath;
  const workerScriptPath = ensureEdgeWorkerScript(taskId);
  const logPath = path.join(getEdgeWorkerDirectory(taskId), 'task.log');
  const outputState: EdgeWorkerOutputState = {
    pending: '',
    taskId,
    resource,
    task: taskInfo,
    submittedRounds: new Set<number>(),
    submitting: false,
  };

  writeTaskLog(
    taskId,
    `Starting QSDM Edge Worker resource=${resource}: "${executablePath}" "${workerScriptPath}" round_time_blocks=${taskInfo.round_time} starting_slot=${taskInfo.starting_slot}`
  );

  const child = spawn(executablePath, [workerScriptPath], {
    detached: false,
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
    env: {
      ...process.env,
      ELECTRON_RUN_AS_NODE: '1',
      QSDM_EDGE_TASK_ID: taskId,
      QSDM_EDGE_RESOURCE: resource,
      QSDM_EDGE_RELAY_URL: getDefaultEdgeRelayURL(),
      QSDM_EDGE_RELAY_TOKEN_FILE: getDefaultEdgeRelayTokenFile(),
      QSDM_EDGE_GPU_HELPER: gpuHelperPath,
      QSDM_TASK_ACTION_SENDER: getQsdmTaskActionSender(),
    },
  });

  child.stdout?.on('data', (data) =>
    handleQsdmEdgeWorkerOutput(data, outputState)
  );
  child.stderr?.on('data', (data) =>
    writeTaskLog(taskId, data.toString().trimEnd())
  );
  child.on('error', (error) =>
    writeTaskLog(taskId, `Edge Worker process error: ${error.message}`)
  );

  return {
    child,
    secret: cryptoRandomString({ length: 20 }),
    logPath,
    executablePath: workerScriptPath,
  };
};

export const startQsdmMotherHiveSystemProcess = (): {
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
} => {
  const tokenFile = assertQsdmMotherHiveConfigured();
  const executablePath = process.execPath;
  const scriptPath = ensureMotherHiveScript();
  const logPath = path.join(getMotherHiveDirectory(), 'task.log');

  writeTaskLog(
    QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
    `Starting QSDM Hive Mother mode relay=${getDefaultEdgeRelayURL()}`
  );
  writeTaskLog(
    QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
    'Pooled resources are available only to QSDM-approved distributed jobs; no remote shell or transparent OS device is exposed.'
  );
  writeTaskLog(
    QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
    `Revenue target contributors=${QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT}% mother_hive=${QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT}% ecosystem=${QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT}% settlement=disabled-pending-chain-verification`
  );

  const child = spawn(executablePath, [scriptPath], {
    detached: false,
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
    env: {
      ...process.env,
      ELECTRON_RUN_AS_NODE: '1',
      QSDM_EDGE_RELAY_URL: getDefaultEdgeRelayURL(),
      QSDM_EDGE_RELAY_TOKEN_FILE: tokenFile,
    },
  });

  child.stdout?.on('data', (data) =>
    writeTaskLog(QSDM_MOTHER_HIVE_SYSTEM_TASK_ID, data.toString().trimEnd())
  );
  child.stderr?.on('data', (data) =>
    writeTaskLog(QSDM_MOTHER_HIVE_SYSTEM_TASK_ID, data.toString().trimEnd())
  );
  child.on('error', (error) =>
    writeTaskLog(
      QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
      `Mother Hive process error: ${error.message}`
    )
  );

  return {
    child,
    secret: cryptoRandomString({ length: 20 }),
    logPath,
    executablePath: scriptPath,
  };
};

export const startQsdmSkyFangLinkSystemProcess = (
  taskInfo: Pick<
    RawTaskData,
    | 'round_time'
    | 'starting_slot'
    | 'bounty_amount_per_round'
    | 'reward_pool_amount'
  > = createQsdmSkyFangLinkSystemTask()
): {
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
} => {
  const executablePath = process.execPath;
  const workerScriptPath = ensureSkyFangLinkScript();
  const logPath = path.join(getSkyFangLinkDirectory(), 'task.log');
  const outputState: SkyFangLinkOutputState = {
    pending: '',
    task: taskInfo,
    submittedRounds: new Set<number>(),
    submitting: false,
  };

  writeTaskLog(
    QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
    `Starting QSDM Sky Fang Link verifier: "${executablePath}" "${workerScriptPath}" base_url=${getSkyFangBaseUrl()}`
  );

  const child = spawn(executablePath, [workerScriptPath], {
    detached: false,
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
    env: {
      ...process.env,
      ELECTRON_RUN_AS_NODE: '1',
      QSDM_TASK_ACTION_SENDER: getQsdmTaskActionSender(),
    },
  });

  child.stdout?.on('data', (data) =>
    handleQsdmSkyFangLinkOutput(data, outputState)
  );
  child.stderr?.on('data', (data) =>
    writeTaskLog(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID, data.toString().trimEnd())
  );
  child.on('error', (error) =>
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang Link verifier process error: ${error.message}`
    )
  );

  return {
    child,
    secret: cryptoRandomString({ length: 20 }),
    logPath,
    executablePath: workerScriptPath,
  };
};

const waitForChildProcessStartup = (
  child: ChildProcess,
  label: string,
  logPath: string
) =>
  new Promise<void>((resolve, reject) => {
    let settled = false;

    const cleanup = () => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timeout);
      child.removeListener('exit', onExit);
      child.removeListener('error', onError);
    };

    const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
      cleanup();
      reject(
        new Error(buildProcessStartupExitDetail(label, code, signal, logPath))
      );
    };

    const onError = (error: Error) => {
      cleanup();
      reject(error);
    };

    const timeout = setTimeout(() => {
      cleanup();
      resolve();
    }, 2000);
    timeout.unref();

    child.once('exit', onExit);
    child.once('error', onError);
  });

const readProcessLogTail = (logPath: string, maxLines = 12) => {
  try {
    if (!fs.existsSync(logPath)) {
      return '';
    }

    return fs
      .readFileSync(logPath, 'utf8')
      .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .slice(-maxLines)
      .join('\n')
      .replace(/(hmac(?:[_ -]?key)?\s*[=:]\s*)\S+/gi, '$1[redacted]')
      .slice(-2000);
  } catch {
    return '';
  }
};

export const buildProcessStartupExitDetail = (
  label: string,
  code: number | null,
  signal: NodeJS.Signals | null,
  logPath: string
) => {
  const protocolDetail =
    label === 'QSDM Miner' && code === 3
      ? ' The validator refused legacy mining protocol v1. QSDM protocol mining requires a compatible NVIDIA GPU, protocol="v2" identity/HMAC configuration, and a separately confirmed on-chain miner enrollment bond.'
      : label === 'QSDM Miner' && code === 2
      ? ' The QSDM Miner configuration is incomplete or invalid.'
      : '';
  const logTail = readProcessLogTail(logPath);
  const logDetail = logTail ? `\nMiner log tail:\n${logTail}` : '';

  return `${label} exited during startup with code ${code} and signal ${signal}.${protocolDetail} Log: ${logPath}.${logDetail}`;
};

export const startQsdmMinerSystemProcess = async (): Promise<{
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
}> => {
  await prepareQsdmMinerV2Config();
  await assertQsdmMinerEnrollmentReady();
  const executablePath = getMinerExecutablePath();
  const configUpdate = setQsdmMinerRewardAddressToSigner();
  const configPath = configUpdate.configPath;
  const logPath = getMinerLogPath();

  if (configUpdate.updated) {
    writeTaskLog(
      QSDM_MINER_SYSTEM_TASK_ID,
      `Aligned miner config with the active Hive signer at ${configPath}`
    );
  }

  fs.mkdirSync(path.dirname(logPath), { recursive: true });
  fs.mkdirSync(
    path.join(getAppDataPath(), 'namespace', QSDM_MINER_SYSTEM_TASK_ID),
    { recursive: true }
  );

  const args = buildQsdmMinerLaunchArgs({ configPath, logPath });

  writeTaskLog(
    QSDM_MINER_SYSTEM_TASK_ID,
    'Miner compute backend: CUDA SHA3 proof solver. Startup fails closed if the packaged solver or a compatible NVIDIA GPU is unavailable.'
  );
  if (!args.includes('--idle-only')) {
    writeTaskLog(
      QSDM_MINER_SYSTEM_TASK_ID,
      'Continuous CUDA solving enabled. Set QSDM_MINER_IDLE_ONLY=1 only if mining should pause while other GPU applications are active.'
    );
  }

  writeTaskLog(
    QSDM_MINER_SYSTEM_TASK_ID,
    `Starting QSDM Miner in user mode: "${executablePath}" ${args.join(' ')}`
  );

  const child = spawn(executablePath, args, {
    detached: false,
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });

  child.stdout?.on('data', (data) =>
    writeTaskLog(QSDM_MINER_SYSTEM_TASK_ID, data.toString().trimEnd())
  );
  child.stderr?.on('data', (data) =>
    writeTaskLog(QSDM_MINER_SYSTEM_TASK_ID, data.toString().trimEnd())
  );
  child.on('error', (error) =>
    writeTaskLog(
      QSDM_MINER_SYSTEM_TASK_ID,
      `Miner process error: ${error.message}`
    )
  );

  await waitForChildProcessStartup(child, 'QSDM Miner', logPath);

  return {
    child,
    secret: cryptoRandomString({ length: 20 }),
    logPath,
    executablePath,
  };
};
