import { Event } from 'electron';
import path from 'path';

import { isQsdmMinerSystemTaskId } from 'config/qsdmSystemTasks';
import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import { initializeQsdmSignerSecretStore } from 'main/services/qsdmSignerSecretStore';
import { getQsdmDefaultLocalSignerPaths } from 'main/services/qsdmTaskActionSigner';
import {
  activateQsdmStoredWallet,
  forgetQsdmStoredWallet,
  getQsdmWalletDir,
  listQsdmWallets,
  QsdmWalletStoreError,
  recoverQsdmBackupWallet,
  renameQsdmStoredWallet,
} from 'main/services/qsdmWalletStore';
import {
  QsdmWalletActivateRequest,
  QsdmWalletActivateResponse,
  QsdmWalletForgetRequest,
  QsdmWalletListResponse,
  QsdmWalletRecoverRequest,
  QsdmWalletRenameRequest,
} from 'models/api/qsdm';

// A miner task signs its proofs with the active wallet, so the active wallet changes only while no miner runs.
export const getRunningQsdmMinerTaskIds = () =>
  Object.keys(qsdmHiveTasks.RUNNING_TASKS || {}).filter((taskId) =>
    isQsdmMinerSystemTaskId(taskId)
  );

const assertNoMinerRunning = () => {
  if (getRunningQsdmMinerTaskIds().length > 0) {
    throw new QsdmWalletStoreError(
      'MINER_RUNNING',
      'Stop the QSDM Miner or QSDM Pool Miner task before switching wallets: it signs with the active wallet.'
    );
  }
};

/** Create, import and restore call this first: asking to switch while a miner runs is refused before anything is written. */
export const assertQsdmWalletSwitchAllowed = (makeActive?: boolean) => {
  if (makeActive) assertNoMinerRunning();
};

const switchTo = (address: string) => {
  assertNoMinerRunning();
  const switched = activateQsdmStoredWallet(address);
  const secrets = initializeQsdmSignerSecretStore();
  return { ...switched, unlocked: !!secrets.configured, secrets };
};

/** After a create/import/restore added a wallet: make it active when Hive had none, or when the user asked to. */
export const activateAddedQsdmWallet = async ({
  address,
  makeActive,
  hadActiveWallet,
}: {
  address: string;
  makeActive?: boolean;
  hadActiveWallet: boolean;
}) => {
  if (hadActiveWallet && !makeActive) {
    return {
      active: false,
      keystorePath: path.join(getQsdmWalletDir(address), 'wallet.json'),
      passphraseFile: '',
    };
  }
  const switched = switchTo(address);
  return {
    active: true,
    keystorePath: getQsdmDefaultLocalSignerPaths().keystorePath,
    passphraseFile:
      (switched.secrets as { passphraseFile?: string }).passphraseFile || '',
  };
};

export const listQsdmWalletsController =
  async (): Promise<QsdmWalletListResponse> => ({
    ...listQsdmWallets(),
    minerRunning: getRunningQsdmMinerTaskIds().length > 0,
  });

export const activateQsdmWalletController = async (
  _: Event,
  payload: QsdmWalletActivateRequest
): Promise<QsdmWalletActivateResponse> => {
  const switched = switchTo(payload.address);
  return { address: switched.address, unlocked: switched.unlocked };
};

export const renameQsdmWalletController = async (
  _: Event,
  payload: QsdmWalletRenameRequest
) => renameQsdmStoredWallet(payload.address, payload.label);

export const forgetQsdmWalletController = async (
  _: Event,
  payload: QsdmWalletForgetRequest
) => {
  const forgotten = forgetQsdmStoredWallet(
    payload.address,
    payload.confirmAddress
  );
  return { address: forgotten.address };
};

export const recoverQsdmWalletController = async (
  _: Event,
  payload: QsdmWalletRecoverRequest
) => {
  const recovered = recoverQsdmBackupWallet(payload.backupFile, payload.label);
  return { address: recovered.address };
};
