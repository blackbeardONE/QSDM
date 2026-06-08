import { ChildProcess, execFile, spawn } from 'child_process';
import { createHash } from 'crypto';
import { EventEmitter } from 'events';
import fs from 'fs';
import os from 'os';
import path from 'path';

import axios from 'axios';
import cryptoRandomString from 'crypto-random-string';

import { PublicKey } from 'vendor/qsdm-chain/web3';
import {
  isQsdmEdgeWorkerSystemTaskId,
  isQsdmMinerSystemTaskId,
  isQsdmSkyFangLinkSystemTaskId,
  isQsdmSystemTaskId,
  QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT,
  QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
  QSDM_MINER_MIN_STAKE_AMOUNT,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT,
  QSDM_SKYFANG_LINK_REWARD_CELL,
  QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID,
  QSDM_SYSTEM_TASK_IDS,
} from 'config/qsdmSystemTasks';
import { buildQsdmCoreApiUrl } from 'config/qsdm';
import { getAppDataPath } from 'main/node/helpers/getAppDataPath';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import { RawTaskData, RequirementType, TaskMetadata } from 'models';
import {
  QsdmMiningAccountResponse,
  QsdmTaskActionSubmitResponse,
} from 'models/api/qsdm';

export {
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT,
  QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_MINER_MIN_STAKE_AMOUNT,
  QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT,
  QSDM_SKYFANG_LINK_REWARD_CELL,
  QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID,
  QSDM_SYSTEM_TASK_IDS,
} from 'config/qsdmSystemTasks';

const QSDM_MINER_MANAGER = new PublicKey('QsdmSystemMinerManager');
const QSDM_MINER_STAKE_POT = new PublicKey('QsdmSystemMinerStakePot');
const QSDM_EDGE_WORKER_MANAGER = new PublicKey('QsdmEdgeWorkerManager');
const QSDM_EDGE_WORKER_STAKE_POT = new PublicKey('QsdmEdgeWorkerStakePot');
const QSDM_SKYFANG_LINK_MANAGER = new PublicKey('QsdmSkyFangLinkManager');
const QSDM_SKYFANG_LINK_STAKE_POT = new PublicKey('QsdmSkyFangLinkStakePot');

const EMPTY_AUDIT_TRIGGERS = {};
const EMPTY_SUBMISSIONS = {};
const QSDM_TASK_ACTION_COMMIT_POLL_MS = 2000;
const QSDM_TASK_ACTION_COMMIT_TIMEOUT_MS = 45000;

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
  task: Pick<
    RawTaskData,
    | 'round_time'
    | 'starting_slot'
    | 'bounty_amount_per_round'
    | 'reward_pool_amount'
  >;
  submittedRounds: Set<number>;
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
  submitted: boolean;
  child?: ChildProcess;
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
};

export type QsdmSkyFangWalletLinkGateResult = {
  ok: boolean;
  sender: string;
  linkedAt?: string;
  site?: string;
  account?: string;
  username?: string;
  player?: string;
  detail?: string;
};

type QsdmMinerSystemProcessOptions = {
  skipSkyFangLinkGate?: boolean;
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
    'Built-in QSDM system task for running the local miner. This path is only for NVIDIA GPU operators: minimum Turing or newer, CUDA compute capability 7.5+, working NVIDIA driver/nvidia-smi, bonded CELL stake, a Sky Fang account linked to the active QSDM wallet, and explicit opt-in start. Miner rewards come from QSDM protocol mining emission when accepted proofs share a block; there is no separate Hive bounty for this task.',
  submissions: EMPTY_SUBMISSIONS,
  submissions_audit_trigger: EMPTY_AUDIT_TRIGGERS,
  total_stake_amount: 0,
  reward_pool_amount: 0,
  pending_reward_amount: 0,
  total_reward_paid_amount: 0,
  minimum_stake_amount: QSDM_MINER_MIN_STAKE_AMOUNT,
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
    requires_skyfang_wallet_link: true,
    required_task_id: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
  }),
  qsdm_vars: '{}',
  is_migrated: false,
  migrated_to: '',
  allowed_failed_distributions: 0,
  task_type: 'CELL',
  ...overrides,
});

