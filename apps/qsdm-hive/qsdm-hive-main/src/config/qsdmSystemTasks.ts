export const QSDM_MINER_SYSTEM_TASK_ID = 'qsdm-system-miner';
export const QSDM_MINER_SYSTEM_TASK_METADATA_ID =
  'qsdm-system-miner-metadata';
export const QSDM_MINER_MIN_STAKE_CELL = 1;
export const QSDM_MINER_MIN_STAKE_AMOUNT =
  QSDM_MINER_MIN_STAKE_CELL * 1_000_000_000;

export const QSDM_EDGE_WORKER_SYSTEM_TASK_ID = 'qsdm-edge-worker';
export const QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID =
  'qsdm-edge-worker-metadata';
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

export const QSDM_SKYFANG_LINK_SYSTEM_TASK_ID = 'qsdm-skyfang-wallet-link';
export const QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID =
  'qsdm-skyfang-wallet-link-metadata';
export const QSDM_SKYFANG_LINK_MIN_STAKE_CELL = 1;
export const QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT =
  QSDM_SKYFANG_LINK_MIN_STAKE_CELL * 1_000_000_000;
export const QSDM_SKYFANG_LINK_REWARD_CELL = readPositiveCellEnv(
  'QSDM_SKYFANG_LINK_REWARD_CELL',
  1
);
export const QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL = readPositiveCellEnv(
  'QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL',
  25
);

export const QSDM_SYSTEM_TASK_IDS = [
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
];

export const QSDM_HIVE_INTERNAL_TASK_ID = 'qsdm-hive-local-task';
export const QSDM_HIVE_INTERNAL_TASK_IDS = [QSDM_HIVE_INTERNAL_TASK_ID];

export const isQsdmMinerSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_MINER_SYSTEM_TASK_ID ||
  taskId === QSDM_MINER_SYSTEM_TASK_METADATA_ID;

export const isQsdmEdgeWorkerSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_EDGE_WORKER_SYSTEM_TASK_ID ||
  taskId === QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID;

export const isQsdmSkyFangLinkSystemTaskId = (taskId?: string | null) =>
  taskId === QSDM_SKYFANG_LINK_SYSTEM_TASK_ID ||
  taskId === QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID;

export const isQsdmSystemTaskId = (taskId?: string | null) =>
  isQsdmMinerSystemTaskId(taskId) ||
  isQsdmEdgeWorkerSystemTaskId(taskId) ||
  isQsdmSkyFangLinkSystemTaskId(taskId);

export const isQsdmHiveInternalTaskId = (taskId?: string | null) =>
  !!taskId && QSDM_HIVE_INTERNAL_TASK_IDS.includes(taskId);
