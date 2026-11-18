import React, { useState } from 'react';
import toast from 'react-hot-toast';
import { useMutation, useQuery, useQueryClient } from 'react-query';

import { Button, ErrorMessage, LoadingSpinner } from 'renderer/components/ui';
import {
  activateQsdmWallet,
  forgetQsdmWallet,
  listQsdmWallets,
  QueryKeys,
  recoverQsdmWallet,
  renameQsdmWallet,
} from 'renderer/services';

import type { QsdmStoredWallet } from 'models/api/qsdm';

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

const shortAddress = (address: string) =>
  address.length > 24
    ? `${address.slice(0, 10)}...${address.slice(-10)}`
    : address;

const formatDate = (value: string) => {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? '' : parsed.toLocaleDateString();
};

const SOURCE_LABELS: Record<QsdmStoredWallet['source'], string> = {
  created: 'Created in Hive',
  imported: 'Imported',
  restored: 'Restored with 24 words',
  migrated: 'Your first wallet',
  recovered: 'Recovered backup',
};

/** Every wallet Hive holds, which one is active, and the wallets older Hive versions replaced (recoverable). */
export function QsdmWalletList() {
  const queryClient = useQueryClient();
  const [renaming, setRenaming] = useState('');
  const [renameLabel, setRenameLabel] = useState('');
  const [forgetting, setForgetting] = useState('');
  const [forgetConfirm, setForgetConfirm] = useState('');

  const {
    data: list,
    isLoading,
    error: listError,
  } = useQuery([QueryKeys.QsdmWalletList], listQsdmWallets, {
    refetchInterval: 15000,
  });

  const refreshAll = async () => {
    await queryClient.invalidateQueries([QueryKeys.QsdmWalletList]);
    await queryClient.invalidateQueries([QueryKeys.QsdmCoreStatus]);
    await queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
    await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
    await queryClient.invalidateQueries([QueryKeys.MainAccountBalance]);
  };

  const {
    mutate: activate,
    isLoading: activating,
    error: activateError,
  } = useMutation((address: string) => activateQsdmWallet({ address }), {
    onSuccess: async (result) => {
      toast.success(
        result.unlocked
          ? `Switched to wallet ${shortAddress(result.address)}`
          : `Switched to wallet ${shortAddress(
              result.address
            )}. Unlock it below with its passphrase.`
      );
      await refreshAll();
    },
  });

  const {
    mutate: rename,
    isLoading: renamingWallet,
    error: renameError,
  } = useMutation(
    () => renameQsdmWallet({ address: renaming, label: renameLabel.trim() }),
    {
      onSuccess: async () => {
        setRenaming('');
        setRenameLabel('');
        await refreshAll();
      },
    }
  );

  const {
    mutate: forget,
    isLoading: forgettingWallet,
    error: forgetError,
  } = useMutation(
    () =>
      forgetQsdmWallet({
        address: forgetting,
        confirmAddress: forgetConfirm.trim(),
      }),
    {
      onSuccess: async (result) => {
        toast.success(
          `Removed ${shortAddress(
            result.address
          )} from Hive. Its files were moved to the wallets trash folder.`
        );
        setForgetting('');
        setForgetConfirm('');
        await refreshAll();
      },
    }
  );

  const {
    mutate: recover,
    isLoading: recovering,
    error: recoverError,
  } = useMutation(
    (backupFile: string) =>
      recoverQsdmWallet({ backupFile, label: 'Recovered wallet' }),
    {
      onSuccess: async (result) => {
        toast.success(
          `Added ${shortAddress(result.address)} back to your wallets.`
        );
        await refreshAll();
      },
    }
  );

  const actionError =
    activateError || renameError || forgetError || recoverError || listError;
  const busy = activating || renamingWallet || forgettingWallet || recovering;

  return (
    <div className="mt-5 border-t border-qsdm-border pt-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-base font-semibold">Wallets</div>
          <div className="pt-1 text-xs text-finnieGray-secondary">
            Hive keeps every wallet you create, import or restore. Mining, the
            Pool Miner, Transfer and connected sites use the active wallet.
          </div>
        </div>
        <span className="text-xs text-finnieGray-secondary">
          {list?.wallets.length || 0} in Hive
        </span>
      </div>

      {list?.minerRunning && (
        <div className="mt-3 rounded-lg border border-qsdm-warn/40 bg-qsdm-warn/5 p-3 text-xs text-finnieOrange">
          A miner task is running and signs with the active wallet. Stop the
          QSDM Miner or Pool Miner task before switching wallets.
        </div>
      )}

      <div className="pt-3">
        {isLoading ? (
          <LoadingSpinner />
        ) : list?.wallets.length ? (
          <div className="divide-y divide-qsdm-border">
            {list.wallets.map((wallet) => {
              const isActive = wallet.address === list.active;
              return (
                <div key={wallet.address} className="py-3">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex items-center gap-2 text-sm font-semibold">
                        <span className="truncate">{wallet.label}</span>
                        {isActive && (
                          <span className="rounded-full border border-qsdm-success/40 bg-qsdm-success/10 px-2 py-0.5 text-[11px] text-qsdm-success">
                            Active
                          </span>
                        )}
                      </div>
                      <div
                        className="pt-1 font-mono text-xs text-finnieGray-secondary"
                        title={wallet.address}
                      >
                        {shortAddress(wallet.address)}
                      </div>
                      <div className="pt-1 text-[11px] text-finnieGray-secondary">
                        {SOURCE_LABELS[wallet.source]}
                        {formatDate(wallet.addedAt)
                          ? ` | Added ${formatDate(wallet.addedAt)}`
                          : ''}
                      </div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {!isActive && (
                        <Button
                          label="Use This Wallet"
                          onClick={() => activate(wallet.address)}
                          disabled={busy || !!list.minerRunning}
                          loading={activating}
                          className="h-9 w-36 border-qsdm-gold/60 bg-qsdm-gold text-qsdm-bg hover:border-qsdm-gold hover:brightness-105"
                        />
                      )}
                      <Button
                        label="Rename"
                        onClick={() => {
                          setForgetting('');
                          setRenaming(wallet.address);
                          setRenameLabel(wallet.label);
                        }}
                        disabled={busy}
                        className="h-9 w-24"
                      />
                      {!isActive && (
                        <Button
                          label="Remove"
                          onClick={() => {
                            setRenaming('');
                            setForgetting(wallet.address);
                            setForgetConfirm('');
                          }}
                          disabled={busy}
                          className="h-9 w-24"
                        />
                      )}
                    </div>
                  </div>

                  {renaming === wallet.address && (
                    <div className="mt-3 flex flex-wrap items-end gap-2">
                      <input
                        value={renameLabel}
                        onChange={(event) => setRenameLabel(event.target.value)}
                        maxLength={40}
                        aria-label="Wallet name"
                        className="h-9 w-64 rounded-lg border border-qsdm-border bg-black/25 px-3 text-sm text-white outline-none transition focus:border-qsdm-teal"
                      />
                      <Button
                        label="Save"
                        onClick={() => rename()}
                        disabled={!renameLabel.trim() || renamingWallet}
                        loading={renamingWallet}
                        className="h-9 w-24"
                      />
                      <Button
                        label="Cancel"
                        onClick={() => setRenaming('')}
                        className="h-9 w-24"
                      />
                    </div>
                  )}

                  {forgetting === wallet.address && (
                    <div className="mt-3 rounded-lg border border-qsdm-warn/50 bg-qsdm-warn/5 p-4">
                      <div className="text-sm font-semibold">
                        Remove this wallet from Hive?
                      </div>
                      <div className="pt-2 break-all font-mono text-xs">
                        {wallet.address}
                      </div>
                      <div className="pt-2 text-xs text-finnieOrange">
                        Hive moves its files to the wallets trash folder; it
                        deletes nothing. Keep this wallet&apos;s 24 Recovery
                        Words (or its JSON backup and passphrase): you need them
                        to add it back. Its CELL stays on the network.
                      </div>
                      <label
                        className="block pt-3 text-xs text-finnieGray-secondary"
                        htmlFor={`qsdm-forget-${wallet.address}`}
                      >
                        Type or paste the full address to confirm
                      </label>
                      <input
                        id={`qsdm-forget-${wallet.address}`}
                        value={forgetConfirm}
                        onChange={(event) =>
                          setForgetConfirm(event.target.value)
                        }
                        autoComplete="off"
                        spellCheck={false}
                        className="mt-1 h-9 w-full rounded-lg border border-qsdm-border bg-black/25 px-3 font-mono text-xs text-white outline-none transition focus:border-qsdm-teal"
                      />
                      <div className="flex gap-2 pt-3">
                        <Button
                          label="Remove Wallet"
                          onClick={() => forget()}
                          disabled={
                            forgetConfirm.trim().toLowerCase() !==
                              wallet.address.toLowerCase() || forgettingWallet
                          }
                          loading={forgettingWallet}
                          className="h-9 w-36"
                        />
                        <Button
                          label="Cancel"
                          onClick={() => setForgetting('')}
                          className="h-9 w-24"
                        />
                      </div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        ) : (
          <div className="text-sm text-finnieGray-secondary">
            No wallet yet. Create, restore or import one below.
          </div>
        )}
      </div>

      {!!list?.recoverable.length && (
        <div className="mt-4 rounded-lg border border-qsdm-teal/40 bg-qsdm-teal/5 p-4">
          <div className="text-sm font-semibold">Recovered wallets</div>
          <div className="pt-1 text-xs text-finnieGray-secondary">
            An older Hive version set these wallets aside when another wallet
            was imported or restored. Add them back to your wallets; nothing
            else changes.
          </div>
          <div className="divide-y divide-qsdm-border pt-2">
            {list.recoverable.map((backup) => (
              <div
                key={backup.backupFile}
                className="flex flex-wrap items-center justify-between gap-3 py-2"
              >
                <div className="min-w-0">
                  <div className="font-mono text-xs" title={backup.address}>
                    {shortAddress(backup.address)}
                  </div>
                  <div className="pt-1 text-[11px] text-finnieGray-secondary">
                    Set aside {formatDate(backup.backedUpAt)}
                    {backup.hasPassphraseBackup
                      ? ''
                      : ' | You will need its passphrase to unlock it'}
                  </div>
                </div>
                <Button
                  label="Add Back"
                  onClick={() => recover(backup.backupFile)}
                  disabled={busy}
                  loading={recovering}
                  className="h-9 w-28 border-qsdm-gold/60 bg-qsdm-gold text-qsdm-bg hover:border-qsdm-gold hover:brightness-105"
                />
              </div>
            ))}
          </div>
        </div>
      )}

      <ErrorMessage
        error={actionError ? `Wallets: ${getErrorMessage(actionError)}` : null}
        className="py-2"
      />
    </div>
  );
}
