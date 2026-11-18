// cspell:words qsdmminer
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  getQsdmMinerSiblingConflictMessage,
  getQsdmMinerSiblingTaskId,
  isQsdmMinerSystemTaskId,
  isQsdmPoolMinerSystemTaskId,
  isQsdmSystemTaskId,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_POOL_MINER_SYSTEM_TASK_ID,
  QSDM_POOL_MINER_SYSTEM_TASK_METADATA_ID,
  QSDM_SYSTEM_TASK_IDS,
} from 'config/qsdmSystemTasks';

import {
  normalizeQsdmPulseSettings,
  readQsdmPulseReceipts,
} from './qsdmPulsePool';
import {
  createQsdmPoolMinerSystemTask,
  getQsdmSystemTaskById,
  getQsdmSystemTaskMetadata,
  isQsdmMinerCommandLineInPoolMode,
  mergeQsdmSystemTasks,
  resolveQsdmMinerPulseArgs,
} from './qsdmSystemTasks';

jest.mock('main/node/helpers/getAppDataPath', () => ({
  getAppDataPath: () => '/tmp/qsdm-pool-miner-task-spec',
}));

describe('QSDM Pool Miner task', () => {
  it('is a system miner task of its own, next to QSDM Miner', () => {
    expect(QSDM_SYSTEM_TASK_IDS).toContain(QSDM_POOL_MINER_SYSTEM_TASK_ID);
    expect(isQsdmPoolMinerSystemTaskId(QSDM_POOL_MINER_SYSTEM_TASK_ID)).toBe(
      true
    );
    expect(
      isQsdmPoolMinerSystemTaskId(QSDM_POOL_MINER_SYSTEM_TASK_METADATA_ID)
    ).toBe(true);
    expect(isQsdmPoolMinerSystemTaskId(QSDM_MINER_SYSTEM_TASK_ID)).toBe(false);
    // Both miner tasks share the miner's enrollment, rewards and no-stake rule.
    expect(isQsdmMinerSystemTaskId(QSDM_POOL_MINER_SYSTEM_TASK_ID)).toBe(true);
    expect(isQsdmSystemTaskId(QSDM_POOL_MINER_SYSTEM_TASK_ID)).toBe(true);

    const tasks = mergeQsdmSystemTasks([]);
    const ids = tasks.map((task) => task.task_id);
    expect(ids.indexOf(QSDM_POOL_MINER_SYSTEM_TASK_ID)).toBe(
      ids.indexOf(QSDM_MINER_SYSTEM_TASK_ID) + 1
    );
    const poolTask = tasks.find(
      (task) => task.task_id === QSDM_POOL_MINER_SYSTEM_TASK_ID
    );
    expect(poolTask?.task_name).toBe('QSDM Pool Miner');
    expect(poolTask?.task_metadata).toBe(
      QSDM_POOL_MINER_SYSTEM_TASK_METADATA_ID
    );
    expect(poolTask?.minimum_stake_amount).toBe(0);
    const vars = JSON.parse(poolTask?.task_vars || '{}') as {
      pulse_pool?: boolean;
    };
    expect(vars.pulse_pool).toBe(true);
  });

  it('keeps its own id and name whatever a catalog record says', () => {
    const task = createQsdmPoolMinerSystemTask({
      task_id: 'something-else',
      task_name: 'Renamed',
    });
    expect(task.task_id).toBe(QSDM_POOL_MINER_SYSTEM_TASK_ID);
    expect(task.task_name).toBe('QSDM Pool Miner');
    expect(
      getQsdmSystemTaskById(QSDM_POOL_MINER_SYSTEM_TASK_ID)?.task_name
    ).toBe('QSDM Pool Miner');
    expect(getQsdmSystemTaskById(QSDM_MINER_SYSTEM_TASK_ID)?.task_name).toBe(
      'QSDM Miner'
    );
  });

  it('has short plain metadata pointing at qsdm.tech/pool', () => {
    const metadata = getQsdmSystemTaskMetadata(
      QSDM_POOL_MINER_SYSTEM_TASK_METADATA_ID
    );
    expect(metadata?.infoUrl).toBe('https://qsdm.tech/pool');
    expect(metadata?.description).toContain('one click');
    expect(metadata?.tags).toContain('Pool');
  });

  it('only one miner task may run on the GPU at a time', () => {
    expect(getQsdmMinerSiblingTaskId(QSDM_POOL_MINER_SYSTEM_TASK_ID)).toBe(
      QSDM_MINER_SYSTEM_TASK_ID
    );
    expect(getQsdmMinerSiblingTaskId(QSDM_MINER_SYSTEM_TASK_ID)).toBe(
      QSDM_POOL_MINER_SYSTEM_TASK_ID
    );
    expect(
      getQsdmMinerSiblingConflictMessage(QSDM_POOL_MINER_SYSTEM_TASK_ID)
    ).toBe(
      'QSDM Miner is already mining on this GPU. Stop QSDM Miner first, then start QSDM Pool Miner.'
    );
    expect(getQsdmMinerSiblingConflictMessage(QSDM_MINER_SYSTEM_TASK_ID)).toBe(
      'QSDM Pool Miner is already mining on this GPU. Stop QSDM Pool Miner first, then start QSDM Miner.'
    );
  });
});

