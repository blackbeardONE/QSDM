import {
  CheckSuccessFill,
  Icon,
  WarningCircleLine,
} from 'vendor/qsdm-styleguide';
import React, { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from 'react-query';
import { useNavigate } from 'react-router-dom';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { Button, ErrorMessage, LoadingSpinner } from 'renderer/components/ui';
import {
  exportQsdmSignerWalletBackup,
  getQsdmCoreStatus,
  QueryKeys,
} from 'renderer/services';
import { AppRoute } from 'renderer/types/routes';

const formatAddress = (address?: string) => {
  if (!address) return 'Not configured';
  return address.length > 24
    ? `${address.slice(0, 12)}...${address.slice(-12)}`
    : address;
};

const formatError = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

export function BackupKeyNow() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [backupComplete, setBackupComplete] = useState(false);
  const [backupMessage, setBackupMessage] = useState('');

  const {
    data: coreStatus,
    isLoading: coreStatusLoading,
    error: coreStatusError,
  } = useQuery(
    [QueryKeys.QsdmCoreStatus, 'onboarding-wallet-backup'],
    getQsdmCoreStatus,
    {
      refetchInterval: 10000,
    }
  );

  const signer = coreStatus?.taskSigner;
  const signerReady = !!signer?.ready;

  const {
    mutate: backupWallet,
    isLoading: backingUp,
    error: backupError,
  } = useMutation(exportQsdmSignerWalletBackup, {
    onSuccess: async (result) => {
      if (!result.exported) {
        setBackupMessage('Backup was cancelled. Choose a folder to continue.');
        setBackupComplete(false);
        return;
      }

      setBackupComplete(true);
      setBackupMessage(
        `Backed up QSDM wallet JSON and passphrase for ${formatAddress(
          result.address
        )}.`
      );
      await queryClient.invalidateQueries([QueryKeys.QsdmCoreStatus]);
      await queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
    },
  });

  const continueToFunding = () => {
    navigate(AppRoute.OnboardingCreateNewKey);
  };

  return (
    <div className="flex flex-col items-center gap-5 md2h:gap-7 text-center px-44 text-sm md2h:text-base 2xl:text-base transition-all duration-300 ease-in-out">
      <div className="w-full mb-4 text-2xl md2h:text-3xl 2xl:text-3xl font-semibold">
        Back up your QSDM wallet
      </div>
      <div className="w-full mb-4">
        Your {NATIVE_TOKEN_SYMBOL} wallet is recovered with two things: the
        QSDM keystore JSON and its passphrase. Hive does not use a seed phrase
        for this wallet.
      </div>

      <div className="w-[460px] max-w-full rounded-md bg-finnieBlue-light-tertiary p-5 text-left">
        <div className="text-xs text-finnieGray-secondary">Active signer</div>
        <div className="pt-2 text-sm font-semibold">
          {coreStatusLoading ? <LoadingSpinner /> : formatAddress(signer?.sender)}
        </div>
        <div className="pt-3 text-xs text-finnieGray-secondary break-all">
          Keystore: {signer?.keystorePath || 'Not discovered'}
        </div>
        <div className="pt-1 text-xs text-finnieGray-secondary break-all">
          Passphrase: {signer?.passphraseFile || 'Not discovered'}
        </div>
      </div>

      <div className="flex flex-row items-start justify-center w-[360px] xl:w-[560px] gap-2.5 mb-4 text-sm text-[#FFA54B]">
        <Icon source={WarningCircleLine} className="h-6 w-6 mt-1" />
        <div className="text-xs text-left font-light w-fit">
          Keep the JSON and passphrase together, private, and offline when
          possible. Anyone with both files can recover and use the wallet.
        </div>
      </div>

      {backupComplete && (
        <div className="flex items-center gap-2 text-finnieEmerald-light">
          <Icon source={CheckSuccessFill} className="h-5 w-5" />
          <span>{backupMessage}</span>
        </div>
      )}
      {!backupComplete && backupMessage && (
        <div className="text-sm text-[#FFA54B]">{backupMessage}</div>
      )}
      <ErrorMessage
        error={
          backupError
            ? `QSDM wallet backup failed: ${formatError(backupError)}`
            : coreStatusError
            ? `QSDM signer check failed: ${formatError(coreStatusError)}`
            : !coreStatusLoading && !signerReady
            ? signer?.reason || 'QSDM signer is not ready yet.'
            : null
        }
      />

      <div className="flex flex-col items-center gap-3">
        <Button
          label={backingUp ? 'Backing up...' : 'Back Up QSDM Wallet'}
          className="font-semibold bg-finnieGray-light text-finnieBlue-light w-[260px] h-[48px]"
          onClick={() => {
            setBackupMessage('');
            backupWallet();
          }}
          disabled={!signerReady || backingUp}
          loading={backingUp}
        />
        <Button
          label={backupComplete ? 'Continue' : 'I already backed this up'}
          className="font-semibold bg-transparent text-white w-auto h-[48px] px-6 py-[14px] underline hover:border-2 hover:border-white"
          onClick={continueToFunding}
        />
      </div>
    </div>
  );
}
