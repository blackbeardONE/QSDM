import { CheckSuccessLine, Icon, KeyUnlockLine } from 'vendor/qsdm-styleguide';
import React from 'react';
import { useQuery } from 'react-query';

import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { useClipboard } from 'renderer/features/common/hooks';
import { useMainAccount } from 'renderer/features/settings/hooks';
import { getQsdmCellAccount, QueryKeys } from 'renderer/services';

const ICON_SIZE = 20;

const shortenAddress = (address?: string) => {
  if (!address) return '';
  return address.length > 18
    ? `${address.substring(0, 9)}...${address.substring(address.length - 7)}`
    : address;
};

export function MainWalletView() {
  const isQsdmNative = QSDM_BRIDGE_CONFIG.runtimeMode === 'qsdm-native';
  const { data: mainAccountPublicKey } = useMainAccount();
  const { data: qsdmCellAccount } = useQuery(
    [QueryKeys.QsdmCellAccount, 'sidebar-wallet'],
    () => getQsdmCellAccount(),
    {
      enabled: isQsdmNative,
      refetchInterval: 15000,
    }
  );
  const walletAddress =
    (isQsdmNative ? qsdmCellAccount?.address : mainAccountPublicKey) ||
    mainAccountPublicKey ||
    '';
  const shortenedWalletAddress = shortenAddress(walletAddress);
  const handleCopy = () => {
    copyToClipboard(walletAddress);
  };
  const handleKeyDown =
    (callback: () => void) => (event: React.KeyboardEvent) => {
      if (event.key === 'Enter') {
        callback();
      }
    };
  const { copyToClipboard, copied: isCopied } = useClipboard();

  const tooltipContent = isCopied
    ? 'Copied!'
    : isQsdmNative
    ? 'Copy your QSDM signer wallet address.'
    : "Copy your system wallet's address.";

  return (
    <Popover tooltipContent={tooltipContent}>
      <div
        className="flex flex-col text-white w-[186px] xl:w-[230px] md2:w-[350px] xl1:w-[450px] xl2:w-[550px] rounded border-2 border-finnieBlue-light-secondary transition-all duration-300 ease-in-out"
        role="button"
        onClick={handleCopy}
        onKeyDown={handleKeyDown(handleCopy)}
        tabIndex={0}
      >
        <div
          className={`transition-all duration-300 ease-in-out flex h-[40px] lgh2:h-14 ${
            isCopied ? 'bg-purple-5/[0.5]' : 'bg-transparent'
          }`}
        >
          <div
            className={`transition-all duration-300 ease-in-out ${
              isCopied ? 'bg-purple-5' : 'bg-finnieBlue-light-secondary'
            }`}
            style={{
              width: '17%',
              height: '100%',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <Icon
              source={isCopied ? CheckSuccessLine : KeyUnlockLine}
              size={ICON_SIZE}
              data-testid="key-unlock-icon"
              aria-label="key-unlock icon"
            />
          </div>
          <div className="flex items-center justify-center m-auto">
            <p className="px-1 overflow-hidden text-xs xl:text-sm w-fit whitespace-nowrap text-ellipsis">
              {shortenedWalletAddress}
            </p>
          </div>
        </div>
      </div>
    </Popover>
  );
}
