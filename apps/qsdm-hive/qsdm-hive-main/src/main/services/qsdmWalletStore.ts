/**
 * QSDM wallet store: several wallets in one Hive, one of them active.
 *
 * The ACTIVE wallet keeps living in the signer slot (hive-signer/wallet.json plus its passphrase entry), so the miner,
 * the pool miner, the wallet provider and transfers keep reading the same paths. Every wallet also has its own folder
 * under hive-signer/wallets/<address>/ with its keystore and its OS-protected passphrase entry, and
 * hive-signer/wallets/index.json lists them with a label and the active address.
 *
 * Rules:
 * - adding (create, import, restore with 24 words) never replaces or deletes a wallet;
 * - switching saves the active slot into its folder first, so nothing can be lost;
 * - "forget" moves a wallet's folder into wallets/.trash; no wallet file is ever deleted;
 * - wallet.json.bak-<time> files left by older Hive versions are offered as recovered wallets.
 * Key material is never logged or returned: only addresses, labels and file names.
 */
import fs from 'fs';
import path from 'path';

import {
  activateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths,
} from './qsdmTaskActionSigner';

export type QsdmStoredWalletSource =
  | 'created'
  | 'imported'
  | 'restored'
  | 'migrated'
  | 'recovered';

export interface QsdmStoredWallet {
  address: string;
  label: string;
  addedAt: string;
  source: QsdmStoredWalletSource;
}

export interface QsdmRecoverableWallet {
  address: string;
  backupFile: string;
  backedUpAt: string;
  hasPassphraseBackup: boolean;
}

export interface QsdmWalletList {
  active: string;
  wallets: QsdmStoredWallet[];
  recoverable: QsdmRecoverableWallet[];
}

interface WalletIndex {
  version: 1;
  active: string;
  wallets: QsdmStoredWallet[];
}

const STORE_DIR = 'wallets';
const INDEX_FILE = 'index.json';
const TRASH_DIR = '.trash';
const KEYSTORE = 'wallet.json';
const PASSPHRASE_FILES = ['passphrase.safe', 'passphrase.txt'];
const ADDRESS = /^[0-9a-f]{64}$/;
const BACKUP_NAME = /^wallet\.json\.bak-(\d{10,})$/;
const PASSPHRASE_BACKUP_NAME = /^(passphrase\.(?:safe|txt))\.bak-(\d{10,})$/;
const PASSPHRASE_BACKUP_WINDOW_MS = 60_000;
const MAX_LABEL = 40;

export class QsdmWalletStoreError extends Error {
  code: string;

  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

const paths = () => {
  const { signerDir, keystorePath } = getQsdmDefaultLocalSignerPaths();
  const storeDir = path.join(signerDir, STORE_DIR);
  return {
    signerDir,
    activeKeystore: keystorePath,
    storeDir,
    indexPath: path.join(storeDir, INDEX_FILE),
    trashDir: path.join(storeDir, TRASH_DIR),
  };
};

const normalizeAddress = (value?: string | null) =>
  (value || '').trim().toLowerCase();

export const readQsdmKeystoreAddress = (keystorePath: string) => {
  try {
    const wallet = JSON.parse(fs.readFileSync(keystorePath, 'utf-8')) as {
      address?: string;
    };
    const address = normalizeAddress(wallet.address);
    return ADDRESS.test(address) ? address : '';
  } catch {
    return '';
  }
};

const ensureDirPrivate = (dir: string) => {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
};

// Copy through a temporary file and a rename, so a failed write never leaves a half file in place.
const copyPrivateAtomic = (source: string, destination: string) => {
  const tmp = `${destination}.tmp-${process.pid}-${Date.now()}`;
  fs.copyFileSync(source, tmp);
  try {
    fs.chmodSync(tmp, 0o600);
  } catch {
    // Windows protects files through the user profile ACL.
  }
  fs.renameSync(tmp, destination);
};

const writeJsonAtomic = (file: string, value: unknown) => {
  const tmp = `${file}.tmp-${process.pid}-${Date.now()}`;
  fs.writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, {
    mode: 0o600,
  });
  fs.renameSync(tmp, file);
};

export const getQsdmWalletDir = (address: string) =>
  path.join(paths().storeDir, normalizeAddress(address));

const readIndex = (): WalletIndex | undefined => {
  try {
    const raw = JSON.parse(fs.readFileSync(paths().indexPath, 'utf-8')) as
      | WalletIndex
      | undefined;
    if (!raw || !Array.isArray(raw.wallets)) return undefined;
    return {
      version: 1,
      active: normalizeAddress(raw.active),
      wallets: raw.wallets
        .filter((w) => ADDRESS.test(normalizeAddress(w?.address)))
        .map((w) => ({
          address: normalizeAddress(w.address),
          label: String(w.label || '').slice(0, MAX_LABEL) || 'Wallet',
          addedAt: String(w.addedAt || ''),
          source: w.source || 'imported',
        })),
    };
  } catch {
    return undefined;
  }
};

