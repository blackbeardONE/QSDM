import axios from 'axios';

import {
  QSDM_CANONICAL_API_URL,
  QSDM_CANONICAL_GENESIS_HASH,
  QSDM_CANONICAL_GENESIS_STATE_ROOT,
  QSDM_CANONICAL_MAX_HEIGHT_AHEAD,
  QSDM_CANONICAL_MAX_HEIGHT_LAG,
  QSDM_CORE_API_URL,
  setQsdmRuntimeCoreApiUrl,
} from 'config/qsdm';
import {
  QsdmCanonicalChainReason,
  QsdmCanonicalChainSafety,
} from 'models/api/qsdm';

type CoreStatus = {
  chain_tip?: number | string;
  peers?: number | string;
};

type ChainBlock = {
  height?: number;
  hash?: string;
  state_root?: string;
};

type ChainBlocksResponse = {
  blocks?: ChainBlock[];
};

type NodeIdentity = {
  apiUrl: string;
  tip: number;
  peers: number;
  genesis: ChainBlock;
};

type VerificationFailure = {
  reason: QsdmCanonicalChainReason;
  detail: string;
  target?: NodeIdentity;
  canonical?: NodeIdentity;
  commonHeight?: number;
  localBlockHash?: string;
  canonicalBlockHash?: string;
};

type VerificationResult =
  | {
      ok: true;
      target: NodeIdentity;
      canonical: NodeIdentity;
      commonHeight: number;
      localBlockHash: string;
      canonicalBlockHash: string;
    }
  | ({ ok: false } & VerificationFailure);

type CanonicalSafetyOptions = {
  forceRefresh?: boolean;
  allowGatewayFallback?: boolean;
};

const REQUEST_TIMEOUT_MS = 8_000;
const SAFETY_CACHE_MS = 10_000;
const reportCache = new Map<
  string,
  { expiresAt: number; report: QsdmCanonicalChainSafety }
>();
const reportRefreshes = new Map<string, Promise<QsdmCanonicalChainSafety>>();

const trimTrailingSlash = (value: string) => value.replace(/\/+$/, '');
const sameApiUrl = (left: string, right: string) =>
  trimTrailingSlash(left) === trimTrailingSlash(right);

const asNumber = (value: number | string | undefined) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : undefined;
};

