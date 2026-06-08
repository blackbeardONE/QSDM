import axios from 'axios';
import {
  QSDM_BRIDGE_CONFIG,
  QSDM_CORE_HEALTH_URL,
  QSDM_CORE_STATUS_URL,
  QSDM_TASK_RPC_HEALTH_URL,
  QSDM_TASK_RPC_STATUS_URL,
} from 'config/qsdm';
import { getQsdmTaskActionSignerStatus } from 'main/services/qsdmTaskActionSigner';
import { QsdmCoreStatusResponse } from 'models/api/qsdm';

type SafeGetResult =
  | {
      ok: true;
      data: unknown;
    }
  | {
      ok: false;
      error: string;
    };

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

const safeGet = async (url: string): Promise<SafeGetResult> => {
  try {
    const response = await axios.get(url, { timeout: 10000 });
    return { ok: true, data: response.data };
  } catch (error) {
    return { ok: false, error: getErrorMessage(error) };
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
  const [
    healthResult,
    statusResult,
    taskRpcHealthResult,
    taskRpcStatusResult,
  ] = await Promise.all([
    safeGet(QSDM_CORE_HEALTH_URL),
    safeGet(QSDM_CORE_STATUS_URL),
    safeGet(QSDM_TASK_RPC_HEALTH_URL),
    safeGet(QSDM_TASK_RPC_STATUS_URL),
  ]);

  const coreHealthy = healthResult.ok || statusResult.ok;
  const directTaskRpcHealthy = taskRpcHealthResult.ok || taskRpcStatusResult.ok;
  const shouldUseCoreAsTaskRpc =
    !directTaskRpcHealthy && QSDM_BRIDGE_CONFIG.runtimeMode === 'qsdm-native';

  return {
    apiUrl: QSDM_BRIDGE_CONFIG.apiUrl,
    coreApiUrl: QSDM_BRIDGE_CONFIG.coreApiUrl,
    gatewayApiUrl: QSDM_BRIDGE_CONFIG.gatewayApiUrl,
    dashboardUrl: QSDM_BRIDGE_CONFIG.dashboardUrl,
    walletAddress: QSDM_BRIDGE_CONFIG.walletAddress || undefined,
    tokenSymbol: QSDM_BRIDGE_CONFIG.tokenSymbol,
    protocolSymbol: QSDM_BRIDGE_CONFIG.protocolSymbol,
    runtimeMode: QSDM_BRIDGE_CONFIG.runtimeMode,
    healthy: coreHealthy,
    health: healthResult.ok ? healthResult.data : undefined,
    status: statusResult.ok ? statusResult.data : undefined,
    taskRpcHealthy: directTaskRpcHealthy || (shouldUseCoreAsTaskRpc && coreHealthy),
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
    error: getStatusError(healthResult, statusResult),
    checkedAt: new Date().toISOString(),
  };
};
