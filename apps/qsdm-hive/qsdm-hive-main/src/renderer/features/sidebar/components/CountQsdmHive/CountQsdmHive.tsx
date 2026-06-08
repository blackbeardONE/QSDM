import React from 'react';
import CountUp from 'react-countup';

import NativeTokenLogo from 'assets/svgs/qsdm-hive-logo.svg';
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
  const roundedValue =
    isNativeTokenSymbol(ticker)
      ? getCellFromBaseUnits(value)
      : value / 10 ** decimals;
  const fullValue =
    isNativeTokenSymbol(ticker)
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
        <div className="flex gap-1 items-center">
          <p>{displayTicker}</p>
          {!!logoURI && !!ticker && (
            <img
              src={logoURI}
              alt={displayTicker}
              className="w-5 h-5 rounded-full"
            />
          )}
          {isNativeTokenSymbol(ticker) && (
            <NativeTokenLogo className="w-[21px] h-[21px] rounded-full" />
          )}
        </div>
      </div>
    </Popover>
  );
}
