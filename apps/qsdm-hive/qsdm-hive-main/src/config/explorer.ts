export const EXPLORER_BASE_URL = 'https://qsdm.tech/explorer.html';
export const EXPLORER_DEVNET_PARAM = '?cluster=qsdm-dev';
export const EXPLORER_TESTNET_PARAM = '?cluster=qsdm-local';
export const EXPLORER_MAINNET_PARAM = '?cluster=qsdm-gateway';

export const buildExplorerAddressUrl = (address: string) =>
  `${EXPLORER_BASE_URL}?address=${encodeURIComponent(address)}`;
