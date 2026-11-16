export const QSDM_MINER_SYSTEM_TASK_ID = 'qsdm-system-miner';
export const QSDM_MINER_SYSTEM_TASK_METADATA_ID = 'qsdm-system-miner-metadata';
// QSDM Pool Miner: the same packaged miner and GPU enrollment as QSDM Miner,
// started in PULSE pool mode with the defaults (one button, no settings).
export const QSDM_POOL_MINER_SYSTEM_TASK_ID = 'qsdm-system-pool-miner';
export const QSDM_POOL_MINER_SYSTEM_TASK_METADATA_ID =
  'qsdm-system-pool-miner-metadata';
// PULSE payouts start at this block (the rc1114 consensus default). Before it
// the Pool Miner task mines solo.
export const QSDM_PULSE_ACTIVATION_HEIGHT = 1_210_000;
export const QSDM_PULSE_ACTIVATION_DATE_TEXT = 'about 26-27 November';
// Protocol miners lock the validator-advertised enrollment bond. They do not
// also pay a separate Hive task stake.
export const QSDM_MINER_MIN_STAKE_CELL = 0;
export const QSDM_MINER_MIN_STAKE_AMOUNT =
  QSDM_MINER_MIN_STAKE_CELL * 1_000_000_000;

export const QSDM_EDGE_WORKER_SYSTEM_TASK_ID = 'qsdm-edge-worker';
export const QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID =
  'qsdm-edge-worker-metadata';
export const QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID = 'qsdm-edge-worker-gpu';
export const QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID =
  'qsdm-edge-worker-gpu-metadata';
export const QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID = 'qsdm-edge-worker-ram';
export const QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID =
  'qsdm-edge-worker-ram-metadata';
export const QSDM_EDGE_WORKER_MIN_STAKE_CELL = 1;
export const QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT =
  QSDM_EDGE_WORKER_MIN_STAKE_CELL * 1_000_000_000;
const readPositiveCellEnv = (key: string, fallback: number) => {
  const parsed = Number(process.env[key]);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
};

export const QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL = readPositiveCellEnv(
  'QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL',
  0.05
);
export const QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL = readPositiveCellEnv(
  'QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL',
  1
);
export const QSDM_EDGE_WORKER_GPU_REWARD_PER_ROUND_CELL = readPositiveCellEnv(
  'QSDM_EDGE_WORKER_GPU_REWARD_PER_ROUND_CELL',
  0.1
);
export const QSDM_EDGE_WORKER_GPU_REWARD_POOL_TARGET_CELL = readPositiveCellEnv(
  'QSDM_EDGE_WORKER_GPU_REWARD_POOL_TARGET_CELL',
  2
);
export const QSDM_EDGE_WORKER_RAM_REWARD_PER_ROUND_CELL = readPositiveCellEnv(
  'QSDM_EDGE_WORKER_RAM_REWARD_PER_ROUND_CELL',
  0.05
);
export const QSDM_EDGE_WORKER_RAM_REWARD_POOL_TARGET_CELL = readPositiveCellEnv(
  'QSDM_EDGE_WORKER_RAM_REWARD_POOL_TARGET_CELL',
  1
);

export type QsdmEdgeWorkerResource = 'cpu' | 'gpu' | 'ram';

// Mother Hive is not a separate client. It is the coordinator role assumed by
// QSDM Hive while a paired Relay supplies authenticated pooled resources.
export const QSDM_MOTHER_HIVE_SYSTEM_TASK_ID = 'qsdm-mother-hive';
export const QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID =
  'qsdm-mother-hive-metadata';
export const QSDM_MOTHER_HIVE_MIN_STAKE_CELL = 1;
export const QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT =
  QSDM_MOTHER_HIVE_MIN_STAKE_CELL * 1_000_000_000;
export const QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT = 70;
export const QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT = 15;
export const QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT = 15;
export const QSDM_MOTHER_HIVE_SETTLEMENT_VERSION = 'qsdm-edge-settlement/v1';
export const QSDM_MOTHER_HIVE_SETTLEMENT_SOURCE = 'qsdm-edge-relay-v2';
export const QSDM_ECOSYSTEM_RESERVE_WALLET =
  '651a79b2b1790820dd73bda81be24057e1bc27377c1f1117c6db2ab79dc038ea';

