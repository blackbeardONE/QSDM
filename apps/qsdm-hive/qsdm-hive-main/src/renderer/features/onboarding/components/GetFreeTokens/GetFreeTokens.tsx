import {
  CheckSuccessLine,
  CloseLine,
  CopyLine,
  CurrencyMoneyLine,
  TipGiveLine,
} from 'vendor/qsdm-styleguide';
import React, { useState } from 'react';
import { useQuery, useQueryClient } from 'react-query';
import { useNavigate } from 'react-router-dom';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { Button } from 'renderer/components/ui';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { useClipboard } from 'renderer/features/common/hooks/useClipboard';
import { useMainAccount } from 'renderer/features/settings/hooks/useMainAccount';
import { QueryKeys } from 'renderer/services';
import { trackEvent } from 'renderer/services/analytics';
import { AppRoute } from 'renderer/types/routes';

export function GetFreeTokens({
  closeModal,
  accountPublicKey,
}: {
  closeModal?: () => void;
  accountPublicKey?: string;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [claimMessage, setClaimMessage] = useState('');
  const [claimError, setClaimError] = useState('');
  const [isClaiming, setIsClaiming] = useState(false);

  const { data: mainAccountPubKey = '' } = useMainAccount();
  const accountPublicKeyToUse = accountPublicKey || mainAccountPubKey;
  const { data: qsdmCellAccount } = useQuery(
    [QueryKeys.QsdmCellAccount, 'faucet'],
    () => window.main.getQsdmCellAccount(),
    { retry: false }
  );
  const qsdmAddressToUse = qsdmCellAccount?.address || accountPublicKeyToUse;

  const claimCell = async () => {
    setIsClaiming(true);
    setClaimError('');
    setClaimMessage('');
    try {
      const result = await window.main.claimQsdmCellFaucet({
        address: qsdmAddressToUse,
      });
      const granted = Number(result.amount_granted || 0);
      setClaimMessage(
        result.status === 'already_funded'
          ? `This wallet already has at least ${result.target_balance} ${NATIVE_TOKEN_SYMBOL}.`
          : `Claimed ${granted.toLocaleString()} ${NATIVE_TOKEN_SYMBOL}.`
      );
      await queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
      await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
    } catch (error) {
      setClaimError(error instanceof Error ? error.message : String(error));
    } finally {
      setIsClaiming(false);
    }
    trackEvent('claim_qsdm_cell_faucet', {
      walletAddress: accountPublicKeyToUse,
      qsdmAddress: qsdmAddressToUse,
      behavior: 'claim_local_cell',
    });
  };

  const { copyToClipboard, copied: hasCopiedMainAccountPubKey } =
    useClipboard();

  const copyMainAccountPubKey = () => {
    copyToClipboard(qsdmAddressToUse);
  };
  const copyTooltipContent = hasCopiedMainAccountPubKey
    ? 'Copied!'
    : 'Copy your address';
  const CopyIcon = hasCopiedMainAccountPubKey ? CheckSuccessLine : CopyLine;
  const clickClose =
    closeModal || (() => navigate(AppRoute.OnboardingSeeBalance));

  return (
    <div className="relative flex items-center justify-center text-white text-center bg-purple-5 rounded py-10 2xl:scale-125 transition-all duration-300 ease-in-out">
      <button
        className="absolute -right-2.5 -top-3 text-white"
        onClick={clickClose}
      >
        <CloseLine className="w-6 h-6" />
      </button>
      <div className="flex flex-col gap-6 px-16">
        <TipGiveLine className="w-8 h-8 mx-auto rotate-180 -mb-4" />
        <div className="font-semibold">Get free tokens</div>
        <div className="font-light w-80 2xl:w-96 mx-auto">
          <p>
            Claim starter {NATIVE_TOKEN_SYMBOL} from your local QSDM validator.
            Hive talks directly to QSDM Core, so there are no external faucet
            redirects.
          </p>
        </div>

        <div>
          <Popover tooltipContent={copyTooltipContent}>
            <button
              onClick={copyMainAccountPubKey}
              className="my-2 border-2 border-finnieTeal text-finnieTeal rounded-full p-2 flex items-center gap-4 hover:text-white transition-all duration-300 ease-in-out hover:border-white cursor-pointer"
            >
              <CopyIcon />
              <span className="text-xs select-text underline font-light">
                {qsdmAddressToUse}
              </span>
            </button>
          </Popover>
        </div>
        <div className="flex flex-col items-center gap-4">
          <Button
            className="font-semibold bg-finnieGray-light text-finnieBlue-light w-[220px] h-[48px] hover:brightness-110"
            label={isClaiming ? 'Claiming...' : `Claim ${NATIVE_TOKEN_SYMBOL}`}
            icon={<CurrencyMoneyLine className="w-5 h-5" />}
            onClick={claimCell}
          />
          {claimMessage && (
            <div className="text-xs text-finnieTeal-100">{claimMessage}</div>
          )}
          {claimError && (
            <div className="text-xs text-finnieRed">{claimError}</div>
          )}
        </div>
      </div>
    </div>
  );
}