const getErrorMessage = (error: unknown) => {
  if (typeof axios.isAxiosError === 'function' && axios.isAxiosError(error)) {
    const data = error.response?.data as
      | { error?: string; message?: string }
      | string
      | undefined;
    if (typeof data === 'string' && data.trim()) return data.trim();
    if (data && typeof data === 'object') {
      return data.message || data.error || error.message;
    }
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
};

const getStatus = async (apiUrl: string): Promise<CoreStatus> => {
  const response = await axios.get<CoreStatus>(`${apiUrl}/status`, {
    timeout: REQUEST_TIMEOUT_MS,
  });
  return response.data;
};

const getBlock = async (
  apiUrl: string,
  height: number
): Promise<ChainBlock & { hash: string }> => {
  const response = await axios.get<ChainBlocksResponse>(
    `${apiUrl}/chain/blocks?from=${height}&to=${height}&limit=1`,
    { timeout: REQUEST_TIMEOUT_MS }
  );
  const block = response.data.blocks?.find(
    (candidate) => candidate.height === height
  );
  if (!block?.hash) {
    throw new Error(`Core did not return block ${height}`);
  }
  return block as ChainBlock & { hash: string };
};

const getNodeIdentity = async (apiUrl: string): Promise<NodeIdentity> => {
  const normalizedApiUrl = trimTrailingSlash(apiUrl);
  let status: CoreStatus;
  try {
    status = await getStatus(normalizedApiUrl);
  } catch (error) {
    throw Object.assign(new Error(getErrorMessage(error)), {
      qsdmReason: 'status-unavailable' as QsdmCanonicalChainReason,
    });
  }

  const tip = asNumber(status.chain_tip);
  if (tip === undefined) {
    throw Object.assign(new Error('Core status did not include chain_tip'), {
      qsdmReason: 'status-unavailable' as QsdmCanonicalChainReason,
    });
  }

  let genesis: ChainBlock;
  try {
    genesis = await getBlock(normalizedApiUrl, 0);
  } catch (error) {
    throw Object.assign(new Error(getErrorMessage(error)), {
      qsdmReason: 'genesis-unavailable' as QsdmCanonicalChainReason,
    });
  }

  return {
    apiUrl: normalizedApiUrl,
    tip,
    peers: asNumber(status.peers) || 0,
    genesis,
  };
};

const failureFromError = (
  error: unknown,
  fallbackReason: QsdmCanonicalChainReason
): VerificationFailure => ({
  reason:
    (error as { qsdmReason?: QsdmCanonicalChainReason })?.qsdmReason ||
    fallbackReason,
  detail: getErrorMessage(error),
});

const assertPinnedGenesis = (
  identity: NodeIdentity
): VerificationFailure | undefined => {
  if (identity.genesis.hash !== QSDM_CANONICAL_GENESIS_HASH) {
    return {
      reason: 'genesis-mismatch',
      detail: `Genesis hash ${
        identity.genesis.hash || 'missing'
      } does not match the canonical QSDM network.`,
      target: identity,
    };
  }
  if (identity.genesis.state_root !== QSDM_CANONICAL_GENESIS_STATE_ROOT) {
    return {
      reason: 'state-root-mismatch',
      detail: `Genesis state root ${
        identity.genesis.state_root || 'missing'
      } does not match the canonical QSDM network.`,
      target: identity,
    };
  }
  return undefined;
};

const verifyTargetAgainstCanonical = async (
  targetApiUrl: string,
  canonical: NodeIdentity
): Promise<VerificationResult> => {
  let target: NodeIdentity;
  try {
    target = sameApiUrl(targetApiUrl, canonical.apiUrl)
      ? canonical
      : await getNodeIdentity(targetApiUrl);
  } catch (error) {
    return { ok: false, ...failureFromError(error, 'status-unavailable') };
  }

  const genesisFailure = assertPinnedGenesis(target);
  if (genesisFailure) {
    return { ok: false, ...genesisFailure, canonical };
  }

  if (!sameApiUrl(target.apiUrl, canonical.apiUrl) && target.peers < 1) {
    return {
      ok: false,
      reason: 'isolated-node',
      detail:
        'The selected QSDM Core has no connected peers and cannot prove it is following the live network.',
      target,
      canonical,
    };
  }

  const heightDelta = canonical.tip - target.tip;
  if (heightDelta > QSDM_CANONICAL_MAX_HEIGHT_LAG) {
    return {
      ok: false,
      reason: 'height-lag',
      detail: `The selected QSDM Core is ${heightDelta} blocks behind the canonical network.`,
      target,
      canonical,
    };
  }
  if (heightDelta < -QSDM_CANONICAL_MAX_HEIGHT_AHEAD) {
    return {
      ok: false,
      reason: 'height-ahead',
      detail: `The selected QSDM Core is ${Math.abs(
        heightDelta
      )} blocks ahead of the canonical network.`,
      target,
      canonical,
    };
  }

  const commonHeight = Math.min(target.tip, canonical.tip);
  try {
    const canonicalBlock = await getBlock(canonical.apiUrl, commonHeight);
    const localBlock = sameApiUrl(target.apiUrl, canonical.apiUrl)
      ? canonicalBlock
      : await getBlock(target.apiUrl, commonHeight);

    if (localBlock.hash !== canonicalBlock.hash) {
      return {
        ok: false,
        reason: 'common-block-mismatch',
        detail: `Block ${commonHeight} does not match the canonical QSDM chain.`,
        target,
        canonical,
        commonHeight,
        localBlockHash: localBlock.hash,
        canonicalBlockHash: canonicalBlock.hash,
      };
    }

    return {
      ok: true,
      target,
      canonical,
      commonHeight,
      localBlockHash: localBlock.hash,
      canonicalBlockHash: canonicalBlock.hash,
    };
  } catch (error) {
    return {
      ok: false,
      reason: 'common-block-unavailable',
      detail: getErrorMessage(error),
      target,
      canonical,
      commonHeight,
    };
  }
};

const buildReport = ({
  result,
  effectiveApiUrl,
  usingGatewayFallback,
}: {
  result: VerificationResult;
  effectiveApiUrl: string;
  usingGatewayFallback: boolean;
}): QsdmCanonicalChainSafety => {
  const { target, canonical } = result;
  return {
    safe: result.ok,
    state: result.ok
      ? usingGatewayFallback
        ? 'gateway-fallback'
        : 'canonical'
      : result.reason === 'canonical-source-unavailable'
      ? 'unreachable'
      : 'unsafe',
    reason: result.ok ? undefined : result.reason,
    detail: result.ok ? undefined : result.detail,
    configuredApiUrl: QSDM_CORE_API_URL,
    effectiveApiUrl,
    canonicalApiUrl: QSDM_CANONICAL_API_URL,
    usingGatewayFallback,
    localTip: target?.tip,
    canonicalTip: canonical?.tip,
    heightDelta: target && canonical ? canonical.tip - target.tip : undefined,
    peers: target?.peers,
    commonHeight: result.commonHeight,
    localBlockHash: result.localBlockHash,
    canonicalBlockHash: result.canonicalBlockHash,
    genesisHash: target?.genesis.hash,
    checkedAt: new Date().toISOString(),
  };
};

const getCacheKey = (allowGatewayFallback: boolean) =>
  allowGatewayFallback ? 'fallback' : 'configured-only';

export const clearQsdmCanonicalChainSafetyCache = () => {
  reportCache.clear();
  reportRefreshes.clear();
};

export const getQsdmCanonicalChainSafety = async ({
  forceRefresh = false,
  allowGatewayFallback = true,
}: CanonicalSafetyOptions = {}): Promise<QsdmCanonicalChainSafety> => {
  const cacheKey = getCacheKey(allowGatewayFallback);
  const cached = reportCache.get(cacheKey);
  if (!forceRefresh && cached && cached.expiresAt > Date.now()) {
    return cached.report;
  }

  const activeRefresh = reportRefreshes.get(cacheKey);
  if (!forceRefresh && activeRefresh) {
    return activeRefresh;
  }

  const refresh = (async () => {
    let canonical: NodeIdentity;
    try {
      canonical = await getNodeIdentity(QSDM_CANONICAL_API_URL);
      const canonicalGenesisFailure = assertPinnedGenesis(canonical);
      if (canonicalGenesisFailure) {
        throw Object.assign(new Error(canonicalGenesisFailure.detail), {
          qsdmReason: canonicalGenesisFailure.reason,
        });
      }
    } catch (error) {
      const failure = failureFromError(error, 'canonical-source-unavailable');
      const report = buildReport({
        result: {
          ok: false,
          reason: 'canonical-source-unavailable',
          detail: `Canonical QSDM source could not be verified: ${failure.detail}`,
        },
        effectiveApiUrl: QSDM_CORE_API_URL,
        usingGatewayFallback: false,
      });
      if (allowGatewayFallback) setQsdmRuntimeCoreApiUrl();
      reportCache.set(cacheKey, {
        expiresAt: Date.now() + SAFETY_CACHE_MS,
        report,
      });
      return report;
    }

    const configuredResult = await verifyTargetAgainstCanonical(
      QSDM_CORE_API_URL,
      canonical
    );
    if (configuredResult.ok) {
      if (allowGatewayFallback) setQsdmRuntimeCoreApiUrl();
      const report = buildReport({
        result: configuredResult,
        effectiveApiUrl: QSDM_CORE_API_URL,
        usingGatewayFallback: false,
      });
      reportCache.set(cacheKey, {
        expiresAt: Date.now() + SAFETY_CACHE_MS,
        report,
      });
      return report;
    }

    if (
      allowGatewayFallback &&
      !sameApiUrl(QSDM_CORE_API_URL, QSDM_CANONICAL_API_URL)
    ) {
      const canonicalResult = await verifyTargetAgainstCanonical(
        QSDM_CANONICAL_API_URL,
        canonical
      );
      if (canonicalResult.ok) {
        setQsdmRuntimeCoreApiUrl(QSDM_CANONICAL_API_URL);
        const report = buildReport({
          result: canonicalResult,
          effectiveApiUrl: QSDM_CANONICAL_API_URL,
          usingGatewayFallback: false,
        });
        report.detail = `The configured Core was rejected (${configuredResult.detail}). Hive is using the verified canonical QSDM Core.`;
        reportCache.set(cacheKey, {
          expiresAt: Date.now() + SAFETY_CACHE_MS,
          report,
        });
        return report;
      }
    }

    if (allowGatewayFallback) setQsdmRuntimeCoreApiUrl();
    const report = buildReport({
      result: configuredResult,
      effectiveApiUrl: QSDM_CORE_API_URL,
      usingGatewayFallback: false,
    });
    reportCache.set(cacheKey, {
      expiresAt: Date.now() + SAFETY_CACHE_MS,
      report,
    });
    return report;
  })();

  if (!forceRefresh) {
    reportRefreshes.set(cacheKey, refresh);
  }
  try {
    return await refresh;
  } finally {
    if (reportRefreshes.get(cacheKey) === refresh) {
      reportRefreshes.delete(cacheKey);
    }
  }
};

export const assertQsdmCanonicalChainSafety = async (
  options: CanonicalSafetyOptions = {}
) => {
  const safety = await getQsdmCanonicalChainSafety(options);
  if (!safety.safe) {
    throw new Error(
      `QSDM Core is not on the verified canonical network (${
        safety.reason || safety.state
      }). ${safety.detail || ''} Value-bearing actions are blocked.`.trim()
    );
  }
  return safety;
};
