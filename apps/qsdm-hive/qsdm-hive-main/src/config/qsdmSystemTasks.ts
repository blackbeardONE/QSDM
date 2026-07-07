export const QSDM_MINER_SYSTEM_TASK_ID = 'qsdm-system-miner';
export const QSDM_MINER_SYSTEM_TASK_METADATA_ID = 'qsdm-system-miner-metadata';
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
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
];

export const QSDM_HIVE_INTERNAL_TASK_ID = 'qsdm-hive-local-task';
export const QSDM_HIVE_INTERNAL_TASK_IDS = [QSDM_HIVE_INTERNAL_TASK_ID];

export const isQsdmMinerSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_MINER_SYSTEM_TASK_ID ||
  taskId === QSDM_MINER_SYSTEM_TASK_METADATA_ID;

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
