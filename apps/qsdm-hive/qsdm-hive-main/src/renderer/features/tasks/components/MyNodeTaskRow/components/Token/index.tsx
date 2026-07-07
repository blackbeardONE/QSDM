import React from 'react';

import NativeTokenLogo from 'assets/svgs/qsdm-hive-logo.svg';
import TokenPlaceholder from 'assets/svgs/token_placeholder.png';
import { buildExplorerAddressUrl } from 'config/explorer';
import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { KPLToken } from 'models';
import { LoadingSpinner } from 'renderer/components';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { openBrowserWindow } from 'renderer/services';
import { Theme } from 'renderer/types/common';

const QSDM_TOKEN_BASE_URL = 'https://qsdm.tech/tokens/';

export function Token({
  token,
  isLoading,
}: {
  token?: KPLToken;
  isLoading: boolean;
}) {
  return (
    <Popover
      tooltipContent={
        token
          ? `Learn more about ${token.symbol}`
          : `This task will accept stake and issue rewards in ${NATIVE_TOKEN_SYMBOL}`
      }
      theme={Theme.Dark}
    >
      {isLoading ? (
        <LoadingSpinner />
      ) : token ? (
        <button
          onClick={() => {
            if (token.symbol === 'Unknown') {
              openBrowserWindow(buildExplorerAddressUrl(token.address));
            } else {
              openBrowserWindow(`${QSDM_TOKEN_BASE_URL}${token.symbol}`);
            }
          }}
          className="w-8 !h-8 flex justify-center items-center cursor-pointer"
        >
          <img
            src={token?.logoURI}
            onError={(e) => {
              e.currentTarget.src = TokenPlaceholder;
            }}
            alt={`${token?.symbol || NATIVE_TOKEN_SYMBOL} logo`}
            className="w-8 h-8 rounded-full"
          />
        </button>
      ) : (
        <NativeTokenLogo className="w-[38px] h-[38px] rounded-full flex justify-center items-center -ml-1 cursor-auto" />
      )}
    </Popover>
  );
}
