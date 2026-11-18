import {
  CheckSuccessLine,
  CloseLine,
  Icon,
  KeyUnlockLine,
  UploadLine,
} from 'vendor/qsdm-styleguide';
import React, { ChangeEvent, memo, useState } from 'react';
import { useMutation, useQueryClient } from 'react-query';

import { Button, ErrorMessage } from 'renderer/components/ui';
import { importQsdmSignerWallet, QueryKeys } from 'renderer/services';
import { Theme } from 'renderer/types/common';
import { ModalContent } from 'renderer/features/modals';

type PropsType = Readonly<{
  onClose: () => void;
  onImportSuccess: (keys: {
    accountName: string;
    mainAccountPubKey: string;
  }) => void;
}>;

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

function ImportQsdmWallet({ onClose, onImportSuccess }: PropsType) {
  const queryCache = useQueryClient();
  const [keystoreJson, setKeystoreJson] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [fileName, setFileName] = useState('');
  const [error, setError] = useState('');
  const [makeActive, setMakeActive] = useState(false);

  const {
    mutate: importWallet,
    isLoading,
    error: importError,
  } = useMutation(
    () =>
      importQsdmSignerWallet({
        keystoreJson,
        passphrase,
        makeActive,
      }),
    {
      onSuccess: async (result) => {
        await queryCache.invalidateQueries([QueryKeys.QsdmWalletList]);
        await queryCache.invalidateQueries([QueryKeys.QsdmCoreStatus]);
        await queryCache.invalidateQueries([QueryKeys.QsdmCellAccount]);
        await queryCache.invalidateQueries([QueryKeys.AccountBalance]);
        await queryCache.invalidateQueries([QueryKeys.MainAccountBalance]);
        onImportSuccess({
          accountName: 'QSDM Signer',
          mainAccountPubKey: result.address,
        });
      },
    }
  );

  const onFileUpload = async (event: ChangeEvent<HTMLInputElement>) => {
    const uploadedFile = event.target.files?.[0];
    setError('');

    if (!uploadedFile) {
      setFileName('');
      setKeystoreJson('');
      return;
    }

    if (!uploadedFile.name.toLowerCase().endsWith('.json')) {
      setError('Choose a QSDM keystore JSON file');
      setFileName('');
      setKeystoreJson('');
      return;
    }

    const fileContent = await uploadedFile.text();
    setFileName(uploadedFile.name);
    setKeystoreJson(fileContent);
  };

  const handleImport = () => {
    setError('');
    if (!keystoreJson.trim()) {
      setError('Choose a QSDM keystore JSON file');
      return;
    }
    if (!passphrase) {
      setError('Enter the QSDM wallet passphrase');
      return;
    }
    importWallet();
  };

  return (
    <ModalContent theme={Theme.Dark} className="w-[760px] h-fit pt-4 pb-6">
      <div className="text-white">
        <div className="flex justify-between p-3">
          <div className="flex items-center justify-between gap-6 pl-6">
            <Icon source={KeyUnlockLine} className="w-7 h-7" />
            <span className="text-[24px]">Import QSDM signer wallet</span>
          </div>
          <Icon
            source={CloseLine}
            className="w-8 h-8 cursor-pointer"
            onClick={onClose}
          />
        </div>

        <div className="flex flex-col w-full p-4 px-14">
          <label
            htmlFor="qsdm-keystore-file"
            className="bg-[rgba(245,245,245,0.15)] w-full h-[150px] mt-2 rounded-lg cursor-pointer"
          >
            <input
              type="file"
              accept=".json,application/json"
              className="hidden"
              onChange={onFileUpload}
              id="qsdm-keystore-file"
            />
            {fileName ? (
              <div className="w-full h-full flex items-center justify-center flex-col text-white text-sm leading-4">
                <Icon
                  source={CheckSuccessLine}
                  className="w-7 h-7 text-finnieEmerald-light"
                />
                <p className="mt-5">{fileName}</p>
              </div>
            ) : (
              <div className="w-full h-full flex items-center justify-center flex-col text-white text-sm leading-4">
                <Icon source={UploadLine} className="w-7 h-7" />
                <p className="mt-5">QSDM keystore JSON</p>
              </div>
            )}
          </label>

          <input
            className="w-full px-6 py-3 mt-5 rounded-md bg-finnieBlue-light-tertiary focus:ring-2 focus:ring-finnieTeal focus:outline-none text-sm focus:bg-finnieBlue-light-secondary"
            type="password"
            value={passphrase}
            onChange={(event) => {
              setError('');
              setPassphrase(event.target.value);
            }}
            placeholder="QSDM wallet passphrase"
          />
          <div className="pt-4 text-xs text-white/70">
            Hive adds this wallet beside any wallet it already holds; nothing is
            replaced. Switch wallets any time in Settings, Wallets.
          </div>
          <label className="mt-2 flex items-center gap-2 text-sm text-white">
            <input
              type="checkbox"
              checked={makeActive}
              onChange={(event) => setMakeActive(event.target.checked)}
              className="h-4 w-4"
            />
            <span>Switch to this wallet now</span>
          </label>
        </div>

        <div className="w-full flex items-center justify-center flex-col gap-4 pt-2">
          <ErrorMessage
            error={
              error ||
              (importError
                ? `Import failed: ${getErrorMessage(importError)}`
                : null)
            }
          />
          <Button
            onClick={handleImport}
            label="Import Wallet"
            disabled={isLoading || !keystoreJson.trim() || !passphrase}
            loading={isLoading}
            className="w-[220px] h-12 rounded-md text-base font-bold bg-[#F5F5F5] text-purple-4"
          />
        </div>
      </div>
    </ModalContent>
  );
}

export default memo(ImportQsdmWallet);
