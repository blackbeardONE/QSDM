import { decrypt } from '@metamask/browser-passworder';
import React, { useCallback, useEffect, useState } from 'react';
import { useQueryClient } from 'react-query';

import { PinInput } from 'renderer/components/PinInput';
import { ErrorMessage, Button } from 'renderer/components/ui';
import { useKeyInput } from 'renderer/features/common/hooks';
import { useUserAppConfig } from 'renderer/features/settings/hooks';
import {
  exportQsdmSignerWalletBackup,
  getEncryptedSecretPhrase,
  QueryKeys,
} from 'renderer/services';
import { validatePin } from 'renderer/utils';

import { Steps } from '../types';

type PropsType = Readonly<{
  setNextStep: (step: Steps) => void;
  accountName: string;
  publicKey: string;
  setSeedPhrase: (seedPhrase: string) => void;
  onOpenWalletBackup: () => void;
}>;

export function EnterNodePassword({
  setNextStep,
  accountName,
  publicKey,
  setSeedPhrase,
  onOpenWalletBackup,
}: PropsType) {
  const [pin, setPin] = useState('');
  const [error, setError] = useState<Error | string>('');
  const [backupMessage, setBackupMessage] = useState('');
  const [isBackingUp, setIsBackingUp] = useState(false);
  const [hasProfilePhrase, setHasProfilePhrase] = useState<boolean | null>(
    null
  );
  const queryCache = useQueryClient();
  const { userConfig: settings } = useUserAppConfig({});

  useEffect(() => {
    let isMounted = true;

    getEncryptedSecretPhrase(publicKey)
      .then((encryptedSecretPhrase) => {
        if (isMounted) {
          setHasProfilePhrase(!!encryptedSecretPhrase);
        }
      })
      .catch(() => {
        if (isMounted) {
          setHasProfilePhrase(true);
        }
      });

    return () => {
      isMounted = false;
    };
  }, [publicKey]);

  const handleShowPhrase = async () => {
    const isPinValid = await validatePin(pin, settings?.pin);

    if (isPinValid && settings?.pin) {
      const encryptedSecretPhrase = await getEncryptedSecretPhrase(publicKey);
      if (!encryptedSecretPhrase) {
        setHasProfilePhrase(false);
        return setError(
          "This local profile was not generated with a Hive profile phrase. Back up your CELL wallet from Settings > Wallet using QSDM keystore JSON + passphrase."
        );
      }
      try {
        const seedPhrase: string = (await decrypt(
          pin,
          encryptedSecretPhrase
        )) as string;
        setSeedPhrase(seedPhrase);
        setNextStep(Steps.ShowSecretPhase);
      } catch (error) {
        console.log(
          'First attempt of decrypting the Hive recovery phrase failed, trying to use the old pin from DB...'
        );
        try {
          const seedPhrase: string = (await decrypt(
            settings?.pin as string,
            encryptedSecretPhrase
          )) as string;
          setSeedPhrase(seedPhrase);
          setNextStep(Steps.ShowSecretPhase);
        } catch (error) {
          console.log(
            'Second attempt of decrypting the Hive recovery phrase failed'
          );
          setError('Failed to decrypt the Hive recovery phrase');
        }
      } finally {
        queryCache.invalidateQueries(QueryKeys.Accounts);
      }
    } else {
      setError("Whoops. That PIN isn't right. Double check it and try again.");
    }
  };

  const handlePinInputChange = useCallback((pin: string) => {
    setPin(pin);
  }, []);

  const handleBackupWallet = async () => {
    setError('');
    setBackupMessage('');
    setIsBackingUp(true);

    try {
      const result = await exportQsdmSignerWalletBackup();
      if (result.exported) {
        setBackupMessage('QSDM wallet JSON and passphrase were backed up.');
        return;
      }
      setBackupMessage('QSDM wallet backup was cancelled.');
    } catch (error) {
      const message =
        error instanceof Error ? error.message : 'QSDM wallet backup failed.';
      setError(message);
    } finally {
      setIsBackingUp(false);
    }
  };

  useKeyInput(
    'Enter',
    handleShowPhrase,
    !hasProfilePhrase || accountName.length === 0 || pin.length !== 6
  );

  if (hasProfilePhrase === false) {
    return (
      <div className="text-white">
        <div className="flex px-12 pt-6 text-lg font-semibold leading-6 text-white">
          Account Name:{' '}
          <span className="ml-1 font-normal tracking-tight">
            {accountName}
          </span>
        </div>

        <div className="px-12 pt-4 text-base leading-[30px] tracking-tight">
          This local Hive profile was not generated with a profile phrase.
          QSDM CELL wallets are recovered using the QSDM keystore JSON and its
          passphrase.
        </div>

        <div className="mx-12 mt-5 rounded-md bg-finnieBlue-light-tertiary p-4 text-sm leading-6 text-finnieTeal-100">
          Open Wallet settings to back up your real QSDM wallet files. Keep the
          JSON and passphrase together, private, and offline when possible.
        </div>

        {backupMessage && (
          <div className="mx-12 mt-4 text-center text-sm text-finnieEmerald-light">
            {backupMessage}
          </div>
        )}

        {error && (
          <div className="flex flex-col items-center px-4 pt-4">
            <ErrorMessage error={error} />
          </div>
        )}

        <div className="flex justify-center pt-6">
          <button
            type="button"
            onClick={handleBackupWallet}
            disabled={isBackingUp}
            className="flex h-[48px] w-[230px] items-center justify-center rounded bg-white font-semibold text-finnieBlue-light disabled:cursor-not-allowed disabled:opacity-60"
          >
            {isBackingUp ? 'Backing Up...' : 'Back Up QSDM Wallet'}
          </button>
        </div>

        <div className="flex justify-center pt-3">
          <button
            type="button"
            onClick={onOpenWalletBackup}
            className="text-sm font-semibold text-finnieTeal-100 underline"
          >
            Open Wallet Settings
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="text-white">
      <div className="flex px-12 pt-6 text-lg font-semibold leading-6 text-white">
        Account Name:{' '}
        <span className="ml-1 font-normal tracking-tight">{accountName}</span>
      </div>

      <p className="px-12 mt-1.5 tracking-tight w-full items-start text-start leading-[32px] text-base">
        This phrase restores only the local Hive profile.{' '}
        <span className="font-bold">
          CELL wallet backup is in Settings &gt; Wallet as QSDM keystore JSON
          plus passphrase.
        </span>
      </p>

      <div className="px-10 mt-5">
        <p className="pl-2 mb-1.5 text-left uppercase tracking-widest">
          Enter node Access PIN
        </p>
        <div className="bg-transparent w-full px-2.5 pt-5 pb-4 flex flex-col items-start rounded-md">
          <PinInput onChange={handlePinInputChange} />
        </div>
      </div>

      {error && (
        <div className="flex flex-col items-center px-4">
          <ErrorMessage error={error} />
        </div>
      )}

      <div className="flex justify-center pt-2">
        <Button
          disabled={
            hasProfilePhrase !== true || accountName.length === 0 || pin.length !== 6
          }
          onClick={handleShowPhrase}
          label="Reveal Profile Phrase"
          className="font-semibold bg-white text-finnieBlue-light w-[220px] h-[48px]"
        />
      </div>
    </div>
  );
}
