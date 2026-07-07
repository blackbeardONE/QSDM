import {
  QSDM_BRIDGE_CONFIG,
  getQsdmCoreConnectionMode,
  getQsdmRuntimeCoreApiUrl,
  QSDM_TASK_RPC_HEALTH_URL,
  QSDM_TASK_RPC_STATUS_URL,
} from 'config/qsdm';
import { getQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import {
  getQsdmReadErrorMessage,
  qsdmGetJson,
} from 'main/services/qsdmHttpRead';
import { getQsdmTaskActionSignerStatus } from 'main/services/qsdmTaskActionSigner';
import {
  QsdmCoreStatusResponse,
  QsdmNodeStatusResponse,
} from 'models/api/qsdm';

type SafeGetResult =
  | {
      ok: true;
      data: unknown;
    }
  | {
      ok: false;
      error: string;
    };

const CORE_STATUS_TIMEOUT_MS = 8_000;
const CONFIRMED_STATUS_GRACE_MS = 2 * 60_000;

type ConfirmedCoreSnapshot = {
  confirmedAt: number;
  health?: unknown;
  status?: QsdmNodeStatusResponse;
};

let lastConfirmedCoreSnapshot: ConfirmedCoreSnapshot | undefined;
let consecutiveCoreFailures = 0;

export const clearQsdmCoreStatusSnapshot = () => {
  lastConfirmedCoreSnapshot = undefined;
  consecutiveCoreFailures = 0;
};

const safeGet = async (
  url: string,
  timeout = CORE_STATUS_TIMEOUT_MS
): Promise<SafeGetResult> => {
  try {
    const data = await qsdmGetJson(url, { timeout });
    return { ok: true, data };
  } catch (error) {
    return { ok: false, error: getQsdmReadErrorMessage(error) };
  }
};

const getStatusError = (
  healthResult: SafeGetResult,
  statusResult: SafeGetResult
) => {
  if (!healthResult.ok && !statusResult.ok) return healthResult.error;
  if (!statusResult.ok) return statusResult.error;
  return undefined;
};

export const getQsdmCoreStatus = async (): Promise<QsdmCoreStatusResponse> => {
  const canonicalSafety = await getQsdmCanonicalChainSafety();
  const effectiveCoreApiUrl = getQsdmRuntimeCoreApiUrl();
  const statusResult = await safeGet(`${effectiveCoreApiUrl}/status`);
  const healthResult = statusResult.ok
    ? ({ ok: true, data: undefined } as SafeGetResult)
    : await safeGet(`${effectiveCoreApiUrl}/health`);
  const taskRpcUsesEffectiveCore =
    QSDM_TASK_RPC_HEALTH_URL === `${effectiveCoreApiUrl}/health` &&
    QSDM_TASK_RPC_STATUS_URL === `${effectiveCoreApiUrl}/status`;
  const taskRpcHealthResult = taskRpcUsesEffectiveCore
    ? healthResult
    : await safeGet(QSDM_TASK_RPC_HEALTH_URL);
  const taskRpcStatusResult = taskRpcUsesEffectiveCore
    ? statusResult
    : taskRpcHealthResult.ok
    ? await safeGet(QSDM_TASK_RPC_STATUS_URL)
    : taskRpcHealthResult;

  const directTaskRpcHealthy = taskRpcHealthResult.ok || taskRpcStatusResult.ok;
  const shouldUseCoreAsTaskRpc =
    !directTaskRpcHealthy && QSDM_BRIDGE_CONFIG.runtimeMode === 'qsdm-native';
  const coreHealthy =
    canonicalSafety.safe && (healthResult.ok || statusResult.ok);
  const checkedAt = new Date().toISOString();

  if (coreHealthy) {
    consecutiveCoreFailures = 0;
    lastConfirmedCoreSnapshot = {
      confirmedAt: Date.now(),
      health: healthResult.ok ? healthResult.data : undefined,
      status: statusResult.ok
        ? (statusResult.data as QsdmNodeStatusResponse)
        : undefined,
    };
  } else {
    consecutiveCoreFailures += 1;
  }

  const transientSafetyFailure =
    canonicalSafety.state === 'unreachable' ||
    canonicalSafety.reason === 'status-unavailable' ||
    canonicalSafety.reason === 'genesis-unavailable' ||
    canonicalSafety.reason === 'common-block-unavailable';
  const canUseConfirmedSnapshot = Boolean(
    lastConfirmedCoreSnapshot &&
      Date.now() - lastConfirmedCoreSnapshot.confirmedAt <=
        CONFIRMED_STATUS_GRACE_MS &&
      (!healthResult.ok || !statusResult.ok || transientSafetyFailure)
  );
  const connectionState: QsdmCoreStatusResponse['connectionState'] = coreHealthy
    ? 'online'
    : canUseConfirmedSnapshot
    ? 'degraded'
    : 'offline';
  const confirmedAt = lastConfirmedCoreSnapshot
    ? new Date(lastConfirmedCoreSnapshot.confirmedAt).toISOString()
    : undefined;

  return {
    apiUrl: QSDM_BRIDGE_CONFIG.apiUrl,
    coreApiUrl: effectiveCoreApiUrl,
    configuredCoreApiUrl: QSDM_BRIDGE_CONFIG.coreApiUrl,
    effectiveCoreApiUrl,
    coreConnectionMode: getQsdmCoreConnectionMode(
      effectiveCoreApiUrl,
      QSDM_BRIDGE_CONFIG.gatewayApiUrl
    ),
    configuredCoreConnectionMode: QSDM_BRIDGE_CONFIG.coreConnectionMode,
    gatewayApiUrl: QSDM_BRIDGE_CONFIG.gatewayApiUrl,
    canonicalApiUrl: QSDM_BRIDGE_CONFIG.canonicalApiUrl,
    canonicalSafety,
    dashboardUrl: QSDM_BRIDGE_CONFIG.dashboardUrl,
    walletAddress: QSDM_BRIDGE_CONFIG.walletAddress || undefined,
    tokenSymbol: QSDM_BRIDGE_CONFIG.tokenSymbol,
    protocolSymbol: QSDM_BRIDGE_CONFIG.protocolSymbol,
    runtimeMode: QSDM_BRIDGE_CONFIG.runtimeMode,
    healthy: coreHealthy,
    connectionState,
    lastSuccessfulAt: confirmedAt,
    consecutiveFailures: consecutiveCoreFailures,
    health: healthResult.ok
      ? healthResult.data
      : canUseConfirmedSnapshot
      ? lastConfirmedCoreSnapshot?.health
      : undefined,
    status: statusResult.ok
      ? (statusResult.data as QsdmNodeStatusResponse)
      : canUseConfirmedSnapshot
      ? lastConfirmedCoreSnapshot?.status
      : undefined,
    taskRpcHealthy:
      directTaskRpcHealthy || (shouldUseCoreAsTaskRpc && coreHealthy),
    taskRpcHealth: taskRpcHealthResult.ok
      ? taskRpcHealthResult.data
      : shouldUseCoreAsTaskRpc && healthResult.ok
      ? healthResult.data
      : undefined,
    taskRpcStatus: taskRpcStatusResult.ok
      ? taskRpcStatusResult.data
      : shouldUseCoreAsTaskRpc && statusResult.ok
      ? statusResult.data
      : undefined,
    taskRpcError: directTaskRpcHealthy
      ? undefined
      : getStatusError(taskRpcHealthResult, taskRpcStatusResult),
    taskSigner: getQsdmTaskActionSignerStatus(),
    error: canonicalSafety.safe
      ? getStatusError(healthResult, statusResult)
      : canonicalSafety.detail || canonicalSafety.reason,
    checkedAt,
  };
};
