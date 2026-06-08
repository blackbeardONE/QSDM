import {
  NATIVE_TOKEN_PROTOCOL_SYMBOL,
  NATIVE_TOKEN_SYMBOL,
} from './nativeToken';

type QsdmRuntimeMode = 'k2-compat' | 'qsdm-native';

const readEnv = (key: string, fallback: string) => {
  const value = process.env[key];
  return value?.trim() || fallback;
};

const trimTrailingSlash = (value: string) => value.replace(/\/+$/, '');
const trimLeadingSlash = (value: string) => value.replace(/^\/+/, '');
const preferIpv4Localhost = (value: string) =>
  value.replace('://localhost', '://127.0.0.1');

export const QSDM_CORE_API_URL = trimTrailingSlash(
  preferIpv4Localhost(
    readEnv('QSDM_CORE_API_URL', 'http://127.0.0.1:8080/api/v1')
  )
);

export const QSDM_GATEWAY_API_URL = trimTrailingSlash(
  readEnv(
    'QSDM_GATEWAY_API_URL',
    'https://api.qsdm.tech/attest/home-validator/api/v1'
  )
);

export const QSDM_HIVE_API_URL = trimTrailingSlash(
  readEnv(
    'QSDM_HIVE_API_URL',
    process.env.NODE_ENV === 'test' ? QSDM_CORE_API_URL : QSDM_GATEWAY_API_URL
  )
);

export const QSDM_DASHBOARD_URL = trimTrailingSlash(
  readEnv('QSDM_DASHBOARD_URL', 'http://localhost:8081')
);

export const QSDM_WALLET_ADDRESS = readEnv('QSDM_WALLET_ADDRESS', '');

export const QSDM_TASK_ACTION_SENDER = readEnv(
  'QSDM_TASK_ACTION_SENDER',
  QSDM_WALLET_ADDRESS
);

export const QSDM_ENABLE_LOCAL_SIGNED_LOOP =
  readEnv('QSDM_ENABLE_LOCAL_SIGNED_LOOP', '0') === '1';

export const QSDM_TASK_RUNTIME_MODE = readEnv(
  'QSDM_TASK_RUNTIME_MODE',
  'qsdm-native'
) as QsdmRuntimeMode;

export const QSDM_CELL_DECIMALS = 9;

export const buildQsdmApiUrl = (path: string) =>
  `${QSDM_HIVE_API_URL}/${trimLeadingSlash(path)}`;

export const buildQsdmCoreApiUrl = (path: string) =>
  `${QSDM_CORE_API_URL}/${trimLeadingSlash(path)}`;

export const QSDM_CORE_HEALTH_URL = buildQsdmCoreApiUrl('/health');
export const QSDM_CORE_STATUS_URL = buildQsdmCoreApiUrl('/status');
export const QSDM_TASK_RPC_HEALTH_URL = buildQsdmApiUrl('/health');
export const QSDM_TASK_RPC_STATUS_URL = buildQsdmApiUrl('/status');

export const QSDM_BRIDGE_CONFIG = {
  apiUrl: QSDM_HIVE_API_URL,
  coreApiUrl: QSDM_CORE_API_URL,
  gatewayApiUrl: QSDM_GATEWAY_API_URL,
  dashboardUrl: QSDM_DASHBOARD_URL,
  walletAddress: QSDM_WALLET_ADDRESS,
  taskActionSender: QSDM_TASK_ACTION_SENDER,
  localSignedLoopEnabled: QSDM_ENABLE_LOCAL_SIGNED_LOOP,
  healthUrl: QSDM_CORE_HEALTH_URL,
  statusUrl: QSDM_CORE_STATUS_URL,
  taskRpcHealthUrl: QSDM_TASK_RPC_HEALTH_URL,
  taskRpcStatusUrl: QSDM_TASK_RPC_STATUS_URL,
  runtimeMode: QSDM_TASK_RUNTIME_MODE,
  tokenSymbol: NATIVE_TOKEN_SYMBOL,
  protocolSymbol: NATIVE_TOKEN_PROTOCOL_SYMBOL,
  cellDecimals: QSDM_CELL_DECIMALS,
};
