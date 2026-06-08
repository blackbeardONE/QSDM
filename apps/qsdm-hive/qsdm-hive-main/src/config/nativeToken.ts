export const NATIVE_TOKEN_SYMBOL = 'CELL';
export const NATIVE_TOKEN_NAME = 'CELL';
export const NATIVE_TOKEN_LOGO_URI = 'assets/svgs/qsdm-hive-logo.svg';

export const NATIVE_TOKEN_PROTOCOL_SYMBOL = NATIVE_TOKEN_SYMBOL;
export const LEGACY_NATIVE_TOKEN_PROTOCOL_SYMBOL = 'CELL';
export const LEGACY_NATIVE_TOKEN_NAME = 'CELL';

export const isNativeTokenSymbol = (symbol?: string) =>
  symbol === NATIVE_TOKEN_PROTOCOL_SYMBOL ||
  symbol === NATIVE_TOKEN_SYMBOL ||
  symbol === LEGACY_NATIVE_TOKEN_PROTOCOL_SYMBOL;

export const isNativeToken = (token?: { name?: string; symbol?: string }) =>
  isNativeTokenSymbol(token?.symbol) ||
  token?.name === LEGACY_NATIVE_TOKEN_NAME ||
  token?.name === NATIVE_TOKEN_NAME;

export const displayNativeTokenSymbol = (symbol?: string) =>
  isNativeTokenSymbol(symbol)
    ? NATIVE_TOKEN_SYMBOL
    : symbol || NATIVE_TOKEN_SYMBOL;
