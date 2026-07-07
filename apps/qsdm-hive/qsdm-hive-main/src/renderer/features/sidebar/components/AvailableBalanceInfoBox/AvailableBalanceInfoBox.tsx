import {
  AddLine,
  Button,
  ButtonSize,
  ButtonVariant,
  Icon,
} from 'vendor/qsdm-styleguide';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery } from 'react-query';
import { useNavigate } from 'react-router-dom';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import { LoadingSpinner } from 'renderer/components/ui';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import {
  useMainAccount,
  useMainAccountBalance,
} from 'renderer/features/settings/hooks';
import {
  useKplTokens,
  useTokenTransferModal,
} from 'renderer/features/tokens/hooks';
import { TokenItemType } from 'renderer/features/tokens/types';
import {
  getActiveAccountName,
  getQsdmCellAccount,
  getMainAccountPublicKey,
  QueryKeys,
} from 'renderer/services';
import { Theme } from 'renderer/types/common';
import { AppRoute } from 'renderer/types/routes';

import { CountQsdmHive } from '../CountQsdmHive';
import { InfoBox } from '../InfoBox';

const sleep = (timeout: number): Promise<void> => {
  return new Promise((resolve) => {
    setTimeout(() => {
      resolve();
    }, timeout);
  });
};

export function AvailableBalanceInfoBox() {
  const isQsdmNative = QSDM_BRIDGE_CONFIG.runtimeMode === 'qsdm-native';
  const { accountBalance: mainAccountBalance = 0, loadingAccountBalance } =
    useMainAccountBalance();
  const { data: mainAccountPublicKey = '' } = useMainAccount();
  const { data: qsdmCellAccount } = useQuery(
    [QueryKeys.QsdmCellAccount, 'sidebar-balance'],
    () => getQsdmCellAccount(),
    {
      enabled: isQsdmNative,
      refetchInterval: 15000,
    }
  );
  const qsdmWalletAddress = qsdmCellAccount?.address || mainAccountPublicKey;

  const { kplTokenItems, isLoadingTokensData } = useKplTokens({
    publicKey: mainAccountPublicKey,
    enabled: !isQsdmNative && !!mainAccountPublicKey,
  });

  // Local variables
  let publicKeyToUse = isQsdmNative ? qsdmWalletAddress : 'INIT';
  let accountNameToUse = isQsdmNative ? '__qsdm_signer__' : 'INIT';

  const { data: mainAccount } = useQuery(
    [QueryKeys.MainAccount],
    getMainAccountPublicKey
  );

  const { data: queriedAccountName } = useQuery(
    [QueryKeys.MainAccountName],
    getActiveAccountName
  );

  if (!isQsdmNative && mainAccount && mainAccount !== 'INIT') {
    publicKeyToUse = mainAccount;
  }

  if (!isQsdmNative && queriedAccountName && queriedAccountName !== 'INIT') {
    accountNameToUse = queriedAccountName;
  }

  const { showModal: showTransferModal } = useTokenTransferModal({
    accountName: accountNameToUse,
    walletAddress: publicKeyToUse,
    accountType: 'SYSTEM',
    kplTokenItems,
  });

  const navigate = useNavigate();

  const navigateToWallets = () =>
    navigate(AppRoute.SettingsWallet, { state: { walletExpanded: true } });

  return (
    <InfoBox className="justify-center py-2 h-36 md2h:h-40 xl:p-4 overflow-hidden lgh:h-[186px]">
      <div className="flex items-center justify-between w-full">
        <div className="flex flex-col items-start w-full gap-1">
          <div className="flex w-full items-center justify-between">
            <span className="text-sm text-green-2 relative">
              Available Balance
            </span>
            <Popover
              asChild
              tooltipContent="View the balances of all the tokens in your wallet."
            >
              <button
                onClick={navigateToWallets}
                className="text-xs hover:text-green-2 leading-tight transition-all duration-300 ease-in-out flex items-center"
              >
                <span className="hidden xl:block">View all</span>
                <Icon
                  source={AddLine}
                  size={15}
                  data-testid="add-line-icon"
                  aria-label="AddLine icon"
                  className="xl:hidden visible"
                />
              </button>
            </Popover>
          </div>

          <BalancesCarousel
            cellBalance={mainAccountBalance}
            isLoadingCellBalance={loadingAccountBalance}
            kplTokenItems={kplTokenItems}
            isLoadingKplItems={isLoadingTokensData}
          />
        </div>
      </div>
      <div className="ml-auto mr-auto">
        <Popover
          asChild
          theme={Theme.Light}
          tooltipContent="Click here to transfer tokens to another wallet."
        >
          <span className="inline-flex">
            <Button
              variant={ButtonVariant.Secondary}
              size={ButtonSize.SM}
              label="Transfer Funds"
              buttonClassesOverrides="mt-3 !transition-all !duration-300 ease-in-out w-[165px] md2:w-[300px] lgh:mt-7 hover:border-green-2 focus:border-green-2 active:border-green-2 focus:text-green-2 hover:text-green-2"
              labelClassesOverrides="mx-auto"
              data-testid="transfer-CELL-button"
              onClick={() => showTransferModal()}
            />
          </span>
        </Popover>
      </div>
    </InfoBox>
  );
}

