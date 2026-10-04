/**
 * @jest-environment node
 */

import axios from 'axios';

import {
  getQsdmRuntimeCoreApiUrl,
  QSDM_CANONICAL_API_URL,
  QSDM_CANONICAL_GENESIS_HASH,
  QSDM_CANONICAL_GENESIS_STATE_ROOT,
  setQsdmRuntimeCoreApiUrl,
} from 'config/qsdm';

import { getQsdmBackupRead, recordQsdmReadCheckpoint } from './qsdmBackupRead';

import {
  assertQsdmCanonicalChainSafety,
  clearQsdmCanonicalChainSafetyCache,
  getQsdmCanonicalChainSafety,
} from './qsdmCanonicalChain';

jest.mock('axios', () => ({
  get: jest.fn(),
  isAxiosError: jest.fn((error) => Boolean(error?.isAxiosError)),
}));

jest.mock('./qsdmBackupRead', () => ({
  getQsdmBackupRead: jest.fn().mockResolvedValue(undefined),
  recordQsdmReadCheckpoint: jest.fn().mockResolvedValue(undefined),
}));

const unavailable = () =>
  Object.assign(new Error('canonical offline'), {
    isAxiosError: true,
    code: 'ECONNREFUSED',
  });
const mockedGet = axios.get as jest.Mock;

const status = (chainTip: number, peers: number) => ({
  data: { chain_tip: chainTip, peers },
});

const block = (
  height: number,
  hash: string,
  stateRoot = QSDM_CANONICAL_GENESIS_STATE_ROOT
) => ({
  data: {
    blocks: [{ height, hash, state_root: stateRoot }],
  },
});

const canonicalGenesis = () => block(0, QSDM_CANONICAL_GENESIS_HASH);

