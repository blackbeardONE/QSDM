import React from 'react';

import NativeTokenLogo from 'assets/svgs/qsdm-hive-logo.svg';
import { isNativeToken } from 'config/nativeToken';
import TokenPlaceholder from 'assets/svgs/token_placeholder.png';

import { TokenItemType } from '../../types';

type TokenLogoDisplayProps = {
  token: TokenItemType;
};

export function TokenLogoDisplay({ token }: TokenLogoDisplayProps) {
  if (isNativeToken(token)) {
    return <NativeTokenLogo className="w-8 h-8 rounded-full" />;
  }

  return (
    <img
      src={token.logoURI}
      onError={(e) => {
        e.currentTarget.src = TokenPlaceholder;
      }}
      alt={token.name}
      className="w-8 h-8 rounded-full"
    />
  );
}