const writeIndex = (index: WalletIndex) => {
  ensureDirPrivate(paths().storeDir);
  writeJsonAtomic(paths().indexPath, index);
};

// Copies the active slot (keystore + passphrase entry) into its own wallet folder. Called before every switch, so the
// active wallet always has a complete copy in the store.
const saveActiveSlotIntoStore = () => {
  const { signerDir, activeKeystore } = paths();
  const address = readQsdmKeystoreAddress(activeKeystore);
  if (!address) return '';
  const dir = getQsdmWalletDir(address);
  ensureDirPrivate(dir);
  copyPrivateAtomic(activeKeystore, path.join(dir, KEYSTORE));
  for (const name of PASSPHRASE_FILES) {
    const source = path.join(signerDir, name);
    if (fs.existsSync(source)) copyPrivateAtomic(source, path.join(dir, name));
  }
  return address;
};

const defaultLabel = (source: QsdmStoredWalletSource, count: number) => {
  if (source === 'migrated') return 'Main wallet';
  if (source === 'recovered') return 'Recovered wallet';
  return count === 0 ? 'Main wallet' : `Wallet ${count + 1}`;
};

const cleanLabel = (label: string | undefined, fallback: string) => {
  const trimmed = (label || '').replace(/\s+/g, ' ').trim();
  return (trimmed || fallback).slice(0, MAX_LABEL);
};

/**
 * Creates the store on first use (Hive 1.4.25 and later) from the single signer wallet of older versions, without
 * changing which wallet is active, and adds the active wallet to the list if an older flow wrote it directly.
 */
export const ensureQsdmWalletStore = (): WalletIndex => {
  const { activeKeystore } = paths();
  let index = readIndex();
  const activeAddress = readQsdmKeystoreAddress(activeKeystore);
  if (!index) {
    index = { version: 1, active: '', wallets: [] };
    if (activeAddress) {
      saveActiveSlotIntoStore();
      index.wallets.push({
        address: activeAddress,
        label: defaultLabel('migrated', 0),
        addedAt: new Date().toISOString(),
        source: 'migrated',
      });
      index.active = activeAddress;
    }
    writeIndex(index);
    return index;
  }
  if (
    activeAddress &&
    !index.wallets.some((w) => w.address === activeAddress)
  ) {
    saveActiveSlotIntoStore();
    index.wallets.push({
      address: activeAddress,
      label: defaultLabel('migrated', index.wallets.length),
      addedAt: new Date().toISOString(),
      source: 'migrated',
    });
    index.active = activeAddress;
    writeIndex(index);
  } else if (activeAddress && index.active !== activeAddress) {
    index.active = activeAddress;
    writeIndex(index);
  }
  return index;
};

const findPassphraseBackup = (signerDir: string, backupTime: number) => {
  let best: { file: string; name: string; diff: number } | undefined;
  fs.readdirSync(signerDir).forEach((entry) => {
    const match = PASSPHRASE_BACKUP_NAME.exec(entry);
    if (!match) return;
    const diff = Math.abs(Number(match[2]) - backupTime);
    if (diff <= PASSPHRASE_BACKUP_WINDOW_MS && (!best || diff < best.diff)) {
      best = { file: path.join(signerDir, entry), name: match[1], diff };
    }
  });
  return best;
};

const listRecoverable = (index: WalletIndex): QsdmRecoverableWallet[] => {
  const { signerDir } = paths();
  if (!fs.existsSync(signerDir)) return [];
  const known = new Set(index.wallets.map((w) => w.address));
  const newest = new Map<string, QsdmRecoverableWallet & { time: number }>();
  fs.readdirSync(signerDir).forEach((entry) => {
    const match = BACKUP_NAME.exec(entry);
    if (!match) return;
    const address = readQsdmKeystoreAddress(path.join(signerDir, entry));
    if (!address || known.has(address)) return;
    const time = Number(match[1]);
    const previous = newest.get(address);
    if (previous && previous.time >= time) return;
    newest.set(address, {
      address,
      backupFile: entry,
      backedUpAt: new Date(time).toISOString(),
      hasPassphraseBackup: !!findPassphraseBackup(signerDir, time),
      time,
    });
  });
  return [...newest.values()]
    .sort((a, b) => b.time - a.time)
    .map(({ address, backupFile, backedUpAt, hasPassphraseBackup }) => ({
      address,
      backupFile,
      backedUpAt,
      hasPassphraseBackup,
    }));
};

