import React, { ChangeEvent, useEffect, useMemo, useState } from 'react';
import toast from 'react-hot-toast';
import { useMutation, useQuery, useQueryClient } from 'react-query';

import NativeTokenLogo from 'assets/svgs/qsdm-hive-logo.svg';
import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import {
  Button,
  CopyButton,
  ErrorMessage,
  LoadingSpinner,
} from 'renderer/components/ui';
import { useClipboard } from 'renderer/features/common/hooks';
import {
  createQsdmSignerWallet,
  enableQsdmSignerLegacyRecovery,
  exportQsdmSignerRecoveryWords,
  exportQsdmSignerWalletBackup,
  getQsdmCellAccount,
  getQsdmCoreStatus,
  getQsdmWalletProviderPermissions,
  importQsdmSignerWallet,
  openBrowserWindow,
  QueryKeys,
  revokeQsdmWalletProviderPermission,
  restoreQsdmSignerWallet,
  transferCellFromMainWallet,
  unlockQsdmSignerWallet,
} from 'renderer/services';
import { isValidWalletAddress } from 'renderer/utils';

const formatAddress = (address?: string) => {
  if (!address) return 'Not configured';
  return address.length > 24
    ? `${address.slice(0, 12)}...${address.slice(-12)}`
    : address;
};

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

const formatPath = (filePath?: string) => filePath || 'Not discovered';

const formatPermissionDate = (value: string) => {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? 'Unknown' : parsed.toLocaleString();
};

const WALLET_PROVIDER_PERMISSIONS_QUERY_KEY = [
  'qsdm-wallet-provider-permissions',
];
const QSDM_ACCOUNT_URL = 'https://qsdm.tech/account/';