export const createQsdmEdgeWorkerSystemTask = (
  overrides: Partial<RawTaskData> = {}
): RawTaskData => {
  const task: RawTaskData = {
    task_id: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
    task_name: 'QSDM Edge Worker',
    task_manager: QSDM_EDGE_WORKER_MANAGER,
    is_allowlisted: true,
    is_active: true,
    task_audit_program: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
    stake_pot_account: QSDM_EDGE_WORKER_STAKE_POT,
    total_bounty_amount: QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
    bounty_amount_per_round: QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
    current_round: 0,
    available_balances: {},
    stake_list: {},
    task_metadata: QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
    task_description:
      'Built-in CPU-only QSDM task for pooled edge compute. It does not require an NVIDIA GPU. It lets non-GPU users contribute bounded, signed compute proofs against a CELL-funded reward pool.',
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
      cpu_worker: true,
      reward_source: 'funded-pool',
      reward_per_round_cell: QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
      reward_pool_target_cell: QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
    }),
    qsdm_vars: '{}',
    is_migrated: false,
    migrated_to: '',
    allowed_failed_distributions: 0,
    task_type: 'CELL',
    ...overrides,
  };

  return {
    ...task,
    total_bounty_amount:
      Number(task.total_bounty_amount) > 0
        ? task.total_bounty_amount
        : QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
    bounty_amount_per_round:
      Number(task.bounty_amount_per_round) > 0
        ? task.bounty_amount_per_round
        : QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
  };
};

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
      'One-time QSDM Hive task for proving your active QSDM wallet is linked to Sky Fang. First link the wallet at skyfang.xyz/dashboard/qsdm, then run this verifier to submit a signed CELL proof for the onboarding reward.',
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
      one_time_reward: true,
      reward_source: 'funded-pool',
      reward_per_link_cell: QSDM_SKYFANG_LINK_REWARD_CELL,
      reward_pool_target_cell: QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
      skyfang_base_url: 'https://skyfang.xyz',
    }),
    qsdm_vars: '{}',
    is_migrated: false,
    migrated_to: '',
    allowed_failed_distributions: 0,
    task_type: 'CELL',
    ...overrides,
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
        : QSDM_SKYFANG_LINK_REWARD_CELL,
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
    return createQsdmEdgeWorkerSystemTask(overrides);
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
        'Run the official QSDM miner as an opt-in, permanent CELL task for NVIDIA GPU operators only. Miner earnings come from QSDM protocol mining emission when accepted proofs share a block, not from a separate Hive task bounty. Minimum GPU path: NVIDIA Turing or newer, CUDA compute capability 7.5+, a working NVIDIA driver/nvidia-smi, a funded QSDM signer for stake and signed task actions, and a Sky Fang account linked to that active QSDM wallet.',
      repositoryUrl: 'https://qsdm.tech',
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
            'Requires an NVIDIA GPU visible to nvidia-smi. QSDM validates NVIDIA attestation and accepts the current v2 allowlist: Turing, Ampere, Ada Lovelace, or Hopper.',
        },
        {
          type: RequirementType.NETWORK,
          value: 'QSDM Core',
          description:
            'Submits mining work to your configured QSDM validator or gateway.',
        },
        {
          type: RequirementType.NETWORK,
          value: 'Sky Fang wallet link',
          description:
            'Requires the active QSDM signer wallet to be linked to a Sky Fang account before the miner can start.',
        },
        {
          type: RequirementType.ADDON,
          value: 'Protocol mining emission',
          description:
            'Rewards are paid by QSDM Core mining emission for accepted proofs. This miner task does not use an extra Hive-funded bounty pool.',
        },
      ],
      infoUrl: 'https://qsdm.tech',
      tags: [
        'QSDM',
        'CELL',
        'Miner',
        'NVIDIA GPU Required',
        'Protocol Emission',
        'No Hive Bounty',
        'Sky Fang Link Required',
        'CC 7.5+',
        'No Expiry',
      ],
    };
  }

  if (isQsdmEdgeWorkerSystemTask(metadataCID)) {
    return {
      author: 'QSDM',
      description:
        'Run a CPU-only QSDM edge compute worker for users without an NVIDIA GPU. This task contributes bounded, signed CPU work to pooled edge-compute capacity and is the non-GPU participation path for future funded CELL reward pools.',
      repositoryUrl: 'https://qsdm.tech',
      createdAt: 0,
      imageUrl: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      migrationDescription: '',
      requirementsTags: [
        {
          type: RequirementType.OS,
          value: process.platform,
          description:
            'Runs inside QSDM Hive without GPU-specific drivers or hardware.',
        },
        {
          type: RequirementType.CPU,
          value: 'Any modern CPU',
          description:
            'Uses bounded SHA-256 proof work so laptops, desktops, and non-NVIDIA machines can participate safely.',
        },
        {
          type: RequirementType.NETWORK,
          value: 'QSDM Core',
          description:
            'Submits signed CPU proof records to your configured QSDM validator or gateway.',
        },
      ],
      infoUrl: 'https://qsdm.tech',
      tags: [
        'QSDM',
        'CELL',
        'CPU',
        'Edge Worker',
        'No NVIDIA GPU',
        'No Expiry',
      ],
    };
  }

  if (isQsdmSkyFangLinkSystemTask(metadataCID)) {
    return {
      author: 'QSDM',
      description:
        'Link your active QSDM wallet to a Sky Fang account. This task is the account-eligibility gate for Sky Fang CELL rewards: use the one-click QSDM Hive link path, or log in at skyfang.xyz/dashboard/qsdm and link there. Hive verifies the active wallet automatically before submitting the one-time reward proof.',
      repositoryUrl: 'https://skyfang.xyz/dashboard/qsdm',
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
            'No GPU or miner is required. This is an account-linking and eligibility task.',
        },
      ],
      infoUrl: 'https://skyfang.xyz/dashboard/qsdm',
      tags: [
        'QSDM',
        'CELL',
        'Sky Fang',
        'Wallet Link',
        'Mandatory',
        'One-Time Reward',
        'No GPU',
      ],
    };
  }

  return null;
};

