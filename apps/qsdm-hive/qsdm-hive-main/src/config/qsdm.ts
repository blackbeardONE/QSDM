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

export type QsdmCoreConnectionMode = 'local' | 'gateway' | 'custom';

export const QSDM_DEFAULT_LOCAL_CORE_API_URL = 'http://127.0.0.1:8080/api/v1';

export const QSDM_OFFICIAL_GATEWAY_API_URL =
  'https://api.qsdm.tech/attest/home-validator/api/v1';

export const QSDM_OFFICIAL_CANONICAL_API_URL = 'https://api.qsdm.tech/api/v1';

export const QSDM_CANONICAL_GENESIS_HASH =
  'b6119386bb6918d0716ab9d7f51864b58c20d542e6beab261151e8d4f9a8feb6';

export const QSDM_CANONICAL_GENESIS_STATE_ROOT =
  '1667aa6937305e49b2bf489aec03dbb6a12ecddef89c1ad884ebe368d29c3998';

export const QSDM_GATEWAY_API_URL = trimTrailingSlash(
  readEnv('QSDM_GATEWAY_API_URL', QSDM_OFFICIAL_GATEWAY_API_URL)
);

export const QSDM_CANONICAL_API_URL = trimTrailingSlash(
  readEnv('QSDM_CANONICAL_API_URL', QSDM_OFFICIAL_CANONICAL_API_URL)
);

