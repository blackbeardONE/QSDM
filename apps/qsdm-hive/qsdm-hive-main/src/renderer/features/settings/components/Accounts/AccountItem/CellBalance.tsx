import React from 'react';

import NativeTokenLogo from 'assets/svgs/cell-coin-rich.svg';
import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';

type PropsType = {
  accountBalanceInCELL?: number | string;
  usdBalance?: number;
};

export function CellBalance({ accountBalanceInCELL, usdBalance }: PropsType) {
  return (
    <div>
      <div className="flex flex-row items-center gap-2">
        <NativeTokenLogo className="w-9 h-9 shrink-0" />
        <div className="text-2xl">
          <span className="font-mono font-medium">{accountBalanceInCELL}</span>{' '}
          <span className="text-lg text-qsdm-muted">{NATIVE_TOKEN_SYMBOL}</span>
        </div>
      </div>
      <div className="text-xs text-finnieGray-secondary">
        {typeof usdBalance === 'number' && usdBalance > 0
          ? `$${usdBalance} USD`
          : `${NATIVE_TOKEN_SYMBOL} price unavailable`}
      </div>
    </div>
  );
}