const createQsdmEdgeWorkerScript = () => `
const crypto = require('crypto');

const taskId = process.env.QSDM_EDGE_TASK_ID || '${QSDM_EDGE_WORKER_SYSTEM_TASK_ID}';
const sender = process.env.QSDM_TASK_ACTION_SENDER || 'unknown';
const intervalMs = Math.max(10000, Number(process.env.QSDM_EDGE_WORKER_INTERVAL_MS || 60000));
const iterations = Math.max(1000, Number(process.env.QSDM_EDGE_WORKER_ITERATIONS || 50000));
let round = 0;
let stopping = false;

function sha256(value) {
  return crypto.createHash('sha256').update(value).digest('hex');
}

function computeProof() {
  if (stopping) return;
  round += 1;
  const startedAt = new Date().toISOString();
  const seed = [taskId, sender, round, Date.now(), process.pid].join(':');
  let digest = seed;
  for (let index = 0; index < iterations; index += 1) {
    digest = sha256(digest);
  }
  const payload = {
    source: 'qsdm-edge-worker',
    worker_kind: 'cpu-sha256-v1',
    round,
    slot: Math.floor(Date.now() / 1000),
    submission_value: digest,
    proof: {
      algorithm: 'sha256-iterated',
      iterations,
      seed_hash: sha256(seed),
      digest,
      started_at: startedAt,
      completed_at: new Date().toISOString(),
    },
  };
  console.log('QSDM_EDGE_PROOF ' + JSON.stringify(payload));
}

console.log('QSDM Edge Worker started task=' + taskId + ' sender=' + sender + ' interval_ms=' + intervalMs + ' iterations=' + iterations);
computeProof();
const timer = setInterval(computeProof, intervalMs);

function shutdown(signal) {
  stopping = true;
  clearInterval(timer);
  console.log('QSDM Edge Worker stopping signal=' + signal);
  process.exit(0);
}

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

const getQsdmNativeTaskClock = async (
  task: Pick<RawTaskData, 'round_time' | 'starting_slot'>
): Promise<QsdmNativeTaskClock> => {
  const response = await axios.get<QsdmStatusResponse>(
    buildQsdmCoreApiUrl('/status'),
    {
      timeout: 10000,
    }
  );
  const slot = Math.max(0, Math.floor(Number(response.data.chain_tip) || 0));
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
  const response = await axios.get<QsdmMiningAccountResponse>(
    buildQsdmCoreApiUrl('/mining/account'),
    {
      params: { address: sender },
      timeout: 10000,
    }
  );
  const nonce = Number(response.data.nonce);
  return Number.isFinite(nonce) ? nonce : undefined;
};

const waitForQsdmTaskActionCommit = async (
  response: QsdmTaskActionSubmitResponse,
  context: string
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
    QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
    `${context} deferred: QSDM Core did not confirm nonce ${signedNonce} before timeout.`
  );
  return false;
};

const getQsdmEdgeWorkerLiveTask = async () => {
  const response = await axios.get<QsdmTaskStateResponse>(
    buildQsdmCoreApiUrl(
      `/tasks/${encodeURIComponent(QSDM_EDGE_WORKER_SYSTEM_TASK_ID)}/state`
    ),
    { timeout: 10000 }
  );

  return response.data.task || {};
};

const ensureQsdmEdgeWorkerRewardPool = async (
  rewardAmount: number,
  taskState?: Partial<RawTaskData>
) => {
  const targetPool = roundCellAmount(
    Math.max(QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL, rewardAmount)
  );
  if (targetPool <= 0 || rewardAmount <= 0) {
    return false;
  }

  let task: Partial<RawTaskData>;
  try {
    task = taskState || (await getQsdmEdgeWorkerLiveTask());
  } catch (error: any) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Could not check Edge Worker reward pool: ${error?.message || error}`
    );
    return false;
  }

  const currentPool = getPositiveNumber(task.reward_pool_amount);
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
      taskId: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      action: 'fund',
      amount: topUpAmount,
      payload: {
        source: 'qsdm-edge-worker',
        reason: 'seed CPU worker reward pool',
        reward_per_round_cell: rewardAmount,
        reward_pool_target_cell: targetPool,
      },
    });
    const committed = await waitForQsdmTaskActionCommit(
      response,
      'Edge Worker reward pool seed'
    );
    if (!committed) {
      return false;
    }
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Seeded Edge Worker reward pool with ${topUpAmount} CELL`
    );
    return true;
  } catch (error: any) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Could not seed Edge Worker reward pool: ${error?.message || error}`
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