export const QSDM_SKYFANG_LINK_SYSTEM_TASK_ID = 'qsdm-skyfang-wallet-link';
export const QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID =
  'qsdm-skyfang-wallet-link-metadata';
export const QSDM_SKYFANG_LINK_MIN_STAKE_CELL = 1;
export const QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT =
  QSDM_SKYFANG_LINK_MIN_STAKE_CELL * 1_000_000_000;
export const QSDM_SKYFANG_LINK_BASE_REWARD_CELL = readPositiveCellEnv(
  'QSDM_SKYFANG_LINK_BASE_REWARD_CELL',
  0.05
);
export const QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE = readPositiveCellEnv(
  'QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE',
  0.01
);
export const QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE = readPositiveCellEnv(
  'QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE',
  0.02
);
export const QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL = readPositiveCellEnv(
  'QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL',
  1
);
export const QSDM_SKYFANG_LINK_REWARD_CELL = QSDM_SKYFANG_LINK_BASE_REWARD_CELL;
export const QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL = readPositiveCellEnv(
  'QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL',
  25
);

export const QSDM_SYSTEM_TASK_IDS = [
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_POOL_MINER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
];

export const QSDM_HIVE_INTERNAL_TASK_ID = 'qsdm-hive-local-task';
export const QSDM_HIVE_INTERNAL_TASK_IDS = [QSDM_HIVE_INTERNAL_TASK_ID];

export const isQsdmPoolMinerSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_POOL_MINER_SYSTEM_TASK_ID ||
  taskId === QSDM_POOL_MINER_SYSTEM_TASK_METADATA_ID;

// True for both miner tasks (solo and pool): they share the packaged miner,
// the GPU enrollment, the protocol rewards and the no-stake rule.
export const isQsdmMinerSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_MINER_SYSTEM_TASK_ID ||
  taskId === QSDM_MINER_SYSTEM_TASK_METADATA_ID ||
  isQsdmPoolMinerSystemTaskId(taskId);

// The other miner task on this GPU: only one of the two may run at a time.
export const getQsdmMinerSiblingTaskId = (taskId?: string | null) =>
  isQsdmPoolMinerSystemTaskId(taskId)
    ? QSDM_MINER_SYSTEM_TASK_ID
    : QSDM_POOL_MINER_SYSTEM_TASK_ID;

export const getQsdmMinerSiblingConflictMessage = (taskId?: string | null) =>
  isQsdmPoolMinerSystemTaskId(taskId)
    ? 'QSDM Miner is already mining on this GPU. Stop QSDM Miner first, then start QSDM Pool Miner.'
    : 'QSDM Pool Miner is already mining on this GPU. Stop QSDM Pool Miner first, then start QSDM Miner.';

export const isQsdmEdgeWorkerSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_EDGE_WORKER_SYSTEM_TASK_ID ||
  taskId === QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID ||
  taskId === QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID ||
  taskId === QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID ||
  taskId === QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID ||
  taskId === QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID;

export const getQsdmEdgeWorkerResource = (
  taskId?: string | null
): QsdmEdgeWorkerResource | null => {
  if (
    taskId === QSDM_EDGE_WORKER_SYSTEM_TASK_ID ||
    taskId === QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID
  ) {
    return 'cpu';
  }
  if (
    taskId === QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID ||
    taskId === QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID
  ) {
    return 'gpu';
  }
  if (
    taskId === QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID ||
    taskId === QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID
  ) {
    return 'ram';
  }
  return null;
};

export const isQsdmSkyFangLinkSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_SKYFANG_LINK_SYSTEM_TASK_ID ||
  taskId === QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID;

export const isQsdmMotherHiveSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_MOTHER_HIVE_SYSTEM_TASK_ID ||
  taskId === QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID;

export const isQsdmSystemTaskId = (taskId?: string | null) =>
  isQsdmMinerSystemTaskId(taskId) ||
  isQsdmEdgeWorkerSystemTaskId(taskId) ||
  isQsdmMotherHiveSystemTaskId(taskId) ||
  isQsdmSkyFangLinkSystemTaskId(taskId);

export const isQsdmHiveInternalTaskId = (taskId?: string | null) =>
  !!taskId && QSDM_HIVE_INTERNAL_TASK_IDS.includes(taskId);
