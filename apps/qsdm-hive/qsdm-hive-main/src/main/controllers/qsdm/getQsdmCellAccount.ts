import { Event } from 'electron';

import {
  buildQsdmCoreApiUrl,
  getQsdmRuntimeCoreApiUrl,
  QSDM_BRIDGE_CONFIG,
  QSDM_WALLET_ADDRESS,
} from 'config/qsdm';
import { getQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import {
  getQsdmReadErrorMessage,
  qsdmGetJson,
} from 'main/services/qsdmHttpRead';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import { selectQsdmWalletAddress } from 'main/services/qsdmWalletAddress';
import {
  QsdmCellAccountRequest,
  QsdmCellAccountResponse,
  QsdmMiningAccountResponse,
  QsdmWalletBalanceResponse,
  QsdmWalletNonceResponse,
} from 'models/api/qsdm';

type SafeGetResult<T> =
  | {
      ok: true;
      data: T;
    }
  | {
      ok: false;
      error: string;
    };

const buildUrl = (path: string, params: Record<string, string>) => {
  const url = new URL(buildQsdmCoreApiUrl(path));
  Object.entries(params).forEach(([key, value]) => {
    url.searchParams.set(key, value);
  });
  return url.toString();
};

const safeGet = async <T>(url: string): Promise<SafeGetResult<T>> => {
  try {
    const data = await qsdmGetJson<T>(url, { timeout: 4000 });
    return { ok: true, data };
  } catch (error) {
    return { ok: false, error: getQsdmReadErrorMessage(error) };
  }
};

const firstError = (...results: SafeGetResult<unknown>[]) => {
  for (const result of results) {
    if (!result.ok) return result.error;
  }

  return undefined;
};

export const getQsdmCellAccount = async (
  _: Event,
  payload?: QsdmCellAccountRequest
): Promise<QsdmCellAccountResponse> => {
  const address = selectQsdmWalletAddress({
    requestedAddress: payload?.address,
    signerAddress: getQsdmTaskActionSender(),
    configuredAddress: QSDM_WALLET_ADDRESS,
  });
  const checkedAt = new Date().toISOString();
  const canonicalSafety = await getQsdmCanonicalChainSafety();

  if (!address) {
    return {
      configured: false,
      reachable: false,
      apiUrl: QSDM_BRIDGE_CONFIG.apiUrl,
      coreApiUrl: getQsdmRuntimeCoreApiUrl(),
      gatewayApiUrl: QSDM_BRIDGE_CONFIG.gatewayApiUrl,
      dashboardUrl: QSDM_BRIDGE_CONFIG.dashboardUrl,
      tokenSymbol: QSDM_BRIDGE_CONFIG.tokenSymbol,
      checkedAt,
    };
  }

  if (!canonicalSafety.safe) {
    return {
      configured: true,
      reachable: false,
      apiUrl: QSDM_BRIDGE_CONFIG.apiUrl,
      coreApiUrl: getQsdmRuntimeCoreApiUrl(),
      gatewayApiUrl: QSDM_BRIDGE_CONFIG.gatewayApiUrl,
      dashboardUrl: QSDM_BRIDGE_CONFIG.dashboardUrl,
      tokenSymbol: QSDM_BRIDGE_CONFIG.tokenSymbol,
      address,
      error:
        canonicalSafety.detail ||
        'QSDM canonical network could not be verified',
      checkedAt,
    };
  }

  const balanceUrl = buildUrl('/wallet/balance', { address });
  const nonceUrl = buildUrl('/wallet/nonce', { sender: address });
  const miningAccountUrl = buildUrl('/mining/account', { address });

  const [balanceResult, nonceResult, miningAccountResult] = await Promise.all([
    safeGet<QsdmWalletBalanceResponse>(balanceUrl),
    safeGet<QsdmWalletNonceResponse>(nonceUrl),
    safeGet<QsdmMiningAccountResponse>(miningAccountUrl),
  ]);

  return {
    configured: true,
    reachable: balanceResult.ok || nonceResult.ok || miningAccountResult.ok,
    apiUrl: QSDM_BRIDGE_CONFIG.apiUrl,
    coreApiUrl: getQsdmRuntimeCoreApiUrl(),
    gatewayApiUrl: QSDM_BRIDGE_CONFIG.gatewayApiUrl,
    dashboardUrl: QSDM_BRIDGE_CONFIG.dashboardUrl,
    tokenSymbol: QSDM_BRIDGE_CONFIG.tokenSymbol,
    address,
    balance: balanceResult.ok ? balanceResult.data.balance : undefined,
    balanceSource: balanceResult.ok ? balanceResult.data.source : undefined,
    nonce: nonceResult.ok ? nonceResult.data.nonce : undefined,
    nextNonce: nonceResult.ok ? nonceResult.data.next : undefined,
    miningAccount: miningAccountResult.ok
      ? miningAccountResult.data
      : undefined,
    error: firstError(balanceResult, nonceResult, miningAccountResult),
    checkedAt,
  };
};
