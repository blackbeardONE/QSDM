/**
 * @jest-environment node
 */

import axios from 'axios';
import { app } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  getQsdmRuntimeCoreApiUrl,
  QSDM_CANONICAL_API_URL,
  QSDM_CANONICAL_GENESIS_HASH,
  QSDM_CANONICAL_GENESIS_STATE_ROOT,
  QSDM_DEFAULT_LOCAL_CORE_API_URL,
  setQsdmRuntimeCoreApiUrl,
} from 'config/qsdm';

import { getQsdmBackupRead, recordQsdmReadCheckpoint } from './qsdmBackupRead';

jest.mock('axios', () => ({ get: jest.fn(), post: jest.fn() }));
jest.mock('electron', () => ({ app: { getPath: jest.fn() } }));

const mockGet = axios.get as jest.Mock;
const mockGetPath = app.getPath as jest.Mock;
const hashFor = (height: number) => height.toString(16).padStart(64, '0');
const history = (tip = 100) =>
  Array.from({ length: Math.min(20, tip + 1) }, (_, index) => {
    const height = Math.max(0, tip - 19) + index;
    return {
      height,
      hash: hashFor(height),
      state_root: 'a'.repeat(64),
      timestamp: '2026-09-02T01:03:48.716773996Z',
      transactions: [{ secret: 'must not persist' }],
    };
  });
const genesis = () => ({
  data: {
    blocks: [
      {
        height: 0,
        hash: QSDM_CANONICAL_GENESIS_HASH,
        state_root: QSDM_CANONICAL_GENESIS_STATE_ROOT,
      },
    ],
  },
});

let userData: string;
const savedPath = () => path.join(userData, 'qsdm-read-checkpoint.json');
const saved = () =>
  JSON.parse(fs.readFileSync(savedPath(), 'utf8')) as {
    canonicalApiUrl: string;
    genesisHash: string;
    confirmedAt: string;
    blocks: Array<{
      height: number;
      hash: string;
      stateRoot: string;
      timestamp: string;
    }>;
  };
const saveHistory = async (tip = 100) => {
  mockGet.mockResolvedValueOnce({ data: { blocks: history(tip) } });
  await recordQsdmReadCheckpoint(QSDM_CANONICAL_API_URL, tip);
  mockGet.mockReset();
};
const acceptBackup = (blocks = history(), reportedTip = 100) => {
  mockGet
    .mockResolvedValueOnce({ data: { chain_tip: reportedTip, peers: 0 } })
    .mockResolvedValueOnce(genesis())
    .mockResolvedValueOnce({ data: { blocks } });
};