describe('miner launch modes', () => {
  const off = normalizeQsdmPulseSettings({});
  const receipts = '/tmp/r.jsonl';

  it("the Miner task follows its pool panel ('settings'), off by default", () => {
    expect(resolveQsdmMinerPulseArgs('settings', off)).toEqual([]);
    expect(
      resolveQsdmMinerPulseArgs('settings', { ...off, enabled: true })
    ).toContain('--pool-url=https://qsdm.tech/pool/api');
  });

  it("the Pool Miner forces pool mode with the defaults ('force-on')", () => {
    const args = resolveQsdmMinerPulseArgs('force-on', off);
    expect(args).toContain('--pool-url=https://qsdm.tech/pool/api');
    expect(args).toContain('--pool-fallback-minutes=5');
    expect(args.some((a) => a.startsWith('--pool-receipts-file='))).toBe(true);
  });

  it("solo for the Pool Miner before PULSE ('off') whatever the panel says", () => {
    expect(resolveQsdmMinerPulseArgs('off', { ...off, enabled: true })).toEqual(
      []
    );
  });

  it('recognizes a running miner in pool mode by its command line', () => {
    expect(
      isQsdmMinerCommandLineInPoolMode(
        `qsdmminer-console --config=x --plain --pool-url=https://qsdm.tech/pool/api --pool-receipts-file=${receipts}`
      )
    ).toBe(true);
    expect(
      isQsdmMinerCommandLineInPoolMode('qsdmminer-console --config=x --plain')
    ).toBe(false);
    expect(isQsdmMinerCommandLineInPoolMode(undefined)).toBe(false);
  });
});

describe('pool receipts today', () => {
  it('counts only the receipts the pool signed today (local day)', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pulse-today-'));
    const file = path.join(dir, 'receipts.jsonl');
    const now = new Date(2026, 10, 20, 15, 0, 0).getTime();
    const yesterday = new Date(2026, 10, 19, 23, 59, 0).getTime();
    const morning = new Date(2026, 10, 20, 0, 1, 0).getTime();
    fs.writeFileSync(
      file,
      [
        JSON.stringify({ receipt: { index: 1, at_unix_ms: yesterday } }),
        JSON.stringify({ receipt: { index: 2, at_unix_ms: morning } }),
        'not json',
        JSON.stringify({ receipt: { index: 3, at_unix_ms: now - 1000 } }),
      ].join('\n')
    );
    const read = readQsdmPulseReceipts(file, now);
    expect(read.today).toBe(2);
    expect(read.total).toBe(4);
    expect(readQsdmPulseReceipts(path.join(dir, 'missing'), now)).toEqual({
      total: 0,
      today: 0,
      recent: [],
    });
    fs.rmSync(dir, { recursive: true, force: true });
  });
});