export function QsdmWalletPanel() {
  const queryClient = useQueryClient();
  const { copyToClipboard, copied } = useClipboard();
  const [recipient, setRecipient] = useState('');
  const [recipientIsValid, setRecipientIsValid] = useState(true);
  const [amount, setAmount] = useState('');
  const [keystoreJson, setKeystoreJson] = useState('');
  const [keystoreFileName, setKeystoreFileName] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [unlockPassphrase, setUnlockPassphrase] = useState('');
  const [walletSetupMode, setWalletSetupMode] = useState<
    'create' | 'restore' | 'unlock' | 'import'
  >('create');
  const [newPassphrase, setNewPassphrase] = useState('');
  const [confirmPassphrase, setConfirmPassphrase] = useState('');
  const [createMessage, setCreateMessage] = useState('');
  const [createdRecoveryWords, setCreatedRecoveryWords] = useState('');
  const [restoreRecoveryWords, setRestoreRecoveryWords] = useState('');
  const [restoreRecoveryType, setRestoreRecoveryType] = useState<
    'native' | 'legacy'
  >('native');
  const [restorePassphrase, setRestorePassphrase] = useState('');
  const [restoreConfirmPassphrase, setRestoreConfirmPassphrase] = useState('');
  const [restoreReplacementAcknowledged, setRestoreReplacementAcknowledged] =
    useState(false);
  const [restoreMessage, setRestoreMessage] = useState('');
  const [importMessage, setImportMessage] = useState('');
  const [unlockMessage, setUnlockMessage] = useState('');
  const [backupMessage, setBackupMessage] = useState('');
  const [recoveryExportPassphrase, setRecoveryExportPassphrase] = useState('');
  const [recoveryExportMessage, setRecoveryExportMessage] = useState('');
  const [legacyRecoveryPassphrase, setLegacyRecoveryPassphrase] = useState('');
  const [legacyRecoveryMessage, setLegacyRecoveryMessage] = useState('');
  const [transferMessage, setTransferMessage] = useState('');

  const {
    data: coreStatus,
    isLoading: coreStatusLoading,
    error: coreStatusError,
  } = useQuery(
    [QueryKeys.QsdmCoreStatus, 'wallet-settings'],
    getQsdmCoreStatus,
    {
      refetchInterval: 15000,
    }
  );

  const {
    data: cellAccount,
    isLoading: cellAccountLoading,
    error: cellAccountError,
  } = useQuery(
    [QueryKeys.QsdmCellAccount, 'wallet-settings'],
    () => getQsdmCellAccount(),
    {
      refetchInterval: 15000,
    }
  );

  const {
    data: providerPermissions,
    isLoading: providerPermissionsLoading,
    error: providerPermissionsError,
  } = useQuery(
    WALLET_PROVIDER_PERMISSIONS_QUERY_KEY,
    getQsdmWalletProviderPermissions,
    { refetchInterval: 30000 }
  );

  const signer = coreStatus?.taskSigner;
  const address = cellAccount?.address || signer?.sender;
  const balance = cellAccount?.balance;
  const amountNumber = Number(amount);
  const canSend =
    !!address &&
    signer?.ready &&
    !!coreStatus?.canonicalSafety?.safe &&
    recipientIsValid &&
    !!recipient.trim() &&
    Number.isFinite(amountNumber) &&
    amountNumber > 0;

  const sendError = useMemo(() => {
    if (!recipient.trim() || recipientIsValid) return '';
    return 'Destination must be a QSDM hex address or a legacy base58 address';
  }, [recipient, recipientIsValid]);

  const canImportSigner = !!keystoreJson.trim() && !!passphrase;
  const canUnlockSigner = !!signer?.keystorePath && !!unlockPassphrase;
  const canCreateSigner =
    !signer?.ready &&
    newPassphrase.length >= 12 &&
    newPassphrase === confirmPassphrase;
  const canRestoreSigner =
    restoreRecoveryWords.trim().split(/\s+/).length === 24 &&
    restorePassphrase.length >= 12 &&
    restorePassphrase === restoreConfirmPassphrase &&
    (!signer?.ready || restoreReplacementAcknowledged);

  useEffect(() => {
    if (!signer?.ready && signer?.keystorePath) {
      setWalletSetupMode((current) =>
        current === 'create' ? 'unlock' : current
      );
    }
  }, [signer?.keystorePath, signer?.ready]);

  const {
    mutate: createSigner,
    isLoading: creatingSigner,
    error: createSignerError,
  } = useMutation(() => createQsdmSignerWallet({ passphrase: newPassphrase }), {
    onSuccess: async (result) => {
      setNewPassphrase('');
      setConfirmPassphrase('');
      setCreatedRecoveryWords(result.recoveryWords || '');
      setCreateMessage(
        `Created ${formatAddress(
          result.address
        )}. Write down the 24 recovery words before leaving this screen.`
      );
      await refresh();
      await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
      await queryClient.invalidateQueries([QueryKeys.MainAccountBalance]);
    },
  });

  const {
    mutate: restoreSigner,
    isLoading: restoringSigner,
    error: restoreSignerError,
  } = useMutation(
    () =>
      restoreQsdmSignerWallet({
        recoveryWords: restoreRecoveryWords,
        passphrase: restorePassphrase,
        recoveryType: restoreRecoveryType,
      }),
    {
      onSuccess: async (result) => {
        setRestoreRecoveryWords('');
        setRestorePassphrase('');
        setRestoreConfirmPassphrase('');
        setRestoreReplacementAcknowledged(false);
        setRestoreMessage(`Restored ${formatAddress(result.address)}`);
        await refresh();
        await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
        await queryClient.invalidateQueries([QueryKeys.MainAccountBalance]);
      },
    }
  );

  const {
    mutate: sendCell,
    isLoading: sending,
    error: transferError,
  } = useMutation(
    () =>
      transferCellFromMainWallet(
        '__qsdm_signer__',
        amountNumber,
        recipient.trim()
      ),
    {
      onMutate: () => {
        setTransferMessage('');
      },
      onSuccess: async (result) => {
        const sentAmount = amountNumber;
        const sentRecipient = recipient.trim();
        const txId =
          result && typeof result === 'object' && 'transaction_id' in result
            ? result.transaction_id
            : '';
        const message = txId
          ? `Sent ${sentAmount} ${NATIVE_TOKEN_SYMBOL} to ${formatAddress(
              sentRecipient
            )}. Tx: ${txId}`
          : `Sent ${sentAmount} ${NATIVE_TOKEN_SYMBOL} to ${formatAddress(
              sentRecipient
            )}.`;
        setTransferMessage(message);
        toast.success(message);
        setRecipient('');
        setAmount('');
        await queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
        await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
        await queryClient.invalidateQueries([QueryKeys.MainAccountBalance]);
      },
      onError: (error) => {
        toast.error(`CELL transfer failed: ${getErrorMessage(error)}`);
      },
    }
  );

  const {
    mutate: importSigner,
    isLoading: importingSigner,
    error: importSignerError,
  } = useMutation(
    () =>
      importQsdmSignerWallet({
        keystoreJson,
        passphrase,
      }),
    {
      onSuccess: async (result) => {
        setKeystoreJson('');
        setKeystoreFileName('');
        setPassphrase('');
        setImportMessage(`Imported ${formatAddress(result.address)}`);
        await refresh();
        await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
        await queryClient.invalidateQueries([QueryKeys.MainAccountBalance]);
      },
    }
  );

  const {
    mutate: backupSigner,
    isLoading: backingUpSigner,
    error: backupSignerError,
  } = useMutation(exportQsdmSignerWalletBackup, {
    onSuccess: (result) => {
      if (!result.exported) {
        setBackupMessage('QSDM wallet backup was cancelled.');
        return;
      }
      setBackupMessage(
        `Backed up encrypted wallet JSON for ${formatAddress(
          result.address
        )}. Your passphrase is intentionally not copied beside it.`
      );
    },
  });

  const {
    mutate: exportRecoveryWords,
    isLoading: exportingRecoveryWords,
    error: exportRecoveryWordsError,
  } = useMutation(
    () =>
      exportQsdmSignerRecoveryWords({
        passphrase: recoveryExportPassphrase,
      }),
    {
      onSuccess: (result) => {
        setRecoveryExportPassphrase('');
        setRecoveryExportMessage(
          result.exported
            ? `Saved QSDM Recovery Words to ${result.recoveryBackupPath}`
            : 'QSDM Recovery Words export was cancelled.'
        );
      },
    }
  );

  const {
    mutate: enableLegacyRecovery,
    isLoading: enablingLegacyRecovery,
    error: enableLegacyRecoveryError,
  } = useMutation(
    () =>
      enableQsdmSignerLegacyRecovery({
        passphrase: legacyRecoveryPassphrase,
      }),
    {
      onSuccess: async (result) => {
        setLegacyRecoveryPassphrase('');
        setLegacyRecoveryMessage(
          result.enabled
            ? `Recovery is now enabled for ${formatAddress(
                result.address
              )}. The wallet address did not change. Recovery Words were saved to ${
                result.recoveryBackupPath
              }.`
            : 'Recovery activation was cancelled.'
        );
        await refresh();
      },
    }
  );

  const {
    mutate: unlockSigner,
    isLoading: unlockingSigner,
    error: unlockSignerError,
  } = useMutation(
    () => unlockQsdmSignerWallet({ passphrase: unlockPassphrase }),
    {
      onSuccess: async (result) => {
        setUnlockPassphrase('');
        setUnlockMessage(`Unlocked ${formatAddress(result.address)}`);
        await refresh();
        await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
        await queryClient.invalidateQueries([QueryKeys.MainAccountBalance]);
      },
    }
  );

  const {
    mutate: revokeProviderPermission,
    isLoading: revokingProviderPermission,
    error: revokeProviderPermissionError,
  } = useMutation(
    (origin: string) => revokeQsdmWalletProviderPermission({ origin }),
    {
      onSuccess: async (result) => {
        toast.success(
          result.revoked
            ? `Disconnected ${result.origin}`
            : `${result.origin} was already disconnected`
        );
        await queryClient.invalidateQueries(
          WALLET_PROVIDER_PERMISSIONS_QUERY_KEY
        );
      },
    }
  );

  const refresh = async () => {
    await queryClient.invalidateQueries([QueryKeys.QsdmCoreStatus]);
    await queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
  };

  const handleRecipientChange = async (
    event: ChangeEvent<HTMLInputElement>
  ) => {
    const { value } = event.target;
    setTransferMessage('');
    setRecipient(value);
    setRecipientIsValid(!value.trim() || (await isValidWalletAddress(value)));
  };

  const handleAmountChange = (event: ChangeEvent<HTMLInputElement>) => {
    setTransferMessage('');
    setAmount(event.target.value);
  };

  const handleKeystoreFileChange = async (
    event: ChangeEvent<HTMLInputElement>
  ) => {
    const file = event.target.files?.[0];
    setImportMessage('');

    if (!file) {
      setKeystoreJson('');
      setKeystoreFileName('');
      return;
    }

    const content = await file.text();
    setKeystoreJson(content);
    setKeystoreFileName(file.name);
  };

  return (
    <section className="w-[90%] p-5 mb-6 rounded-lg border border-purple-1 bg-purple-1 bg-opacity-20">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="text-xl font-semibold">QSDM Wallet</div>
          <div className="pt-1 text-sm text-finnieGray-secondary">
            Your QSDM account for Hive, CELL, tasks, and connected websites.
            {!signer?.ready
              ? ' Create or restore a wallet below.'
              : signer.recoveryEnabled
              ? ' This wallet can be restored with its 24 QSDM Recovery Words.'
              : ' This older wallet can enable 24-word recovery below.'}
          </div>
        </div>
        <div className="flex items-center gap-3">
          <span
            className={`rounded-full px-3 py-1 text-xs ${
              signer?.ready
                ? 'bg-finnieEmerald-light text-finnieBlue-dark'
                : 'bg-finnieOrange text-finnieBlue-dark'
            }`}
          >
            {signer?.ready ? 'Signer ready' : 'Signer setup needed'}
          </span>
          <Button
            label="QSDM Account"
            onClick={() => openBrowserWindow(QSDM_ACCOUNT_URL)}
            className="h-9 w-36 bg-finnieBlue-light-secondary"
          />
          <Button
            label="Refresh"
            onClick={refresh}
            className="w-24 h-9 bg-finnieBlue-light-secondary"
          />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 pt-5 md:grid-cols-3">
        <div className="rounded-md bg-finnieBlue-light-tertiary p-4">
          <div className="text-xs text-finnieGray-secondary">Address</div>
          <div className="flex items-center gap-2 pt-2 text-sm font-semibold">
            <span className="truncate">{formatAddress(address)}</span>
            {address && (
              <CopyButton
                onCopy={() => copyToClipboard(address)}
                isCopied={copied}
              />
            )}
          </div>
        </div>
        <div className="rounded-md bg-finnieBlue-light-tertiary p-4">
          <div className="text-xs text-finnieGray-secondary">Balance</div>
          <div className="flex items-center gap-2 pt-2 text-2xl font-semibold">
            {cellAccountLoading ? (
              <LoadingSpinner />
            ) : (
              <>
                {typeof balance === 'number' ? balance.toFixed(3) : '-'}{' '}
                {NATIVE_TOKEN_SYMBOL}
                <NativeTokenLogo className="w-8 h-8" />
              </>
            )}
          </div>
        </div>
        <div className="rounded-md bg-finnieBlue-light-tertiary p-4">
          <div className="text-xs text-finnieGray-secondary">Nonce</div>
          <div className="pt-2 text-2xl font-semibold">
            {cellAccountLoading ? '-' : cellAccount?.nextNonce ?? '-'}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 pt-3 md:grid-cols-3">
        <div className="rounded-md bg-finnieBlue-light-tertiary p-4">
          <div className="text-xs text-finnieGray-secondary">Recovery</div>
          <div className="pt-2 text-sm font-semibold">
            {!signer?.ready
              ? 'Not configured'
              : signer.recoveryEnabled
              ? `${signer.recoveryWords || 24} QSDM Recovery Words${
                  signer.recoveryScheme === 'qsdm-legacy-wallet-recovery-v1'
                    ? ' (upgraded wallet)'
                    : ''
                }`
              : 'Legacy JSON + passphrase'}
          </div>
          <div className="pt-1 text-xs text-finnieGray-secondary">
            {!signer?.ready
              ? 'Create a new recovery-enabled wallet or restore one below.'
              : signer.recoveryEnabled
              ? 'These words rebuild the same wallet. Keep the encrypted JSON too for routine backup.'
              : 'This wallet predates Recovery Words. Enable them once with the existing passphrase; its address, CELL, stakes, and task ownership will not change.'}
          </div>
          {signer?.recoveryEnabled && (
            <input
              value={recoveryExportPassphrase}
              onChange={(event) => {
                setRecoveryExportMessage('');
                setRecoveryExportPassphrase(event.target.value);
              }}
              type="password"
              autoComplete="current-password"
              aria-label="Wallet passphrase for recovery export"
              placeholder="Passphrase to export words"
              className="mt-3 h-9 w-full rounded-md bg-finnieBlue-dark px-3 text-xs text-white outline-none"
            />
          )}
          {signer?.ready && !signer.recoveryEnabled && (
            <input
              value={legacyRecoveryPassphrase}
              onChange={(event) => {
                setLegacyRecoveryMessage('');
                setLegacyRecoveryPassphrase(event.target.value);
              }}
              type="password"
              autoComplete="current-password"
              aria-label="Existing wallet passphrase for recovery activation"
              placeholder="Existing wallet passphrase"
              className="mt-3 h-9 w-full rounded-md bg-finnieBlue-dark px-3 text-xs text-white outline-none"
            />
          )}
          <div className="flex flex-wrap gap-2 pt-2">
            <Button
              label="Backup JSON"
              onClick={() => backupSigner()}
              disabled={!signer?.ready || backingUpSigner}
              loading={backingUpSigner}
              className="h-9 w-32 bg-finnieTeal-100 text-finnieBlue-dark"
            />
            <Button
              label="Export Words"
              onClick={() => exportRecoveryWords()}
              disabled={
                !signer?.ready ||
                !signer?.recoveryEnabled ||
                !recoveryExportPassphrase ||
                exportingRecoveryWords
              }
              loading={exportingRecoveryWords}
              className="h-9 w-32 bg-finnieBlue-light-secondary"
            />
            {signer?.ready && !signer.recoveryEnabled && (
              <Button
                label="Enable Recovery"
                onClick={() => enableLegacyRecovery()}
                disabled={!legacyRecoveryPassphrase || enablingLegacyRecovery}
                loading={enablingLegacyRecovery}
                className="h-9 w-40 bg-finnieTeal-100 text-finnieBlue-dark"
              />
            )}
          </div>
        </div>
        <div className="rounded-md bg-finnieBlue-light-tertiary p-4">
          <div className="text-xs text-finnieGray-secondary">Keystore JSON</div>
          <div className="pt-2 text-xs font-semibold break-all">
            {formatPath(signer?.keystorePath)}
          </div>
        </div>
        <div className="rounded-md bg-finnieBlue-light-tertiary p-4">
          <div className="text-xs text-finnieGray-secondary">
            Local Passphrase
          </div>
          <div className="pt-2 text-xs font-semibold break-all">
            {signer?.ready ? 'Managed by QSDM Hive' : 'Not configured'}
          </div>
        </div>
      </div>

      <div className="mt-5 border-t border-finnieGray-tertiary pt-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <div className="text-base font-semibold">Connected Sites</div>
            <div className="pt-1 text-xs text-finnieGray-secondary">
              Connect each website once with the QSDM Wallet extension. Hive
              remembers approved sites until you revoke them here. Your keystore
              and passphrase never leave Hive, and every signature or CELL
              transfer still requires your approval.
            </div>
          </div>
          <span className="text-xs text-finnieGray-secondary">
            {providerPermissions?.permissions.length || 0} connected
          </span>
        </div>

        <div className="pt-3">
          {providerPermissionsLoading ? (
            <LoadingSpinner />
          ) : providerPermissions?.permissions.length ? (
            <div className="divide-y divide-finnieGray-tertiary">
              {providerPermissions.permissions.map((permission) => {
                const isActiveWallet =
                  permission.address.toLowerCase() === address?.toLowerCase();
                return (
                  <div
                    key={`${permission.origin}:${permission.address}`}
                    className="flex flex-wrap items-center justify-between gap-3 py-3"
                  >
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold">
                        {permission.origin}
                      </div>
                      <div className="pt-1 text-xs text-finnieGray-secondary">
                        Wallet {formatAddress(permission.address)} | Last used{' '}
                        {formatPermissionDate(permission.lastUsedAt)}
                      </div>
                      {!isActiveWallet && (
                        <div className="pt-1 text-xs text-finnieOrange">
                          This grant belongs to a different wallet and is not
                          active.
                        </div>
                      )}
                    </div>
                    <Button
                      label="Revoke"
                      onClick={() =>
                        revokeProviderPermission(permission.origin)
                      }
                      disabled={revokingProviderPermission}
                      className="h-9 w-24 bg-finnieBlue-light-secondary"
                    />
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="text-sm text-finnieGray-secondary">
              No websites are connected to this wallet yet.
            </div>
          )}
        </div>
        <ErrorMessage
          error={
            providerPermissionsError || revokeProviderPermissionError
              ? `Connected-site management failed: ${getErrorMessage(
                  providerPermissionsError || revokeProviderPermissionError
                )}`
              : null
          }
          className="py-2"
        />
      </div>

      <div className="flex gap-2 pt-5" role="tablist" aria-label="Wallet setup">
        <button
          type="button"
          className={`h-9 px-4 rounded-md text-sm font-semibold ${
            walletSetupMode === 'create'
              ? 'bg-finnieTeal-100 text-finnieBlue-dark'
              : 'bg-finnieBlue-light-tertiary text-white'
          }`}
          onClick={() => setWalletSetupMode('create')}
        >
          Create New Wallet
        </button>
        <button
          type="button"
          className={`h-9 px-4 rounded-md text-sm font-semibold ${
            walletSetupMode === 'restore'
              ? 'bg-finnieTeal-100 text-finnieBlue-dark'
              : 'bg-finnieBlue-light-tertiary text-white'
          }`}
          onClick={() => setWalletSetupMode('restore')}
        >
          Restore with 24 Words
        </button>
        {!!signer?.keystorePath && !signer?.ready && (
          <button
            type="button"
            className={`h-9 px-4 rounded-md text-sm font-semibold ${
              walletSetupMode === 'unlock'
                ? 'bg-finnieTeal-100 text-finnieBlue-dark'
                : 'bg-finnieBlue-light-tertiary text-white'
            }`}
            onClick={() => setWalletSetupMode('unlock')}
          >
            Unlock Existing Wallet
          </button>
        )}
        <button
          type="button"
          className={`h-9 px-4 rounded-md text-sm font-semibold ${
            walletSetupMode === 'import'
              ? 'bg-finnieTeal-100 text-finnieBlue-dark'
              : 'bg-finnieBlue-light-tertiary text-white'
          }`}
          onClick={() => setWalletSetupMode('import')}
        >
          Import Existing Wallet
        </button>
      </div>

      {walletSetupMode === 'create' && (
        <div className="pt-3">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-[1fr_1fr_170px]">
            <div>
              <label
                className="text-xs text-finnieGray-secondary"
                htmlFor="qsdm-new-passphrase"
              >
                New passphrase
              </label>
              <input
                id="qsdm-new-passphrase"
                value={newPassphrase}
                onChange={(event) => {
                  setCreateMessage('');
                  setNewPassphrase(event.target.value);
                }}
                type="password"
                autoComplete="new-password"
                className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
                placeholder="At least 12 characters"
              />
            </div>
            <div>
              <label
                className="text-xs text-finnieGray-secondary"
                htmlFor="qsdm-confirm-passphrase"
              >
                Confirm passphrase
              </label>
              <input
                id="qsdm-confirm-passphrase"
                value={confirmPassphrase}
                onChange={(event) => {
                  setCreateMessage('');
                  setConfirmPassphrase(event.target.value);
                }}
                type="password"
                autoComplete="new-password"
                className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
              />
            </div>
            <div className="flex items-end">
              <Button
                label="Create Wallet"
                onClick={() => createSigner()}
                disabled={!canCreateSigner || creatingSigner}
                loading={creatingSigner}
                className="h-10 w-full bg-finnieTeal-100 text-finnieBlue-dark"
              />
            </div>
          </div>
          {createdRecoveryWords && (
            <div className="mt-4 border border-finnieOrange p-4">
              <div className="text-base font-semibold">
                Write down these 24 QSDM Recovery Words
              </div>
              <div className="pt-1 text-xs text-finnieOrange">
                Anyone with these words controls this wallet. Store them offline
                and never send them to a website or support person.
              </div>
              <ol className="mt-3 grid grid-cols-2 gap-x-5 gap-y-2 md:grid-cols-4">
                {createdRecoveryWords.split(' ').map((word, index) => (
                  <li
                    key={`${word}-${index}`}
                    className="border-b border-finnieGray-tertiary py-1 text-sm"
                  >
                    <span className="mr-2 text-finnieGray-secondary">
                      {index + 1}.
                    </span>
                    {word}
                  </li>
                ))}
              </ol>
              <Button
                label="I Saved These Words"
                onClick={() => setCreatedRecoveryWords('')}
                className="mt-4 h-9 w-48 bg-finnieTeal-100 text-finnieBlue-dark"
              />
            </div>
          )}
        </div>
      )}

      {walletSetupMode === 'restore' && (
        <div className="pt-3">
          <label
            className="text-xs text-finnieGray-secondary"
            htmlFor="qsdm-recovery-type"
          >
            Recovery type
          </label>
          <select
            id="qsdm-recovery-type"
            value={restoreRecoveryType}
            onChange={(event) => {
              setRestoreMessage('');
              setRestoreRecoveryType(event.target.value as 'native' | 'legacy');
            }}
            className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
          >
            <option value="native">Newer recovery-enabled wallet</option>
            <option value="legacy">Older wallet upgraded in Hive</option>
          </select>
          <div className="pb-3 pt-1 text-xs text-finnieGray-secondary">
            {restoreRecoveryType === 'legacy'
              ? 'Use this for an older JSON wallet after Enable Recovery was completed. Hive retrieves its encrypted recovery capsule from QSDM Core.'
              : 'Use this for a wallet created with 24 QSDM Recovery Words.'}
          </div>
          <label
            className="text-xs text-finnieGray-secondary"
            htmlFor="qsdm-recovery-words"
          >
            24 QSDM Recovery Words
          </label>
          <textarea
            id="qsdm-recovery-words"
            value={restoreRecoveryWords}
            onChange={(event) => {
              setRestoreMessage('');
              setRestoreRecoveryWords(event.target.value);
            }}
            rows={4}
            autoComplete="off"
            spellCheck={false}
            className="mt-1 w-full resize-none rounded-md bg-finnieBlue-light-tertiary p-3 text-white outline-none"
            placeholder="Enter all 24 words, separated by spaces"
          />
          {signer?.ready && (
            <label className="mt-3 flex items-start gap-2 text-xs text-finnieOrange">
              <input
                type="checkbox"
                checked={restoreReplacementAcknowledged}
                onChange={(event) =>
                  setRestoreReplacementAcknowledged(event.target.checked)
                }
                className="mt-0.5 h-4 w-4 accent-finnieTeal-100"
              />
              <span>
                Replace the active signer on this device. CELL, stakes, and task
                ownership remain with the old address and are not moved
                automatically.
              </span>
            </label>
          )}
          <div className="grid grid-cols-1 gap-3 pt-3 md:grid-cols-[1fr_1fr_170px]">
            <div>
              <label
                className="text-xs text-finnieGray-secondary"
                htmlFor="qsdm-restore-passphrase"
              >
                New local passphrase
              </label>
              <input
                id="qsdm-restore-passphrase"
                value={restorePassphrase}
                onChange={(event) => setRestorePassphrase(event.target.value)}
                type="password"
                autoComplete="new-password"
                className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
                placeholder="At least 12 characters"
              />
            </div>
            <div>
              <label
                className="text-xs text-finnieGray-secondary"
                htmlFor="qsdm-restore-confirm-passphrase"
              >
                Confirm new passphrase
              </label>
              <input
                id="qsdm-restore-confirm-passphrase"
                value={restoreConfirmPassphrase}
                onChange={(event) =>
                  setRestoreConfirmPassphrase(event.target.value)
                }
                type="password"
                autoComplete="new-password"
                className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
              />
            </div>
            <div className="flex items-end">
              <Button
                label="Restore Wallet"
                onClick={() => restoreSigner()}
                disabled={!canRestoreSigner || restoringSigner}
                loading={restoringSigner}
                className="h-10 w-full bg-finnieTeal-100 text-finnieBlue-dark"
              />
            </div>
          </div>
        </div>
      )}

      {walletSetupMode === 'unlock' && (
        <div className="grid grid-cols-1 gap-3 pt-3 md:grid-cols-[1fr_180px]">
          <div>
            <label
              className="text-xs text-finnieGray-secondary"
              htmlFor="qsdm-unlock-passphrase"
            >
              Existing wallet passphrase
            </label>
            <input
              id="qsdm-unlock-passphrase"
              value={unlockPassphrase}
              onChange={(event) => {
                setUnlockMessage('');
                setUnlockPassphrase(event.target.value);
              }}
              type="password"
              autoComplete="current-password"
              className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
              placeholder="Passphrase for the wallet already on this device"
            />
          </div>
          <div className="flex items-end">
            <Button
              label="Unlock Wallet"
              onClick={() => unlockSigner()}
              disabled={!canUnlockSigner || unlockingSigner}
              loading={unlockingSigner}
              className="h-10 w-full bg-finnieTeal-100 text-finnieBlue-dark"
            />
          </div>
        </div>
      )}

      {walletSetupMode === 'import' && (
        <div className="grid grid-cols-1 gap-3 pt-3 md:grid-cols-[1fr_220px_150px]">
          <div>
            <label
              className="text-xs text-finnieGray-secondary"
              htmlFor="qsdm-keystore-json"
            >
              QSDM keystore JSON
            </label>
            <input
              id="qsdm-keystore-json"
              type="file"
              accept=".json,application/json"
              onChange={handleKeystoreFileChange}
              className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 py-2 text-sm text-white outline-none file:mr-3 file:rounded file:border-0 file:bg-finnieTeal-100 file:px-3 file:py-1 file:text-finnieBlue-dark"
            />
            {keystoreFileName && (
              <div className="pt-1 text-xs text-finnieTeal-100">
                {keystoreFileName}
              </div>
            )}
          </div>
          <div>
            <label
              className="text-xs text-finnieGray-secondary"
              htmlFor="qsdm-keystore-passphrase"
            >
              Passphrase
            </label>
            <input
              id="qsdm-keystore-passphrase"
              value={passphrase}
              onChange={(event) => {
                setImportMessage('');
                setPassphrase(event.target.value);
              }}
              type="password"
              className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
            />
          </div>
          <div className="flex items-end">
            <Button
              label="Import Wallet"
              onClick={() => importSigner()}
              disabled={!canImportSigner || importingSigner}
              loading={importingSigner}
              className="h-10 w-full bg-finnieTeal-100 text-finnieBlue-dark"
            />
          </div>
        </div>
      )}

      <div className="min-h-[28px]">
        <ErrorMessage
          error={
            createSignerError
              ? `QSDM wallet creation failed: ${getErrorMessage(
                  createSignerError
                )}`
              : null
          }
          className="py-2"
        />
        {createMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light">
            {createMessage}
          </div>
        )}
        <ErrorMessage
          error={
            restoreSignerError
              ? `QSDM wallet restore failed: ${getErrorMessage(
                  restoreSignerError
                )}`
              : null
          }
          className="py-2"
        />
        {restoreMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light">
            {restoreMessage}
          </div>
        )}
        <ErrorMessage
          error={
            backupSignerError
              ? `QSDM wallet backup failed: ${getErrorMessage(
                  backupSignerError
                )}`
              : null
          }
          className="py-2"
        />
        {backupMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light">
            {backupMessage}
          </div>
        )}
        <ErrorMessage
          error={
            exportRecoveryWordsError
              ? `QSDM recovery export failed: ${getErrorMessage(
                  exportRecoveryWordsError
                )}`
              : null
          }
          className="py-2"
        />
        {recoveryExportMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light">
            {recoveryExportMessage}
          </div>
        )}
        <ErrorMessage
          error={
            enableLegacyRecoveryError
              ? `QSDM recovery activation failed: ${getErrorMessage(
                  enableLegacyRecoveryError
                )}`
              : null
          }
          className="py-2"
        />
        {legacyRecoveryMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light">
            {legacyRecoveryMessage}
          </div>
        )}
        <ErrorMessage
          error={
            importSignerError
              ? `QSDM wallet import failed: ${getErrorMessage(
                  importSignerError
                )}`
              : null
          }
          className="py-2"
        />
        {importMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light">
            {importMessage}
          </div>
        )}
        <ErrorMessage
          error={
            unlockSignerError
              ? `QSDM wallet unlock failed: ${getErrorMessage(
                  unlockSignerError
                )}`
              : null
          }
          className="py-2"
        />
        {unlockMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light">
            {unlockMessage}
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 gap-3 pt-5 md:grid-cols-[1fr_180px_140px]">
        <div>
          <label
            className="text-xs text-finnieGray-secondary"
            htmlFor="qsdm-recipient"
          >
            Destination
          </label>
          <input
            id="qsdm-recipient"
            value={recipient}
            onChange={handleRecipientChange}
            className={`mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none ${
              sendError ? 'ring-1 ring-finnieRed' : ''
            }`}
            placeholder="QSDM wallet address"
          />
        </div>
        <div>
          <label
            className="text-xs text-finnieGray-secondary"
            htmlFor="qsdm-amount"
          >
            Amount
          </label>
          <input
            id="qsdm-amount"
            value={amount}
            onChange={handleAmountChange}
            type="number"
            min="0"
            step="0.000000001"
            className="mt-1 h-10 w-full rounded-md bg-finnieBlue-light-tertiary px-3 text-white outline-none"
            placeholder="0.000"
          />
        </div>
        <div className="flex items-end">
          <Button
            label="Send CELL"
            onClick={() => sendCell()}
            disabled={!canSend || sending}
            loading={sending}
            className="h-10 w-full bg-finnieTeal-100 text-finnieBlue-dark"
          />
        </div>
      </div>

      <div className="min-h-[28px]">
        <ErrorMessage error={sendError || null} className="py-2" />
        <ErrorMessage
          error={
            transferError
              ? `CELL transfer failed: ${getErrorMessage(transferError)}`
              : null
          }
          className="py-2"
        />
        {transferMessage && (
          <div className="py-2 text-sm text-finnieEmerald-light break-all">
            {transferMessage}
          </div>
        )}
        <ErrorMessage
          error={
            coreStatusError || cellAccountError
              ? `QSDM wallet status failed: ${getErrorMessage(
                  coreStatusError || cellAccountError
                )}`
              : null
          }
          className="py-2"
        />
      </div>

      {!coreStatusLoading && !signer?.ready && (
        <div className="text-xs text-finnieOrange">
          {signer?.reason ||
            'Configure the QSDM local signer before sending CELL.'}
        </div>
      )}
      <div className="pt-2 text-xs text-finnieGray-secondary">
        Core (
        {coreStatus?.coreConnectionMode ||
          QSDM_BRIDGE_CONFIG.coreConnectionMode}
        ): {coreStatus?.effectiveCoreApiUrl || QSDM_BRIDGE_CONFIG.coreApiUrl}
      </div>
    </section>
  );
}