export const listQsdmWallets = (): QsdmWalletList => {
  const index = ensureQsdmWalletStore();
  return {
    active: index.active,
    wallets: index.wallets,
    recoverable: listRecoverable(index),
  };
};

/**
 * Adds a wallet whose keystore is at keystorePath (already validated by the caller) to the store. It never touches the
 * active slot; the caller activates it when asked. persistPassphrase writes the OS-protected passphrase entry into the
 * wallet's own folder (the same secret store Hive uses for the active wallet).
 */
export const addQsdmWalletToStore = ({
  keystorePath,
  label,
  source,
  persistPassphrase,
}: {
  keystorePath: string;
  label?: string;
  source: QsdmStoredWalletSource;
  persistPassphrase: (walletDir: string) => void;
}) => {
  const index = ensureQsdmWalletStore();
  const address = readQsdmKeystoreAddress(keystorePath);
  if (!address) {
    throw new QsdmWalletStoreError(
      'BAD_KEYSTORE',
      'The keystore has no valid QSDM address'
    );
  }
  if (index.wallets.some((w) => w.address === address)) {
    throw new QsdmWalletStoreError(
      'ALREADY_ADDED',
      `This wallet (${address.slice(0, 8)}…${address.slice(
        -8
      )}) is already in Hive`
    );
  }
  const dir = getQsdmWalletDir(address);
  ensureDirPrivate(dir);
  copyPrivateAtomic(keystorePath, path.join(dir, KEYSTORE));
  persistPassphrase(dir);
  index.wallets.push({
    address,
    label: cleanLabel(label, defaultLabel(source, index.wallets.length)),
    addedAt: new Date().toISOString(),
    source,
  });
  writeIndex(index);
  return { address, walletDir: dir, hadActiveWallet: !!index.active };
};

/**
 * Restoring the 24 words of a wallet that is already in Hive (for example after a forgotten passphrase) refreshes that
 * wallet's keystore and passphrase entry. The previous files stay beside them as .bak-<time>. Same address, so nothing
 * of another wallet is touched.
 */
export const refreshQsdmStoredWallet = ({
  keystorePath,
  persistPassphrase,
}: {
  keystorePath: string;
  persistPassphrase: (walletDir: string) => void;
}) => {
  const index = ensureQsdmWalletStore();
  const address = readQsdmKeystoreAddress(keystorePath);
  if (!address || !index.wallets.some((w) => w.address === address)) {
    throw new QsdmWalletStoreError(
      'UNKNOWN_WALLET',
      'This wallet is not in Hive'
    );
  }
  const dir = getQsdmWalletDir(address);
  ensureDirPrivate(dir);
  const stamp = Date.now();
  for (const name of [KEYSTORE, ...PASSPHRASE_FILES]) {
    const file = path.join(dir, name);
    if (fs.existsSync(file)) fs.copyFileSync(file, `${file}.bak-${stamp}`);
  }
  copyPrivateAtomic(keystorePath, path.join(dir, KEYSTORE));
  for (const name of PASSPHRASE_FILES) {
    fs.rmSync(path.join(dir, name), { force: true });
  }
  persistPassphrase(dir);
  return { address, isActive: index.active === address };
};

/** After refreshQsdmStoredWallet of the ACTIVE wallet: copy its refreshed files into the active slot. */
export const reloadActiveQsdmWalletFromStore = () => {
  const index = ensureQsdmWalletStore();
  if (!index.active) return '';
  const { signerDir, activeKeystore } = paths();
  const dir = getQsdmWalletDir(index.active);
  copyPrivateAtomic(path.join(dir, KEYSTORE), activeKeystore);
  for (const name of PASSPHRASE_FILES) {
    const source = path.join(dir, name);
    const destination = path.join(signerDir, name);
    if (fs.existsSync(source)) copyPrivateAtomic(source, destination);
    else fs.rmSync(destination, { force: true });
  }
  return index.active;
};

export const isQsdmWalletInStore = (keystorePath: string) => {
  const address = readQsdmKeystoreAddress(keystorePath);
  return (
    !!address &&
    ensureQsdmWalletStore().wallets.some((w) => w.address === address)
  );
};

/**
 * Makes a stored wallet the active one. The current active slot is saved into its folder first; if anything fails
 * while the target is copied in, the previous active wallet is put back.
 */
