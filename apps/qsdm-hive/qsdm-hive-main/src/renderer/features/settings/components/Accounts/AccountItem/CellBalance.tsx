import React from 'react';

import NativeTokenLogo from 'assets/svgs/qsdm-hive-logo.svg';
import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';

type PropsType = {
  accountBalanceInCELL?: number | string;
  usdBalance?: number;
};

export function CellBalance({ accountBalanceInCELL, usdBalance }: PropsType) {
  return (
    <div>
      <div className="flex flex-row items-center gap-2">
        <div className="text-2xl">
          {accountBalanceInCELL} {NATIVE_TOKEN_SYMBOL}
        </div>
        <NativeTokenLogo className="w-10 h-10" />
      </div>
      <div className="text-xs text-finnieGray-secondary">
        {typeof usdBalance === 'number' && usdBalance > 0
          ? `$${usdBalance} USD`
          : `${NATIVE_TOKEN_SYMBOL} price unavailable`}
      </div>
    </div>
  );
}
