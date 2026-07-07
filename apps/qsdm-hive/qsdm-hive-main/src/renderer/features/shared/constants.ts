export const LOCAL_QSDM_RPC_URL = 'http://127.0.0.1:8080/api/v1';
export const QSDM_GATEWAY_RPC_URL =
  'https://api.qsdm.tech/attest/home-validator/api/v1';

export const TESTNET_RPC_URL = LOCAL_QSDM_RPC_URL;
export const LEGACY_TESTNET_RPC_URL = LOCAL_QSDM_RPC_URL;
export const EMERGENCY_TESTNET_RPC_URL = LOCAL_QSDM_RPC_URL;
export const DEVNET_RPC_URL = LOCAL_QSDM_RPC_URL;
export const MAINNET_RPC_URL = QSDM_GATEWAY_RPC_URL;

export const AVAILABLE_NETWORKS = {
  testnet: {
    name: 'Local QSDM Core',
    url: TESTNET_RPC_URL,
  },
  legacyTestnet: {
    name: 'Local QSDM Fallback',
    url: LEGACY_TESTNET_RPC_URL,
  },

  devnet: {
    name: 'QSDM Dev Core',
    url: DEVNET_RPC_URL,
  },
  mainnet: {
    name: 'QSDM Gateway',
    url: MAINNET_RPC_URL,
  },
} as const;

export type NetworkType =
  (typeof AVAILABLE_NETWORKS)[keyof typeof AVAILABLE_NETWORKS];
export type NetworkUrlType = NetworkType['url'];