const readNonNegativeInteger = (key: string, fallback: number) => {
  const parsed = Number.parseInt(readEnv(key, String(fallback)), 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
};

export const QSDM_CANONICAL_MAX_HEIGHT_LAG = readNonNegativeInteger(
  'QSDM_CANONICAL_MAX_HEIGHT_LAG',
  3
);

export const QSDM_CANONICAL_MAX_HEIGHT_AHEAD = readNonNegativeInteger(
  'QSDM_CANONICAL_MAX_HEIGHT_AHEAD',
  1
);

export const resolveQsdmCoreApiUrl = ({
  platform = process.platform,
  nodeEnv = process.env.NODE_ENV,
  configuredUrl = process.env.QSDM_CORE_API_URL,
  canonicalUrl = QSDM_CANONICAL_API_URL,
  localUrl = QSDM_DEFAULT_LOCAL_CORE_API_URL,
}: {
  platform?: NodeJS.Platform;
  nodeEnv?: string;
  configuredUrl?: string;
  canonicalUrl?: string;
  localUrl?: string;
} = {}) => {
  const defaultUrl =
    platform === 'linux' && nodeEnv !== 'test' ? canonicalUrl : localUrl;
  return trimTrailingSlash(
    preferIpv4Localhost(configuredUrl?.trim() || defaultUrl)
  );
};

export const getQsdmCoreConnectionMode = (
  coreUrl: string,
  gatewayUrl = QSDM_GATEWAY_API_URL,
  canonicalUrl = QSDM_CANONICAL_API_URL
): QsdmCoreConnectionMode => {
  try {
    const host = new URL(coreUrl).hostname.toLowerCase();
    if (host === '127.0.0.1' || host === 'localhost' || host === '::1') {
      return 'local';
    }
  } catch {
    return 'custom';
  }

  return trimTrailingSlash(coreUrl) === trimTrailingSlash(gatewayUrl) ||
    trimTrailingSlash(coreUrl) === trimTrailingSlash(canonicalUrl)
    ? 'gateway'
    : 'custom';
};

export const QSDM_CORE_API_URL = resolveQsdmCoreApiUrl();
export const QSDM_CORE_CONNECTION_MODE =
  getQsdmCoreConnectionMode(QSDM_CORE_API_URL);

let qsdmRuntimeCoreApiUrl: string | undefined;

export const setQsdmRuntimeCoreApiUrl = (apiUrl?: string) => {
  qsdmRuntimeCoreApiUrl = apiUrl
    ? trimTrailingSlash(preferIpv4Localhost(apiUrl.trim()))
    : undefined;
};

export const getQsdmRuntimeCoreApiUrl = () =>
  qsdmRuntimeCoreApiUrl || QSDM_CORE_API_URL;

export const QSDM_HIVE_API_URL = trimTrailingSlash(
  readEnv(
    'QSDM_HIVE_API_URL',
    process.env.NODE_ENV === 'test' ? QSDM_CORE_API_URL : QSDM_GATEWAY_API_URL
  )
);

export const QSDM_DASHBOARD_URL = trimTrailingSlash(
  readEnv(
    'QSDM_DASHBOARD_URL',
    QSDM_CORE_CONNECTION_MODE === 'local'
      ? 'http://localhost:8081'
      : 'https://qsdm.tech/chain.html'
  )
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

// Core ledger amounts use 8 decimal places (dust). Hive's inherited task UI
// stores display denominations at 9 decimals. Keep those units explicit so a
// protocol bond is never accidentally scaled as a legacy task amount.
export const QSDM_CORE_CELL_DECIMALS = 8;
export const QSDM_HIVE_DISPLAY_DECIMALS = 9;
export const QSDM_CELL_DECIMALS = QSDM_HIVE_DISPLAY_DECIMALS;

export const buildQsdmApiUrl = (path: string) =>
  `${QSDM_HIVE_API_URL}/${trimLeadingSlash(path)}`;

export const buildQsdmCanonicalApiUrl = (path: string) =>
  `${QSDM_CANONICAL_API_URL}/${trimLeadingSlash(path)}`;

export const buildQsdmGatewayApiUrl = (path: string) =>
  `${QSDM_GATEWAY_API_URL}/${trimLeadingSlash(path)}`;

export const buildQsdmCoreApiUrl = (path: string) =>
  `${getQsdmRuntimeCoreApiUrl()}/${trimLeadingSlash(path)}`;

interface QsdmTaskActionEndpointOptions {
  runtimeApiUrl?: string;
  taskRpcApiUrl?: string;
  canonicalApiUrl?: string;
}

export const resolveQsdmTaskActionCoreApiUrl = ({
  runtimeApiUrl = getQsdmRuntimeCoreApiUrl(),
  canonicalApiUrl = QSDM_CANONICAL_API_URL,
}: Pick<
  QsdmTaskActionEndpointOptions,
  'runtimeApiUrl' | 'canonicalApiUrl'
> = {}) => {
  const connectionMode = getQsdmCoreConnectionMode(
    runtimeApiUrl,
    undefined,
    canonicalApiUrl
  );
  return trimTrailingSlash(
    connectionMode === 'custom' ? runtimeApiUrl : canonicalApiUrl
  );
};

// Signed task actions must be confirmed by the same Core that accepts them.
// Local validators and the home gateway remain useful fallbacks, but can lag
// behind the main Core while catching up.
export const buildQsdmTaskActionReadUrls = (
  path: string,
  {
    runtimeApiUrl = getQsdmRuntimeCoreApiUrl(),
    taskRpcApiUrl = QSDM_HIVE_API_URL,
    canonicalApiUrl = QSDM_CANONICAL_API_URL,
  }: QsdmTaskActionEndpointOptions = {}
) => {
  const normalizedPath = trimLeadingSlash(path);
  const actionCoreApiUrl = resolveQsdmTaskActionCoreApiUrl({
    runtimeApiUrl,
    canonicalApiUrl,
  });
  const urls = [
    actionCoreApiUrl,
    runtimeApiUrl,
    taskRpcApiUrl,
    canonicalApiUrl,
  ].map((apiUrl) => `${trimTrailingSlash(apiUrl)}/${normalizedPath}`);

  return Array.from(new Set(urls));
};

export const buildQsdmTaskReadUrls = (path: string) => {
  const coreUrl = buildQsdmCoreApiUrl(path);
  const taskRpcUrl = buildQsdmApiUrl(path);
  const canonicalUrl = buildQsdmCanonicalApiUrl(path);
  const coreMode = getQsdmCoreConnectionMode(getQsdmRuntimeCoreApiUrl());
  const urls =
    coreMode === 'local' || coreMode === 'custom'
      ? [coreUrl, taskRpcUrl, canonicalUrl]
      : [taskRpcUrl, coreUrl, canonicalUrl];

  return Array.from(new Set(urls));
};

export const buildConfiguredQsdmCoreApiUrl = (path: string) =>
  `${QSDM_CORE_API_URL}/${trimLeadingSlash(path)}`;

export const QSDM_CORE_HEALTH_URL = buildQsdmCoreApiUrl('/health');
export const QSDM_CORE_STATUS_URL = buildQsdmCoreApiUrl('/status');
export const QSDM_TASK_RPC_HEALTH_URL = buildQsdmApiUrl('/health');
export const QSDM_TASK_RPC_STATUS_URL = buildQsdmApiUrl('/status');

export const QSDM_BRIDGE_CONFIG = {
  apiUrl: QSDM_HIVE_API_URL,
  coreApiUrl: QSDM_CORE_API_URL,
  coreConnectionMode: QSDM_CORE_CONNECTION_MODE,
  gatewayApiUrl: QSDM_GATEWAY_API_URL,
  canonicalApiUrl: QSDM_CANONICAL_API_URL,
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
