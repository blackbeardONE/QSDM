import {
  isQsdmMiningPlatform,
  QSDM_GPU_EDGE_WORKER_UNSUPPORTED_MESSAGE,
  QSDM_MINING_UNSUPPORTED_MESSAGE,
} from 'config/qsdmPlatformSupport';

import {
  assertQsdmMiningPlatform,
  getMinerExecutablePath,
  getQsdmSystemTaskMetadata,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID,
  QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  startOrAdoptQsdmMinerSystemProcess,
  startQsdmEdgeWorkerSystemProcess,
} from './qsdmSystemTasks';

const withPlatform = async (
  platform: NodeJS.Platform,
  run: () => void | Promise<void>
) => {
  const original = Object.getOwnPropertyDescriptor(process, 'platform');
  Object.defineProperty(process, 'platform', { value: platform });
  try {
    await run();
  } finally {
    if (original) Object.defineProperty(process, 'platform', original);
  }
};

const descriptionOf = (metadataId: string) =>
  getQsdmSystemTaskMetadata(metadataId)?.description || '';

describe('QSDM mining platforms', () => {
  it('mines only on Windows and Linux (it needs an NVIDIA GPU)', () => {
    expect(isQsdmMiningPlatform('win32')).toBe(true);
    expect(isQsdmMiningPlatform('linux')).toBe(true);
    expect(isQsdmMiningPlatform('darwin')).toBe(false);
    expect(() => assertQsdmMiningPlatform('darwin')).toThrow(
      QSDM_MINING_UNSUPPORTED_MESSAGE
    );
    expect(() => assertQsdmMiningPlatform('linux')).not.toThrow();
    expect(() => assertQsdmMiningPlatform('win32')).not.toThrow();
  });

  it('never looks for, starts or adopts a miner on macOS', async () => {
    await withPlatform('darwin', async () => {
      expect(() => getMinerExecutablePath()).toThrow(
        QSDM_MINING_UNSUPPORTED_MESSAGE
      );
      await expect(startOrAdoptQsdmMinerSystemProcess()).rejects.toThrow(
        QSDM_MINING_UNSUPPORTED_MESSAGE
      );
    });
  });

  it('never starts the GPU Edge Worker on macOS', async () => {
    await withPlatform('darwin', () => {
      expect(() =>
        startQsdmEdgeWorkerSystemProcess(QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID)
      ).toThrow(QSDM_GPU_EDGE_WORKER_UNSUPPORTED_MESSAGE);
    });
  });

  it('says so first in the miner and GPU task descriptions on macOS only', async () => {
    await withPlatform('darwin', () => {
      expect(
        descriptionOf(QSDM_MINER_SYSTEM_TASK_METADATA_ID).startsWith(
          QSDM_MINING_UNSUPPORTED_MESSAGE
        )
      ).toBe(true);
      expect(
        descriptionOf(QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID).startsWith(
          QSDM_GPU_EDGE_WORKER_UNSUPPORTED_MESSAGE
        )
      ).toBe(true);
    });
    for (const platform of ['linux', 'win32'] as NodeJS.Platform[]) {
      // eslint-disable-next-line no-await-in-loop
      await withPlatform(platform, () => {
        expect(descriptionOf(QSDM_MINER_SYSTEM_TASK_METADATA_ID)).not.toContain(
          QSDM_MINING_UNSUPPORTED_MESSAGE
        );
        expect(
          descriptionOf(QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID)
        ).not.toContain(QSDM_GPU_EDGE_WORKER_UNSUPPORTED_MESSAGE);
      });
    }
  });
});