const HALF_ITEM_WIDTH = 80;

export function BalancesCarousel({
  cellBalance,
  isLoadingCellBalance,
  kplTokenItems,
  isLoadingKplItems,
}: {
  cellBalance: number;
  isLoadingCellBalance: boolean;
  kplTokenItems: TokenItemType[];
  isLoadingKplItems?: boolean;
}) {
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  const scroll = (direction: 'left' | 'right') => {
    if (scrollContainerRef.current) {
      const scrollAmount =
        direction === 'left' ? -HALF_ITEM_WIDTH : HALF_ITEM_WIDTH;
      scrollContainerRef.current.scrollBy({
        left: scrollAmount,
        behavior: 'smooth',
      });
    }
  };

  const [canScrollLeft, setCanScrollLeft] = useState(false);
  const [canScrollRight, setCanScrollRight] = useState(false);

  const checkScrollPosition = useCallback(() => {
    if (scrollContainerRef.current) {
      const { scrollLeft, scrollWidth, clientWidth } =
        scrollContainerRef.current;
      setCanScrollLeft(scrollLeft > 0);
      setCanScrollRight(scrollLeft + clientWidth + 2 < scrollWidth);
    }
  }, []);

  useEffect(() => {
    checkScrollPosition();
    window.addEventListener('resize', () =>
      sleep(100).then(() => checkScrollPosition())
    );
    return () => window.removeEventListener('resize', checkScrollPosition);
  }, [
    checkScrollPosition,
    isLoadingKplItems,
    kplTokenItems.length,
    cellBalance,
    isLoadingCellBalance,
  ]);

  const totalItems = cellBalance
    ? kplTokenItems.length + 1
    : kplTokenItems.length;
  const isSingleItem = totalItems === 1;
  const shouldForceDisplayingCELL = totalItems === 0;
  const boxShouldBeFullSize =
    (isSingleItem || shouldForceDisplayingCELL) &&
    !isLoadingKplItems &&
    !isLoadingCellBalance;

  const loaderClasses =
    'w-[78px] xl:min-w-[100px] transition-all duration-300 ease-in-out';

  return (
    <div className="flex w-full relative">
      <div className="flex items-center w-full">
        <div
          className="flex overflow-x-auto inner-scrollbar whitespace-nowrap scroll-smooth space-x-2"
          ref={scrollContainerRef}
          onScroll={checkScrollPosition}
        >
          <div className="flex items-center gap-1">
            {isLoadingCellBalance ? (
              <LoadingSpinner />
            ) : cellBalance || shouldForceDisplayingCELL ? (
              <BalanceBox
                balance={cellBalance}
                ticker={NATIVE_TOKEN_SYMBOL}
                isSingleItem={boxShouldBeFullSize}
              />
            ) : null}

            {isLoadingKplItems ? (
              <div className={loaderClasses}>
                <LoadingSpinner className="w-fit mx-auto" />
              </div>
            ) : (
              kplTokenItems.map((tokenItem, index) => (
                <BalanceBox
                  key={index}
                  balance={tokenItem.balance}
                  ticker={tokenItem.symbol}
                  tokenLogoUri={tokenItem.logoURI}
                  isSingleItem={boxShouldBeFullSize}
                  decimals={tokenItem.decimals}
                />
              ))
            )}
          </div>
        </div>
      </div>
      {canScrollLeft && (
        <ArrowScrollButton direction="left" onClick={() => scroll('left')} />
      )}
      {canScrollRight && (
        <ArrowScrollButton direction="right" onClick={() => scroll('right')} />
      )}
    </div>
  );
}

export function BalanceBox({
  balance,
  ticker,
  tokenLogoUri,
  isSingleItem,
  decimals,
}: {
  balance: number;
  ticker: string;
  tokenLogoUri?: string;
  isSingleItem: boolean;
  decimals?: number;
}) {
  const boxClasses = `flex flex-col items-start justify-center bg-purple-5 p-2 rounded-md transition-all duration-300 ease-in-out, ${
    isSingleItem
      ? 'min-w-[160px] xl:min-w-[198px] md2:min-w-[316px] xl1:min-w-[416px] xl2:min-w-[516px]'
      : 'min-w-[90px] xl:min-w-[96px]'
  }`;

  return (
    <div className={boxClasses}>
      <span className="text-sm transition-all duration-300 ease-in-out">
        <CountQsdmHive
          value={balance}
          ticker={ticker}
          logoURI={tokenLogoUri}
          decimals={decimals}
        />
      </span>
    </div>
  );
}

function ArrowScrollButton({
  direction,
  onClick,
}: {
  direction: 'left' | 'right';
  onClick: () => void;
}) {
  const positionClasses = direction === 'left' ? 'left-0' : 'right-0';
  const buttonClasses = `focus:outline-none hover:bg-gray-600 p-1  bg-finnieBlue-light-secondary h-full absolute ${positionClasses}`;
  const arrowCharacter = direction === 'left' ? '‹' : '›';

  return (
    <button onClick={onClick} className={buttonClasses}>
      {arrowCharacter}
    </button>
  );
}
