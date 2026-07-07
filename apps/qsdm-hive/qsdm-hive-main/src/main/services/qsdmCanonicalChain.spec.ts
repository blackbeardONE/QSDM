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

import {
  assertQsdmCanonicalChainSafety,
  clearQsdmCanonicalChainSafetyCache,
  getQsdmCanonicalChainSafety,
} from './qsdmCanonicalChain';

jest.mock('axios', () => ({
  get: jest.fn(),
  isAxiosError: jest.fn(() => false),
}));

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
      .mockResolvedValueOnce(block(100, 'shared-tip'))
      .mockResolvedValueOnce(block(100, 'shared-tip'));

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
      .mockResolvedValueOnce(block(100, 'shared-tip'))
      .mockResolvedValueOnce(block(100, 'shared-tip'));

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
      .mockResolvedValueOnce(block(100, 'fork-tip'))
      .mockResolvedValueOnce(block(100, 'canonical-tip'));

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
      .mockResolvedValueOnce(block(100, 'shared-tip'));

    const report = await getQsdmCanonicalChainSafety();

    expect(report).toMatchObject({
      safe: true,
      state: 'canonical',
      effectiveApiUrl: QSDM_CANONICAL_API_URL,
      usingGatewayFallback: false,
    });
    expect(getQsdmRuntimeCoreApiUrl()).toBe(QSDM_CANONICAL_API_URL);
  });

  it('fails closed when the canonical source cannot be verified', async () => {
    mockedGet.mockRejectedValueOnce(new Error('canonical offline'));

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
