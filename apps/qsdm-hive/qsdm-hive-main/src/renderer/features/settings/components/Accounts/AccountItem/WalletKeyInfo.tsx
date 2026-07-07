/* eslint-disable jsx-a11y/no-static-element-interactions */
/* eslint-disable jsx-a11y/click-events-have-key-events */
import { KeyUnlockLine, ShareArrowLine } from 'vendor/qsdm-styleguide';
import { InfoCircledIcon } from '@radix-ui/react-icons';
import React from 'react';
import { twMerge } from 'tailwind-merge';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { LoadingSpinner } from 'renderer/components/ui';
import { Button } from 'renderer/components/ui/Button';
import { CopyButton } from 'renderer/components/ui/CopyButton';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { useClipboard } from 'renderer/features/common/hooks';
import { useRecoverStakingAccount } from 'renderer/features/common/hooks/useRecoverStakingAccount';
import { AccountType } from 'renderer/features/settings/types';
import { useTokenTransferModal } from 'renderer/features/tokens/hooks';
import { TokenItemType } from 'renderer/features/tokens/types';
import { Theme } from 'renderer/types/common';

import { CellBalance } from './CellBalance';

type PropsType = {
  label: string;
  accountName: string;
  tokenValue: number | string;
  tokenUsdValue?: number;
  walletAddress: string;
  isSelected: boolean;
  walletType: AccountType;
  kplTokenItems: TokenItemType[];
  onShowSeedPhraseModal: () => void;
  onClick: () => void;
  compact?: boolean;
  stakingKeyIsMessedUp?: boolean;
  isKpLBalanceFetching?: boolean;
  isDefault?: boolean;
  isBalancesHidden?: boolean;
  nativeSendDisabled?: boolean;
  hideProfilePhraseAction?: boolean;
};

export function WalletKeyInfo({
  label,
  tokenValue,
  tokenUsdValue,
  walletAddress,
  walletType,
  onShowSeedPhraseModal,
  onClick,
  isSelected = false,
  compact = false,
  stakingKeyIsMessedUp,
  kplTokenItems,
  accountName,
  isKpLBalanceFetching,
  isDefault,
  isBalancesHidden,
  nativeSendDisabled,
  hideProfilePhraseAction,
}: PropsType) {
  const { copyToClipboard, copied } = useClipboard();

  const handleCopyMainPublicKey = () => {
    copyToClipboard(walletAddress);
  };

  const { showModal: showTransferFundsModal } = useTokenTransferModal({
    accountName,
    accountType: walletType,
    kplTokenItems,
    walletAddress,
  });
  const shouldWarnAboutStakingKey =
    isDefault &&
    (walletType === 'STAKING' || walletType === 'KPL_STAKING') &&
    stakingKeyIsMessedUp;

  const { showModal: showRecoverStakingAccountModal } =
    useRecoverStakingAccount({
      isKPLStakingAccount: walletType === 'KPL_STAKING',
    });

  const tooltipContent =
    walletType === 'KPL_STAKING'
      ? "Your KPL staking key ran out of funds for rent and got deleted by the network. Now it's not owned by the KPL Program as it should, and it won't be able to stake on KPL tasks. Recover it to continue running KPL tasks!"
      : "Your staking key ran out of funds for rent and got deleted by the network. Now it's not owned by the Tasks Program as it should, and it won't be able to stake on tasks. Recover it to continue running tasks!";

  const infoTooltipContent =
    walletType === 'STAKING'
      ? `This is your ${NATIVE_TOKEN_SYMBOL} staking key. It has unique on-chain permissions, allowing it to stake ${NATIVE_TOKEN_SYMBOL} on tasks and earn rewards.`
      : walletType === 'KPL_STAKING'
      ? 'This is your KPL staking key. It has unique on-chain permissions, allowing it to stake KPL tokens on tasks and earn rewards.'
      : '';

  const infoItem = (
    <Popover tooltipContent={infoTooltipContent}>
      <button>
        <InfoCircledIcon className="w-4 h-4" />
      </button>
    </Popover>
  );

  const shouldDisplayInfoItem =
    walletType === 'STAKING' || walletType === 'KPL_STAKING';

  const displayValue = isBalancesHidden ? '••••••' : tokenValue;
  const sendDisabled = nativeSendDisabled || walletType === 'KPL_STAKING';
  const sendTooltip = nativeSendDisabled
    ? 'Use the QSDM Signer Wallet card above to send native CELL'
    : walletType === 'KPL_STAKING'
    ? 'Transferring from the KPL staking key will be available soon'
    : 'Send funds';

  return (
    <div
      className={twMerge(
        'flex flex-col gap-1.5 rounded-xl p-4 cursor-pointer transition-all duration-300 min-w-[282px] relative',
        isSelected ? 'bg-purple-5' : 'bg-transparent hover:bg-purple-5/[.30]',
        compact && 'bg-transparent py-0 px-4',
        stakingKeyIsMessedUp && 'overflow-hidden'
      )}
      onClick={onClick}
    >
      {stakingKeyIsMessedUp && (
        <div className="absolute inset-0 bg-finnieRed/[.25] animate-pulse pointer-events-none" />
      )}
      <div
        className={twMerge(
          walletType === 'STAKING'
            ? 'text-finnieOrange'
            : 'text-finnieEmerald-light',
          'text-xs',
          'flex items-center gap-1'
        )}
      >
        {label} {shouldDisplayInfoItem && infoItem}
      </div>
      <div className="flex justify-between relative z-10">
        <CellBalance
          accountBalanceInCELL={displayValue}
          usdBalance={tokenUsdValue}
        />

        {shouldWarnAboutStakingKey && (
          <div>
            <Popover tooltipContent={tooltipContent} theme={Theme.Dark}>
              <Button
                label="Recover"
                className="w-20 hover:bg-white hover:text-finnieBlue-light-secondary text-xs"
                onClick={showRecoverStakingAccountModal}
              />
            </Popover>
          </div>
        )}
      </div>
      <div
        className={twMerge(
          'flex items-center justify-between gap-2 px-2 py-0.5 h-6 w-fit',
          (!isSelected || compact) && 'bg-purple-5 rounded-lg',
          stakingKeyIsMessedUp && 'bg-finnieRed/[.25]'
        )}
      >
        <div className="text-xs text-white w-[146px] truncate mr-auto">
          {walletAddress.length > 16
            ? `${walletAddress.slice(0, 8)}...${walletAddress.slice(-8)}`
            : walletAddress}
        </div>

        {isSelected &&
          (isKpLBalanceFetching ? (
            <LoadingSpinner />
          ) : (
            <>
              <CopyButton onCopy={handleCopyMainPublicKey} isCopied={copied} />
              {!hideProfilePhraseAction && (
                <Popover
                  tooltipContent="Hive profile phrase only. Use Settings > Wallet to back up your QSDM CELL wallet."
                  theme={Theme.Dark}
                >
                  <Button
                    icon={
                      <KeyUnlockLine className="w-3.5 h-3.5 text-white" />
                    }
                    className="w-6 h-6 bg-transparent rounded-full "
                    onClick={onShowSeedPhraseModal}
                  />
                </Popover>
              )}
              <Popover
                theme={Theme.Dark}
                tooltipContent={sendTooltip}
              >
                <Button
                  icon={<ShareArrowLine className="w-3.5 h-3.5 text-white" />}
                  className="w-6 h-6 bg-transparent rounded-full "
                  onClick={() => showTransferFundsModal()}
                  disabled={sendDisabled}
                />
              </Popover>
            </>
          ))}
      </div>
    </div>
  );
}
