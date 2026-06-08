import React from 'react';

import {
  displayNativeTokenSymbol,
  NATIVE_TOKEN_SYMBOL,
} from 'config/nativeToken';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { formatNumber } from 'renderer/features/tasks/utils';
import { Theme } from 'renderer/types/common';

const CURRENCIES = {
  CELL: NATIVE_TOKEN_SYMBOL,
  ETH: 'ETH',
  USDC: 'USDC',
  USDT: 'USDT',
  BTC: 'BTC',
} as const;

type CurrencyType = keyof typeof CURRENCIES;

type PropsType = {
  currency: CurrencyType | string;
  amount?: number;
  precision?: number;
  tooltipContent?: React.ReactNode;
  hideSymbol?: boolean;
};

export function CurrencyDisplay({
  amount,
  currency = NATIVE_TOKEN_SYMBOL,
  precision,
  tooltipContent,
  hideSymbol,
}: PropsType) {
  if (amount === undefined) {
    return null;
  }

  const displayCurrency = displayNativeTokenSymbol(currency);
  const content = `${
    Number.isInteger(amount)
      ? amount.toString()
      : precision
      ? amount.toFixed(precision)
      : formatNumber(amount, false)
  } ${hideSymbol ? '' : displayCurrency}`;

  return tooltipContent ? (
    <Popover theme={Theme.Dark} tooltipContent={tooltipContent}>
      {content}
    </Popover>
  ) : (
    <span>{content}</span>
  );
}
