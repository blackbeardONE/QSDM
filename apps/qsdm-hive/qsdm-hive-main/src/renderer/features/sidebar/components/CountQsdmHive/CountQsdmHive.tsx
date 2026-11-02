import React from 'react';
import CountUp from 'react-countup';

import NativeTokenLogo from 'assets/svgs/cell-coin-small.svg';
import {
  displayNativeTokenSymbol,
  isNativeTokenSymbol,
  NATIVE_TOKEN_SYMBOL,
} from 'config/nativeToken';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { usePrevious } from 'renderer/features/common';
import { Theme } from 'renderer/types/common';
import { getFullCellFromBaseUnits, getCellFromBaseUnits } from 'utils';

import { countDecimals } from '../../utils';

export const TOO_SMALL_AMOUNT_PLACEHOLDER = '< 0.001';

type PropsType = {
  value: number;
  ticker?: string;
  logoURI?: string;
  decimals?: number;
};

export function CountQsdmHive({
  value,
  ticker = NATIVE_TOKEN_SYMBOL,
  logoURI,
  decimals = 9,
}: PropsType) {
  const displayTicker = displayNativeTokenSymbol(ticker);
  const roundedValue = isNativeTokenSymbol(ticker)
    ? getCellFromBaseUnits(value)
    : value / 10 ** decimals;
  const fullValue = isNativeTokenSymbol(ticker)
    ? getFullCellFromBaseUnits(value)
    : value / 10 ** decimals;
  const previousValue = usePrevious(roundedValue);
  const decimalsAmount = countDecimals(fullValue);
  const isVerySmallAmount = fullValue < 0.001 && fullValue > 0;
  const trailingDecimals = roundedValue < 100000 ? 2 : 0;

  const formatFullValue = (value: number) => {
    return value.toLocaleString('en-US', {
      minimumFractionDigits: 0,
      maximumFractionDigits: Math.max(decimalsAmount, 3),
      useGrouping: true,
    });
  };

  return (
    <Popover tooltipContent={formatFullValue(fullValue)} theme={Theme.Light}>
      <div className="flex flex-col items-start gap-1 cursor-auto">
        <span className="font-mono text-base font-medium leading-tight text-qsdm-text">
          {isVerySmallAmount ? (
            <span>{TOO_SMALL_AMOUNT_PLACEHOLDER}</span>
          ) : (
            <CountUp
              decimals={trailingDecimals}
              start={previousValue}
              end={roundedValue}
              duration={0.5}
              data-testid="count-qsdm"
            />
          )}
        </span>
        <div className="flex gap-1.5 items-center text-xs text-qsdm-muted">
          {!!logoURI && !!ticker && (
            <img
              src={logoURI}
              alt={displayTicker}
              className="w-5 h-5 rounded-full"
            />
          )}
          {isNativeTokenSymbol(ticker) && (
            <NativeTokenLogo className="w-4 h-4 shrink-0" />
          )}
          <p>{displayTicker}</p>
        </div>
      </div>
    </Popover>
  );
}