describe('QSDM checkpoint backup reads', () => {
  beforeEach(() => {
    userData = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-read-checkpoint-'));
    mockGetPath.mockReturnValue(userData);
    mockGet.mockReset();
    (axios.post as jest.Mock).mockReset();
    setQsdmRuntimeCoreApiUrl();
  });

  afterEach(() => {
    jest.restoreAllMocks();
    setQsdmRuntimeCoreApiUrl();
    // The target is the unique directory created by this test above.
    fs.rmSync(userData, { recursive: true, force: true });
  });

  it('does not query any backup without a persisted canonical checkpoint', async () => {
    await expect(getQsdmBackupRead()).resolves.toBeUndefined();
    expect(mockGet).not.toHaveBeenCalled();
  });

  it('persists only bounded canonical summaries and verifies backup after reload', async () => {
    await saveHistory();
    expect(saved().blocks).toHaveLength(20);
    expect(saved().blocks[0]).toEqual({
      height: 81,
      hash: hashFor(81),
      stateRoot: 'a'.repeat(64),
      timestamp: '2026-09-02T01:03:48.716773996Z',
    });
    expect(fs.readFileSync(savedPath(), 'utf8')).not.toContain('transactions');
    expect(fs.readdirSync(userData)).toEqual(['qsdm-read-checkpoint.json']);
    acceptBackup();
    const result = await getQsdmBackupRead();
    expect(result).toMatchObject({
      sourceApiUrl: QSDM_DEFAULT_LOCAL_CORE_API_URL,
      checkpointHeight: 100,
      checkpointHash: hashFor(100),
      reportedTip: 100,
      peers: 0,
      blocks: saved().blocks,
    });
    expect(mockGet).toHaveBeenCalledTimes(3);
    expect(mockGet.mock.calls[2][0]).toBe(
      `${QSDM_DEFAULT_LOCAL_CORE_API_URL}/chain/blocks?from=81&to=100&limit=20`
    );
  });

  it('returns canonical metadata even if the backup claims different metadata or a later tip', async () => {
    await saveHistory();
    const altered = history().map((block) => ({
      ...block,
      state_root: 'b'.repeat(64),
      timestamp: '2026-09-03T01:00:00.000Z',
    }));
    acceptBackup(altered, 999999);
    const result = await getQsdmBackupRead();
    expect(result?.reportedTip).toBe(999999);
    expect(result?.blocks).toEqual(saved().blocks);
  });

  it('uses GET only and never changes the shared write endpoint', async () => {
    await saveHistory();
    setQsdmRuntimeCoreApiUrl(QSDM_CANONICAL_API_URL);
    acceptBackup();
    await getQsdmBackupRead();
    expect(getQsdmRuntimeCoreApiUrl()).toBe(QSDM_CANONICAL_API_URL);
    expect(axios.post).not.toHaveBeenCalled();
    mockGet.mock.calls.forEach(([, options]) => {
      expect(options).toMatchObject({ timeout: 4000, maxRedirects: 0 });
    });
  });

  it('rejects a fork sharing genesis but differing at the checkpoint', async () => {
    await saveHistory();
    const fork = history();
    fork[19].hash = 'f'.repeat(64);
    acceptBackup(fork);
    await expect(getQsdmBackupRead()).resolves.toBeUndefined();
  });

  it('rejects history changes even when the claimed checkpoint hash matches', async () => {
    await saveHistory();
    const fork = history();
    fork[0].hash = 'f'.repeat(64);
    acceptBackup(fork);
    await expect(getQsdmBackupRead()).resolves.toBeUndefined();
  });

  it.each([99, -1, 100.5, '100', null, Number.MAX_SAFE_INTEGER + 1])(
    'rejects stale or malformed backup tip %p',
    async (chainTip) => {
      await saveHistory();
      mockGet.mockResolvedValueOnce({
        data: { chain_tip: chainTip, peers: 0 },
      });
      await expect(getQsdmBackupRead()).resolves.toBeUndefined();
      expect(mockGet).toHaveBeenCalledTimes(1);
    }
  );

  it('rejects an incorrect genesis state root', async () => {
    await saveHistory();
    const wrong = genesis();
    wrong.data.blocks[0].state_root = 'f'.repeat(64);
    mockGet
      .mockResolvedValueOnce({ data: { chain_tip: 100, peers: 1 } })
      .mockResolvedValueOnce(wrong);
    await expect(getQsdmBackupRead()).resolves.toBeUndefined();
    expect(mockGet).toHaveBeenCalledTimes(2);
  });

  it('rejects sparse, duplicate and oversized history responses', async () => {
    await saveHistory();
    for (const blocks of [
      history().slice(1),
      [...history().slice(1), history()[19]],
      [...history(), history()[19]],
    ]) {
      mockGet.mockReset();
      acceptBackup(blocks);
      // eslint-disable-next-line no-await-in-loop
      await expect(getQsdmBackupRead()).resolves.toBeUndefined();
    }
  });

  it.each([
    'origin',
    'genesis',
    'future',
    'height',
    'hash',
    'root',
    'timestamp',
  ])('rejects a corrupted persisted checkpoint (%s)', async (corruption) => {
    await saveHistory();
    const checkpoint = saved();
    if (corruption === 'origin')
      checkpoint.canonicalApiUrl = 'https://other.invalid';
    if (corruption === 'genesis') checkpoint.genesisHash = 'f'.repeat(64);
    if (corruption === 'future')
      checkpoint.confirmedAt = new Date(Date.now() + 60000).toISOString();
    if (corruption === 'height') checkpoint.blocks[0].height = 2;
    if (corruption === 'hash') checkpoint.blocks[0].hash = 'invalid';
    if (corruption === 'root') checkpoint.blocks[0].stateRoot = 'invalid';
    if (corruption === 'timestamp') checkpoint.blocks[0].timestamp = 'invalid';
    fs.writeFileSync(savedPath(), JSON.stringify(checkpoint));
    await expect(getQsdmBackupRead()).resolves.toBeUndefined();
    expect(mockGet).not.toHaveBeenCalled();
  });

  it('does not discard an old checkpoint merely because it is historical', async () => {
    await saveHistory();
    const checkpoint = saved();
    checkpoint.confirmedAt = '2026-09-02T01:04:00.000Z';
    fs.writeFileSync(savedPath(), JSON.stringify(checkpoint));
    acceptBackup();
    await expect(getQsdmBackupRead()).resolves.toMatchObject({
      confirmedAt: checkpoint.confirmedAt,
      checkpointHeight: 100,
    });
  });

  it('never records a checkpoint from an alternate origin or malformed tip', async () => {
    await recordQsdmReadCheckpoint(QSDM_DEFAULT_LOCAL_CORE_API_URL, 100);
    await recordQsdmReadCheckpoint(QSDM_CANONICAL_API_URL, 1.5);
    expect(mockGet).not.toHaveBeenCalled();
    expect(fs.existsSync(savedPath())).toBe(false);
  });

  it('retains the previous checkpoint on canonical read failures and rollback', async () => {
    await saveHistory();
    const before = fs.readFileSync(savedPath(), 'utf8');
    mockGet.mockRejectedValueOnce(new Error('timeout'));
    await recordQsdmReadCheckpoint(QSDM_CANONICAL_API_URL, 101);
    await recordQsdmReadCheckpoint(QSDM_CANONICAL_API_URL, 99);
    expect(fs.readFileSync(savedPath(), 'utf8')).toBe(before);
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it('retains the previous checkpoint if replacement conflicts with it', async () => {
    await saveHistory();
    const before = fs.readFileSync(savedPath(), 'utf8');
    const replacement = history(101);
    replacement[18].hash = 'f'.repeat(64);
    mockGet.mockResolvedValueOnce({ data: { blocks: replacement } });
    await recordQsdmReadCheckpoint(QSDM_CANONICAL_API_URL, 101);
    expect(fs.readFileSync(savedPath(), 'utf8')).toBe(before);
  });

  it('retains previous file if atomic replacement fails and cleans the temporary file', async () => {
    await saveHistory();
    const before = fs.readFileSync(savedPath(), 'utf8');
    jest.spyOn(fs, 'renameSync').mockImplementation(() => {
      throw new Error('permission denied');
    });
    mockGet.mockResolvedValueOnce({ data: { blocks: history(101) } });
    await recordQsdmReadCheckpoint(QSDM_CANONICAL_API_URL, 101);
    expect(fs.readFileSync(savedPath(), 'utf8')).toBe(before);
    expect(fs.readdirSync(userData)).toEqual(['qsdm-read-checkpoint.json']);
  });
});