const claimQsdmEdgeWorkerReward = async (round: number) => {
  try {
    const response = await submitQsdmTaskActionIntent({
      taskId: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      action: 'claim',
      payload: {
        source: 'qsdm-edge-worker',
        round: 0,
        latest_submitted_round: round,
      },
    });
    await waitForQsdmTaskActionCommit(response, 'Edge Worker reward claim');
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Claimed Edge Worker reward round=${round}`
    );
  } catch (error: any) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Edge Worker reward claim deferred: ${error?.message || error}`
    );
  }
};

const submitQsdmEdgeWorkerProof = async (
  payload: Record<string, unknown>,
  state: EdgeWorkerOutputState
) => {
  const sender = getQsdmTaskActionSender();
  if (!sender) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      'Skipping Edge Worker proof submit: QSDM_TASK_ACTION_SENDER is not configured.'
    );
    return;
  }

  let clock: QsdmNativeTaskClock;
  try {
    clock = await getQsdmNativeTaskClock(state.task);
  } catch (error: any) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Skipping Edge Worker proof submit: QSDM Core status unavailable: ${
        error?.message || error
      }`
    );
    return;
  }

  if (state.submittedRounds.has(clock.round)) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Skipping Edge Worker proof submit: round=${clock.round} already submitted for slot=${clock.slot}.`
    );
    return;
  }

  let liveTask: Partial<RawTaskData>;
  try {
    liveTask = await getQsdmEdgeWorkerLiveTask();
  } catch (error: any) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Skipping Edge Worker proof submit: task state unavailable: ${
        error?.message || error
      }`
    );
    return;
  }

  if (hasQsdmEdgeWorkerRoundSubmission(liveTask, sender, clock.round)) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Skipping Edge Worker proof submit: round=${clock.round} already exists on QSDM Core.`
    );
    state.submittedRounds.add(clock.round);
    return;
  }

  let rewardAmount = roundCellAmount(
    getPositiveNumber(state.task.bounty_amount_per_round)
  );
  if (rewardAmount > 0) {
    const poolReady = await ensureQsdmEdgeWorkerRewardPool(
      rewardAmount,
      liveTask
    );
    if (!poolReady) {
      rewardAmount = 0;
    }
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
      taskId: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      action: 'submit',
      payload: hardenedPayload,
    });
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Submitted Edge Worker proof round=${clock.round} slot=${clock.slot} reward=${rewardAmount}`
    );
    state.submittedRounds.add(clock.round);
    if (rewardAmount > 0) {
      const committed = await waitForQsdmTaskActionCommit(
        response,
        'Edge Worker proof submit'
      );
      if (committed) {
        await claimQsdmEdgeWorkerReward(clock.round);
      }
    }
  } catch (error: any) {
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Edge Worker proof submit failed: ${error?.message || error}`
    );
  }
};

