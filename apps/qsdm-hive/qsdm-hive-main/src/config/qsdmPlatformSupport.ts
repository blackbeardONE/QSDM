/**
 * What each operating system can run in QSDM Hive.
 *
 * Mining CELL needs an NVIDIA GPU, and Hive's miner and its CUDA proof solver
 * are built for Windows and Linux only. On macOS (12 and newer, Intel and
 * Apple silicon) Hive is the wallet and runs tasks; the miner never starts.
 * The GPU Edge Worker needs the same NVIDIA CUDA helper, so it follows the
 * same list. Shared by the main process (which refuses to start either) and
 * the renderer (which says so in the Miner panel).
 */
export const QSDM_MINING_PLATFORMS: readonly string[] = ['win32', 'linux'];

export const QSDM_MINING_UNSUPPORTED_MESSAGE =
  'Mining CELL needs Windows or Linux with an NVIDIA GPU. On a Mac, QSDM Hive is your wallet and runs your tasks, but the miner cannot start.';

export const QSDM_GPU_EDGE_WORKER_UNSUPPORTED_MESSAGE =
  'QSDM Edge Worker GPU needs Windows or Linux with an NVIDIA GPU, so it cannot start on a Mac.';

export const isQsdmMiningPlatform = (platform: string): boolean =>
  QSDM_MINING_PLATFORMS.includes(platform);