export const activateQsdmStoredWallet = (address: string) => {
  const target = normalizeAddress(address);
  const index = ensureQsdmWalletStore();
  if (!index.wallets.some((w) => w.address === target)) {
    throw new QsdmWalletStoreError(
      'UNKNOWN_WALLET',
      'This wallet is not in Hive'
    );
  }
  const { signerDir, activeKeystore } = paths();
  const targetDir = getQsdmWalletDir(target);
  if (!fs.existsSync(path.join(targetDir, KEYSTORE))) {
    throw new QsdmWalletStoreError(
      'MISSING_FILES',
      'The wallet file of this wallet is missing'
    );
  }
  const previous = saveActiveSlotIntoStore();
  const putInPlace = (dir: string) => {
    ensureDirPrivate(signerDir);
    copyPrivateAtomic(path.join(dir, KEYSTORE), activeKeystore);
    for (const name of PASSPHRASE_FILES) {
      const source = path.join(dir, name);
      const destination = path.join(signerDir, name);
      if (fs.existsSync(source)) copyPrivateAtomic(source, destination);
      // The previous wallet's entry is already saved in its folder, so the slot can drop it.
      else fs.rmSync(destination, { force: true });
    }
  };
  try {
    putInPlace(targetDir);
    if (readQsdmKeystoreAddress(activeKeystore) !== target) {
      throw new Error('the active wallet file does not match after the copy');
    }
  } catch (error) {
    if (previous) putInPlace(getQsdmWalletDir(previous));
    throw new QsdmWalletStoreError(
      'SWITCH_FAILED',
      `Could not switch wallets; the previous wallet is still active (${
        (error as Error).message
      })`
    );
  }
  index.active = target;
  writeIndex(index);
  // Point the signer at the new wallet; the caller then loads its protected passphrase (or asks to unlock it).
  activateQsdmImportedSignerPaths({
    keystorePath: activeKeystore,
    passphraseFile: path.join(signerDir, 'passphrase.txt'),
    sender: target,
  });
  return { address: target, previous };
};

/** Keeps the store copy of the active wallet current after the user unlocks it (a new passphrase entry). */
export const syncActiveQsdmWalletIntoStore = () => {
  const index = readIndex();
  if (!index) return '';
  return saveActiveSlotIntoStore();
};

export const renameQsdmStoredWallet = (address: string, label: string) => {
  const target = normalizeAddress(address);
  const index = ensureQsdmWalletStore();
  const wallet = index.wallets.find((w) => w.address === target);
  if (!wallet) {
    throw new QsdmWalletStoreError(
      'UNKNOWN_WALLET',
      'This wallet is not in Hive'
    );
  }
  wallet.label = cleanLabel(label, wallet.label);
  writeIndex(index);
  return wallet;
};

/**
 * Removes a wallet from the list. confirmAddress must repeat the full address. The active wallet cannot be forgotten.
 * The wallet's folder is moved into wallets/.trash, never deleted.
 */
export const forgetQsdmStoredWallet = (
  address: string,
  confirmAddress: string
) => {
  const target = normalizeAddress(address);
  if (normalizeAddress(confirmAddress) !== target) {
    throw new QsdmWalletStoreError(
      'CONFIRM_MISMATCH',
      'The confirmation does not match the wallet address'
    );
  }
  const index = ensureQsdmWalletStore();
  if (index.active === target) {
    throw new QsdmWalletStoreError(
      'ACTIVE_WALLET',
      'Switch to another wallet before removing this one'
    );
  }
  if (!index.wallets.some((w) => w.address === target)) {
    throw new QsdmWalletStoreError(
      'UNKNOWN_WALLET',
      'This wallet is not in Hive'
    );
  }
  const { trashDir } = paths();
  ensureDirPrivate(trashDir);
  const dir = getQsdmWalletDir(target);
  const trashPath = path.join(trashDir, `${target}-${Date.now()}`);
  if (fs.existsSync(dir)) fs.renameSync(dir, trashPath);
  index.wallets = index.wallets.filter((w) => w.address !== target);
  writeIndex(index);
  return { address: target, trashPath };
};

/**
 * Adds a wallet that an older Hive replaced (a wallet.json.bak-<time> file in the signer folder) back to the list,
 * with its passphrase entry backup when one was made at the same moment. It does not switch to it.
 */
export const recoverQsdmBackupWallet = (backupFile: string, label?: string) => {
  const { signerDir } = paths();
  const name = path.basename(backupFile || '');
  const match = BACKUP_NAME.exec(name);
  if (!match || name !== backupFile) {
    throw new QsdmWalletStoreError(
      'BAD_BACKUP',
      'Not a wallet backup file of this Hive'
    );
  }
  const source = path.join(signerDir, name);
  if (!fs.existsSync(source)) {
    throw new QsdmWalletStoreError('BAD_BACKUP', 'The backup file is missing');
  }
  const passphraseBackup = findPassphraseBackup(signerDir, Number(match[1]));
  return addQsdmWalletToStore({
    keystorePath: source,
    label: label || 'Recovered wallet',
    source: 'recovered',
    persistPassphrase: (walletDir) => {
      if (passphraseBackup) {
        copyPrivateAtomic(
          passphraseBackup.file,
          path.join(walletDir, passphraseBackup.name)
        );
      }
    },
  });
};