const handleQsdmEdgeWorkerOutput = (data: Buffer, state: EdgeWorkerOutputState) => {
  const text = data.toString();
  writeTaskLog(QSDM_EDGE_WORKER_SYSTEM_TASK_ID, text.trimEnd());

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
      submitQsdmEdgeWorkerProof(payload, state);
    } catch (error: any) {
      writeTaskLog(
        QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
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
  const response = await axios.get<QsdmTaskStateResponse>(
    buildQsdmCoreApiUrl(
      `/tasks/${encodeURIComponent(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID)}/state`
    ),
    { timeout: 10000 }
  );

  return response.data.task || {};
};

const hasAnyQsdmTaskSubmissionForSender = (
  task: Partial<RawTaskData>,
  sender: string
) => {
  const submissions = task.submissions || {};
  return Object.values(submissions).some(
    (bySender) => isRecord(bySender) && Boolean(bySender[sender])
  );
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

    return {
      ok: true,
      sender,
      linkedAt: status.linked_at,
      site: status.site || getSkyFangBaseUrl(),
      account: status.account,
      username: status.username,
      player: status.player,
    };
  };

export const requireQsdmSkyFangWalletLinkedForMiner = async () => {
  const gate = await verifyQsdmSkyFangWalletLinked();
  if (!gate.ok) {
    writeTaskLog(
      QSDM_MINER_SYSTEM_TASK_ID,
      `Sky Fang wallet link gate failed: ${gate.detail}`
    );
    throw new Error(gate.detail || buildSkyFangLinkRequiredMessage(gate.sender));
  }

  writeTaskLog(
    QSDM_MINER_SYSTEM_TASK_ID,
    `Sky Fang wallet link verified for ${gate.sender}`
  );
  return gate;
};

export const requireQsdmSkyFangWalletLinkedForSkyFangLink = async () => {
  const gate = await verifyQsdmSkyFangWalletLinked();
  if (!gate.ok) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang wallet link verifier gate failed: ${gate.detail}`
    );
    throw new Error(gate.detail || buildSkyFangLinkRequiredMessage(gate.sender));
  }

  writeTaskLog(
    QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
    `Sky Fang wallet link verified for ${gate.sender}; starting one-time proof submitter.`
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

  const currentPool = getPositiveNumber(task.reward_pool_amount);
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
        reason: 'seed Sky Fang wallet-link reward pool',
        reward_per_link_cell: rewardAmount,
        reward_pool_target_cell: targetPool,
      },
    });
    const committed = await waitForQsdmTaskActionCommit(
      response,
      'Sky Fang Link reward pool seed'
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
        round: 0,
        linked_round: round,
      },
    });
    await waitForQsdmTaskActionCommit(response, 'Sky Fang Link reward claim');
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Claimed Sky Fang Link reward round=${round}`
    );
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang Link reward claim deferred: ${error?.message || error}`
    );
  }
};

const submitQsdmSkyFangLinkProof = async (state: SkyFangLinkOutputState) => {
  if (state.submitted) {
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
    state.submitted = false;
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang account is not linked to the active Hive wallet yet. Open ${getSkyFangBaseUrl()}/login?next=/dashboard/qsdm, sign in, and link ${sender}.`
    );
    return;
  }

  if (hasAnyQsdmTaskSubmissionForSender(liveTask, sender)) {
    state.submitted = true;
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      'Sky Fang Link proof already exists on QSDM Core and Sky Fang live status confirms this wallet is still linked; stopping verifier.'
    );
    state.child?.kill();
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

  let rewardAmount = roundCellAmount(
    getPositiveNumber(
      state.task.bounty_amount_per_round,
      QSDM_SKYFANG_LINK_REWARD_CELL
    )
  );
  const poolReady = await ensureQsdmSkyFangLinkRewardPool(
    rewardAmount,
    liveTask
  );
  if (!poolReady) {
    rewardAmount = 0;
  }

  const payload = {
    source: 'qsdm-skyfang-wallet-link',
    submission_value: createSubmissionDigest({
      sender,
      site: status.site || getSkyFangBaseUrl(),
      linked_at: status.linked_at || '',
    }),
    round: clock.round,
    slot: clock.slot,
    reward_amount: rewardAmount,
    qsdm_round_unit: 'block-height',
    qsdm_round_time_blocks: clock.roundTimeBlocks,
    qsdm_starting_slot: clock.startingSlot,
    one_time_reward: true,
    linked_wallet_address: sender,
    skyfang_site: status.site || getSkyFangBaseUrl(),
    skyfang_linked_at: status.linked_at || '',
  };

  try {
    const response = await submitQsdmTaskActionIntent({
      taskId: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      action: 'submit',
      payload,
    });
    state.submitted = true;
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Submitted Sky Fang Link proof round=${clock.round} slot=${clock.slot} reward=${rewardAmount}`
    );
    if (rewardAmount > 0) {
      const committed = await waitForQsdmTaskActionCommit(
        response,
        'Sky Fang Link proof submit'
      );
      if (committed) {
        await claimQsdmSkyFangLinkReward(clock.round);
      }
    }
    state.child?.kill();
  } catch (error: any) {
    writeTaskLog(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      `Sky Fang Link proof submit failed: ${error?.message || error}`
    );
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

    submitQsdmSkyFangLinkProof(state);
  });
};

const findFromAncestors = (relativePath: string): string | null => {
  let current = process.cwd();

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

const getMinerExecutableCandidates = () => [
  process.env.QSDM_MINER_EXE,
  findFromAncestors(path.join('Blackbeard', 'qsdmminer.exe')),
  findFromAncestors(path.join('QSDM', 'source', 'qsdmminer-console.exe')),
  findFromAncestors(path.join('QSDM', 'source', 'qsdmminer.exe')),
  path.join(process.env.ProgramFiles || 'C:\\Program Files', 'QSDM Miner', 'qsdmminer.exe'),
].filter(Boolean) as string[];

const getMinerExecutablePath = () => {
  const minerExecutable = getMinerExecutableCandidates().find((candidate) =>
    fs.existsSync(candidate)
  );

  if (!minerExecutable) {
    throw new Error(
      'QSDM miner executable was not found. Set QSDM_MINER_EXE or install/build QSDM Miner first.'
    );
  }

  return minerExecutable;
};

const getMinerConfigPath = () =>
  process.env.QSDM_MINER_CONFIG ||
  path.join(os.homedir(), '.qsdm', 'miner.toml');

const getMinerLogPath = () =>
  process.env.QSDM_MINER_LOG ||
  path.join(os.homedir(), '.qsdm', 'miner.log');

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

  const separator = config.trim().length > 0 && !config.endsWith('\n') ? '\n' : '';
  return `${config}${separator}${line}\n`;
};

export const getQsdmMinerRewardAddressInfo = ():
  | QsdmMinerRewardAddressInfo
  | null => {
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
    const configAddress = readMinerConfigStringValue(config, 'reward_address');
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
    const updatedConfig = setMinerConfigStringValue(
      previousConfig,
      'reward_address',
      signer
    );

    let backupPath: string | undefined;
    if (fs.existsSync(configPath) && currentAddress !== signer) {
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
      updated: currentAddress !== signer || updatedConfig !== previousConfig,
      backupPath,
      requiresMinerRestart: true,
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

export const getQsdmMinerSystemProcesses =
  async (): Promise<QsdmMinerProcessInfo[]> => {
    try {
      return process.platform === 'win32'
        ? sortMinerProcessesByPreference(await getWindowsMinerProcesses())
        : sortMinerProcessesByPreference(await getUnixMinerProcesses());
    } catch (error: any) {
      writeTaskLog(
        QSDM_MINER_SYSTEM_TASK_ID,
        `Could not probe existing QSDM miner process: ${
          error?.message || error
        }`
      );
      return [];
    }
  };

export const getQsdmMinerSystemProcessInfo =
  async (): Promise<QsdmMinerProcessInfo | null> =>
    (await getQsdmMinerSystemProcesses())[0] || null;

export const stopExtraQsdmMinerSystemProcesses = async (keepPid?: number) => {
  const processes = await getQsdmMinerSystemProcesses();
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
        `Could not stop duplicate QSDM miner process pid=${
          processInfo.pid
        }: ${error?.message || error}`
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
      writeTaskLog(taskId, `Adopted QSDM miner process ${processInfo.pid} exited`);
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

export const startOrAdoptQsdmMinerSystemProcess = async (
  options: QsdmMinerSystemProcessOptions = {}
): Promise<{
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
}> => {
  if (!options.skipSkyFangLinkGate) {
    await requireQsdmSkyFangWalletLinkedForMiner();
  }

  const processInfo = await getQsdmMinerSystemProcessInfo();
  if (processInfo) {
    await stopExtraQsdmMinerSystemProcesses(processInfo.pid);
    return adoptQsdmMinerSystemProcess(processInfo);
  }

  return startQsdmMinerSystemProcess({ skipSkyFangLinkGate: true });
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

const getEdgeWorkerDirectory = () =>
  path.join(getAppDataPath(), 'namespace', QSDM_EDGE_WORKER_SYSTEM_TASK_ID);

const getEdgeWorkerScriptPath = () =>
  path.join(getEdgeWorkerDirectory(), 'edge-worker.js');

const ensureEdgeWorkerScript = () => {
  const workerScriptPath = getEdgeWorkerScriptPath();
  fs.mkdirSync(path.dirname(workerScriptPath), { recursive: true });
  fs.writeFileSync(workerScriptPath, createQsdmEdgeWorkerScript(), 'utf8');
  return workerScriptPath;
};

export const startQsdmEdgeWorkerSystemProcess = (
  taskInfo: Pick<
    RawTaskData,
    | 'round_time'
    | 'starting_slot'
    | 'bounty_amount_per_round'
    | 'reward_pool_amount'
  > = createQsdmEdgeWorkerSystemTask()
): {
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
} => {
  const executablePath = process.execPath;
  const workerScriptPath = ensureEdgeWorkerScript();
  const logPath = path.join(getEdgeWorkerDirectory(), 'task.log');
  const outputState: EdgeWorkerOutputState = {
    pending: '',
    task: taskInfo,
    submittedRounds: new Set<number>(),
  };

  writeTaskLog(
    QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
    `Starting QSDM Edge Worker: "${executablePath}" "${workerScriptPath}" round_time_blocks=${taskInfo.round_time} starting_slot=${taskInfo.starting_slot}`
  );

  const child = spawn(executablePath, [workerScriptPath], {
    detached: false,
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
    env: {
      ...process.env,
      ELECTRON_RUN_AS_NODE: '1',
      QSDM_EDGE_TASK_ID: QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      QSDM_TASK_ACTION_SENDER: getQsdmTaskActionSender(),
    },
  });

  child.stdout?.on('data', (data) =>
    handleQsdmEdgeWorkerOutput(data, outputState)
  );
  child.stderr?.on('data', (data) =>
    writeTaskLog(QSDM_EDGE_WORKER_SYSTEM_TASK_ID, data.toString().trimEnd())
  );
  child.on('error', (error) =>
    writeTaskLog(
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      `Edge Worker process error: ${error.message}`
    )
  );

  return {
    child,
    secret: cryptoRandomString({ length: 20 }),
    logPath,
    executablePath: workerScriptPath,
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
    submitted: false,
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
  outputState.child = child;

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
        new Error(
          `${label} exited during startup with code ${code} and signal ${signal}. Check ${logPath}.`
        )
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

export const startQsdmMinerSystemProcess = async (
  options: QsdmMinerSystemProcessOptions = {}
): Promise<{
  child: ChildProcess;
  secret: string;
  logPath: string;
  executablePath: string;
}> => {
  if (!options.skipSkyFangLinkGate) {
    await requireQsdmSkyFangWalletLinkedForMiner();
  }

  const executablePath = getMinerExecutablePath();
  const configPath = getMinerConfigPath();
  const logPath = getMinerLogPath();

  if (!fs.existsSync(configPath)) {
    throw new Error(
      `QSDM miner config is missing at ${configPath}. Run the QSDM Miner setup once, then start this task again.`
    );
  }

  fs.mkdirSync(path.dirname(logPath), { recursive: true });
  fs.mkdirSync(
    path.join(getAppDataPath(), 'namespace', QSDM_MINER_SYSTEM_TASK_ID),
    { recursive: true }
  );

  const args = [
    `--config=${configPath}`,
    '--idle-only',
    '--idle-threshold=10',
    '--idle-grace=60s',
    `--log-file=${logPath}`,
    '--log-size-mb=10',
    '--log-keep=5',
    '--plain',
  ];

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