describe('qsdmCanonicalChain', () => {
  beforeEach(() => {
    mockedGet.mockReset();
    (getQsdmBackupRead as jest.Mock).mockReset().mockResolvedValue(undefined);
    (recordQsdmReadCheckpoint as jest.Mock).mockClear();
    clearQsdmCanonicalChainSafetyCache();
    setQsdmRuntimeCoreApiUrl();
  });

  afterEach(() => {
    setQsdmRuntimeCoreApiUrl();
  });

  it('accepts a synchronized peer-connected local Core', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(status(100, 1))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(block(100, 'a'.repeat(64)))
      .mockResolvedValueOnce(block(100, 'a'.repeat(64)));

    const report = await getQsdmCanonicalChainSafety({
      allowGatewayFallback: false,
    });

    expect(report).toMatchObject({
      safe: true,
      state: 'canonical',
      localTip: 100,
      canonicalTip: 100,
      peers: 1,
      commonHeight: 100,
      usingGatewayFallback: false,
    });
  });

  it('shares one canonical verification across concurrent callers', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(status(100, 1))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(block(100, 'a'.repeat(64)))
      .mockResolvedValueOnce(block(100, 'a'.repeat(64)));

    const first = getQsdmCanonicalChainSafety({
      allowGatewayFallback: false,
    });
    const second = getQsdmCanonicalChainSafety({
      allowGatewayFallback: false,
    });

    await expect(first).resolves.toMatchObject({ safe: true });
    await expect(second).resolves.toMatchObject({ safe: true });
    expect(mockedGet).toHaveBeenCalledTimes(6);
  });

  it('rejects a fork that shares genesis but not the latest common block', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(status(100, 1))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(block(100, 'b'.repeat(64)))
      .mockResolvedValueOnce(block(100, 'c'.repeat(64)));

    const report = await getQsdmCanonicalChainSafety({
      allowGatewayFallback: false,
    });

    expect(report).toMatchObject({
      safe: false,
      state: 'unsafe',
      reason: 'common-block-mismatch',
      commonHeight: 100,
    });
  });

  it('rejects an isolated non-authoritative Core', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(status(100, 0))
      .mockResolvedValueOnce(canonicalGenesis());

    const report = await getQsdmCanonicalChainSafety({
      allowGatewayFallback: false,
    });

    expect(report).toMatchObject({
      safe: false,
      reason: 'isolated-node',
      peers: 0,
    });
  });

  it('rejects a Core outside the canonical height window', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(status(90, 1))
      .mockResolvedValueOnce(canonicalGenesis());

    const report = await getQsdmCanonicalChainSafety({
      allowGatewayFallback: false,
    });

    expect(report).toMatchObject({
      safe: false,
      reason: 'height-lag',
      localTip: 90,
      canonicalTip: 100,
      heightDelta: 10,
    });
  });

  it('selects the canonical Core when the configured local Core is unavailable', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockRejectedValueOnce(new Error('connection refused'))
      .mockResolvedValueOnce(block(100, 'a'.repeat(64)));

    const report = await getQsdmCanonicalChainSafety();

    expect(report).toMatchObject({
      safe: true,
      state: 'canonical',
      effectiveApiUrl: QSDM_CANONICAL_API_URL,
      usingGatewayFallback: false,
    });
    expect(getQsdmRuntimeCoreApiUrl()).toBe(QSDM_CANONICAL_API_URL);
  });

  it('offers backup reads during an outage without granting write authority or rerouting writes', async () => {
    const initialUrl = getQsdmRuntimeCoreApiUrl();
    mockedGet.mockRejectedValueOnce(unavailable());
    (getQsdmBackupRead as jest.Mock).mockResolvedValue({
      checkpointHeight: 100,
      sourceApiUrl: 'http://127.0.0.1:8080/api/v1',
    });
    const report = await getQsdmCanonicalChainSafety();
    expect(report).toMatchObject({
      safe: false,
      state: 'unreachable',
      backupRead: { checkpointHeight: 100 },
    });
    expect(getQsdmRuntimeCoreApiUrl()).toBe(initialUrl);
    await expect(assertQsdmCanonicalChainSafety()).rejects.toThrow(
      'Value-bearing actions are blocked'
    );
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockRejectedValueOnce(unavailable())
      .mockResolvedValueOnce(block(100, 'a'.repeat(64)));
    const recovered = await getQsdmCanonicalChainSafety({ forceRefresh: true });
    expect(recovered.safe).toBe(true);
    expect(recovered.backupRead).toBeUndefined();
    expect(recordQsdmReadCheckpoint).toHaveBeenCalledWith(
      QSDM_CANONICAL_API_URL,
      100
    );
  });

  it.each([undefined, null, '', -1, 1.5, 'bad', Number.MAX_SAFE_INTEGER + 1])(
    'rejects malformed canonical tip %s without consulting backup',
    async (tip) => {
      mockedGet.mockResolvedValueOnce({ data: { chain_tip: tip } });
      const report = await getQsdmCanonicalChainSafety();
      expect(report).toMatchObject({
        safe: false,
        state: 'unsafe',
        reason: 'invalid-response',
      });
      expect(getQsdmBackupRead).not.toHaveBeenCalled();
    }
  );

  it('rejects malformed common-block data without treating it as a transient outage', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce(status(100, 1))
      .mockResolvedValueOnce(canonicalGenesis())
      .mockResolvedValueOnce({
        data: { blocks: [{ height: 100, hash: 'invalid' }] },
      });
    const report = await getQsdmCanonicalChainSafety({
      allowGatewayFallback: false,
    });
    expect(report).toMatchObject({
      safe: false,
      state: 'unsafe',
      reason: 'invalid-response',
    });
    expect(getQsdmBackupRead).not.toHaveBeenCalled();
  });

  it('does not hide TLS verification failure with backup history', async () => {
    mockedGet.mockRejectedValueOnce({
      isAxiosError: true,
      code: 'CERT_HAS_EXPIRED',
      message: 'TLS failed',
    });
    const report = await getQsdmCanonicalChainSafety();
    expect(report).toMatchObject({ safe: false, state: 'unsafe' });
    expect(getQsdmBackupRead).not.toHaveBeenCalled();
  });

  it('does not hide a wrong canonical genesis with backup reads', async () => {
    mockedGet
      .mockResolvedValueOnce(status(100, 2))
      .mockResolvedValueOnce(block(0, 'f'.repeat(64)));
    const report = await getQsdmCanonicalChainSafety();
    expect(report).toMatchObject({
      safe: false,
      state: 'unsafe',
      reason: 'genesis-mismatch',
    });
    expect(getQsdmBackupRead).not.toHaveBeenCalled();
  });

  it.each([401, 403, 404])(
    'does not turn HTTP %s into backup authority',
    async (httpStatus) => {
      mockedGet.mockRejectedValueOnce({
        isAxiosError: true,
        response: { status: httpStatus },
        message: 'rejected',
      });
      const report = await getQsdmCanonicalChainSafety();
      expect(report.safe).toBe(false);
      expect(getQsdmBackupRead).not.toHaveBeenCalled();
    }
  );

  it('fails closed when the canonical source cannot be verified', async () => {
    mockedGet.mockRejectedValueOnce(unavailable());

    const report = await getQsdmCanonicalChainSafety();

    expect(report).toMatchObject({
      safe: false,
      state: 'unreachable',
      reason: 'canonical-source-unavailable',
    });
    await expect(assertQsdmCanonicalChainSafety()).rejects.toThrow(
      'Value-bearing actions are blocked'
    );
  });
});
