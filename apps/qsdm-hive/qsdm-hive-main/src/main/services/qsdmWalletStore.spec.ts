/**
 * @jest-environment node
 */

import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  activateQsdmStoredWallet,
  addQsdmWalletToStore,
  ensureQsdmWalletStore,
  forgetQsdmStoredWallet,
  listQsdmWallets,
  recoverQsdmBackupWallet,
  refreshQsdmStoredWallet,
  reloadActiveQsdmWalletFromStore,
  renameQsdmStoredWallet,
  syncActiveQsdmWalletIntoStore,
} from './qsdmWalletStore';

let mockSignerDir = '';

// The real activateQsdmImportedSignerPaths runs, so the tests see the signer environment the miner and transfers read.
jest.mock('main/services/qsdmTaskActionSigner', () => {
  const actual = jest.requireActual('main/services/qsdmTaskActionSigner');
  const pathModule = jest.requireActual<typeof import('path')>('path');
  return {
    ...actual,
    getQsdmDefaultLocalSignerPaths: () => ({
      signerDir: mockSignerDir,
      keystorePath: pathModule.join(mockSignerDir, 'wallet.json'),
      passphraseFile: pathModule.join(mockSignerDir, 'passphrase.txt'),
    }),
  };
});

const A = 'a'.repeat(56) + '13d78670';
const B = 'b8fcf6a1' + 'b'.repeat(48) + 'eb525dff';
const C = 'c'.repeat(64);

const keystore = (address: string) =>
  JSON.stringify({
    type: 'qsdm-keystore',
    address,
    public_key: `pk-${address.slice(0, 6)}`,
    cipher: `cipher-of-${address.slice(0, 6)}`,
  });

const slot = (name: string) => path.join(mockSignerDir, name);
const walletFile = (address: string, name: string) =>
  path.join(mockSignerDir, 'wallets', address, name);
const read = (file: string) => fs.readFileSync(file, 'utf-8');

const writeKeystoreFile = (address: string) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-incoming-'));
  const file = path.join(dir, 'wallet.json');
  fs.writeFileSync(file, keystore(address));
  return file;
};

const persistAs =
  (secret: string) =>
  (walletDir: string): void => {
    fs.writeFileSync(path.join(walletDir, 'passphrase.safe'), secret);
  };

const ENV_KEYS = [
  'QSDM_TASK_ACTION_KEYSTORE_PATH',
  'QSDM_TASK_ACTION_PASSPHRASE_FILE',
  'QSDM_TASK_ACTION_SIGNER',
  'QSDM_TASK_ACTION_SENDER',
];
const savedEnv: Record<string, string | undefined> = {};

describe('qsdmWalletStore', () => {
  beforeEach(() => {
    mockSignerDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-wallets-'));
    ENV_KEYS.forEach((key) => {
      savedEnv[key] = process.env[key];
      delete process.env[key];
    });
  });

  afterEach(() => {
    jest.restoreAllMocks();
    fs.rmSync(mockSignerDir, { recursive: true, force: true });
    ENV_KEYS.forEach((key) => {
      if (savedEnv[key] === undefined) delete process.env[key];
      else process.env[key] = savedEnv[key];
    });
  });

  const withActiveWallet = (address: string, secret: string) => {
    fs.writeFileSync(slot('wallet.json'), keystore(address));
    fs.writeFileSync(slot('passphrase.safe'), secret);
  };

  describe('first start of 1.4.25 (migration)', () => {
    it('copies the single wallet into the store and keeps it active and untouched', () => {
      withActiveWallet(A, 'dpapi-A');

      const index = ensureQsdmWalletStore();

      expect(index.active).toBe(A);
      expect(index.wallets).toEqual([
        expect.objectContaining({
          address: A,
          label: 'Main wallet',
          source: 'migrated',
        }),
      ]);
      expect(read(walletFile(A, 'wallet.json'))).toBe(keystore(A));
      expect(read(walletFile(A, 'passphrase.safe'))).toBe('dpapi-A');
      // The active slot is exactly as before.
      expect(read(slot('wallet.json'))).toBe(keystore(A));
      expect(read(slot('passphrase.safe'))).toBe('dpapi-A');
    });

    it('starts an empty store when Hive has no wallet yet', () => {
      expect(ensureQsdmWalletStore()).toEqual({
        version: 1,
        active: '',
        wallets: [],
      });
    });

    it('is idempotent and adds a wallet that an older flow wrote straight into the slot', () => {
      withActiveWallet(A, 'dpapi-A');
      ensureQsdmWalletStore();
      ensureQsdmWalletStore();
      expect(listQsdmWallets().wallets).toHaveLength(1);

      withActiveWallet(C, 'dpapi-C');
      const index = ensureQsdmWalletStore();
      expect(index.active).toBe(C);
      expect(index.wallets.map((w) => w.address)).toEqual([A, C]);
    });

    it('offers wallet.json.bak-* files that older versions left as recovered wallets', () => {
      // The owner's case: B was imported over A, and 1.4.24 kept A as a .bak file.
      withActiveWallet(B, 'dpapi-B');
      fs.writeFileSync(slot('wallet.json.bak-1795029829858'), keystore(A));
      fs.writeFileSync(slot('passphrase.safe.bak-1795029829860'), 'dpapi-A');
      // An older backup of the same address, a backup of a wallet already in Hive, and junk.
      fs.writeFileSync(slot('wallet.json.bak-1700000000000'), keystore(A));
      fs.writeFileSync(slot('wallet.json.bak-1795029829999'), keystore(B));
      fs.writeFileSync(slot('wallet.json.bak-1795029830000'), '{not json');

      const list = listQsdmWallets();

      expect(list.active).toBe(B);
      expect(list.wallets.map((w) => w.address)).toEqual([B]);
      expect(list.recoverable).toEqual([
        {
          address: A,
          backupFile: 'wallet.json.bak-1795029829858',
          backedUpAt: new Date(1795029829858).toISOString(),
          hasPassphraseBackup: true,
        },
      ]);
    });
  });

  describe('adding a wallet', () => {
    it('adds beside the active wallet and never replaces it', () => {
      withActiveWallet(A, 'dpapi-A');
      const incoming = writeKeystoreFile(B);

      const added = addQsdmWalletToStore({
        keystorePath: incoming,
        label: 'Ops',
        source: 'imported',
        persistPassphrase: persistAs('dpapi-B'),
      });

      expect(added).toEqual(
        expect.objectContaining({ address: B, hadActiveWallet: true })
      );
      expect(read(slot('wallet.json'))).toBe(keystore(A));
      expect(read(slot('passphrase.safe'))).toBe('dpapi-A');
      expect(read(walletFile(B, 'wallet.json'))).toBe(keystore(B));
      expect(read(walletFile(B, 'passphrase.safe'))).toBe('dpapi-B');
      const list = listQsdmWallets();
      expect(list.active).toBe(A);
      expect(list.wallets.map((w) => [w.address, w.label, w.source])).toEqual([
        [A, 'Main wallet', 'migrated'],
        [B, 'Ops', 'imported'],
      ]);
    });

    it('refuses a second copy of the same wallet', () => {
      withActiveWallet(A, 'dpapi-A');
      expect(() =>
        addQsdmWalletToStore({
          keystorePath: writeKeystoreFile(A),
          source: 'imported',
          persistPassphrase: persistAs('other'),
        })
      ).toThrow('already in Hive');
      expect(read(walletFile(A, 'passphrase.safe'))).toBe('dpapi-A');
    });

    it('reports no active wallet for the first wallet of a fresh Hive', () => {
      const added = addQsdmWalletToStore({
        keystorePath: writeKeystoreFile(B),
        source: 'created',
        persistPassphrase: persistAs('dpapi-B'),
      });
      expect(added.hadActiveWallet).toBe(false);
      expect(listQsdmWallets().wallets[0].label).toBe('Main wallet');
    });

    it('loses nothing when writing the passphrase entry fails, and the retry works', () => {
      withActiveWallet(A, 'dpapi-A');
      const incoming = writeKeystoreFile(B);

      expect(() =>
        addQsdmWalletToStore({
          keystorePath: incoming,
          source: 'imported',
          persistPassphrase: () => {
            throw new Error('safeStorage failed');
          },
        })
      ).toThrow('safeStorage failed');
      expect(listQsdmWallets().wallets.map((w) => w.address)).toEqual([A]);
      expect(read(slot('wallet.json'))).toBe(keystore(A));

      addQsdmWalletToStore({
        keystorePath: incoming,
        source: 'imported',
        persistPassphrase: persistAs('dpapi-B'),
      });
      expect(listQsdmWallets().wallets.map((w) => w.address)).toEqual([A, B]);
    });
  });

  describe('switching the active wallet', () => {
    beforeEach(() => {
      withActiveWallet(A, 'dpapi-A');
      addQsdmWalletToStore({
        keystorePath: writeKeystoreFile(B),
        source: 'imported',
        persistPassphrase: persistAs('dpapi-B'),
      });
    });

    it('puts the chosen wallet in the signer slot and points the signer at it', () => {
      const result = activateQsdmStoredWallet(B);

      expect(result).toEqual({ address: B, previous: A });
      expect(read(slot('wallet.json'))).toBe(keystore(B));
      expect(read(slot('passphrase.safe'))).toBe('dpapi-B');
      expect(listQsdmWallets().active).toBe(B);
      // What mining, proofs, the pool miner, the extension and transfers read:
      expect(process.env.QSDM_TASK_ACTION_KEYSTORE_PATH).toBe(
        slot('wallet.json')
      );
      expect(process.env.QSDM_TASK_ACTION_SENDER).toBe(B);
      expect(process.env.QSDM_TASK_ACTION_SIGNER).toBe('cli');

      // and back, with A's own passphrase entry
      activateQsdmStoredWallet(A);
      expect(read(slot('wallet.json'))).toBe(keystore(A));
      expect(read(slot('passphrase.safe'))).toBe('dpapi-A');
      expect(process.env.QSDM_TASK_ACTION_SENDER).toBe(A);
    });

    it('saves a passphrase entry the user renewed on the active wallet before switching away', () => {
      fs.writeFileSync(slot('passphrase.safe'), 'dpapi-A-renewed');
      activateQsdmStoredWallet(B);
      expect(read(walletFile(A, 'passphrase.safe'))).toBe('dpapi-A-renewed');
    });

    it('drops a legacy passphrase.txt of the previous wallet from the slot', () => {
      fs.writeFileSync(slot('passphrase.txt'), 'plain-A');
      activateQsdmStoredWallet(B);
      expect(fs.existsSync(slot('passphrase.txt'))).toBe(false);
      expect(read(walletFile(A, 'passphrase.txt'))).toBe('plain-A');
    });

    it('rolls back to the previous wallet when the copy fails midway', () => {
      const realRename = fs.renameSync;
      let failed = false;
      jest
        .spyOn(fs, 'renameSync')
        .mockImplementation((from: fs.PathLike, to: fs.PathLike) => {
          if (!failed && String(to) === slot('passphrase.safe')) {
            failed = true;
            throw new Error('disk full');
          }
          return realRename(from, to);
        });

      expect(() => activateQsdmStoredWallet(B)).toThrow(
        'previous wallet is still active'
      );
      expect(read(slot('wallet.json'))).toBe(keystore(A));
      expect(read(slot('passphrase.safe'))).toBe('dpapi-A');
      expect(listQsdmWallets().active).toBe(A);
      expect(read(walletFile(B, 'wallet.json'))).toBe(keystore(B));
    });

    it('rolls back when the stored file holds a different wallet', () => {
      fs.writeFileSync(walletFile(B, 'wallet.json'), keystore(C));
      expect(() => activateQsdmStoredWallet(B)).toThrow(
        'previous wallet is still active'
      );
      expect(read(slot('wallet.json'))).toBe(keystore(A));
      expect(listQsdmWallets().active).toBe(A);
    });

    it('refuses an unknown wallet', () => {
      expect(() => activateQsdmStoredWallet(C)).toThrow('not in Hive');
      expect(read(slot('wallet.json'))).toBe(keystore(A));
    });

    it('keeps the store copy current after an unlock', () => {
      fs.writeFileSync(slot('passphrase.safe'), 'dpapi-A-unlocked');
      syncActiveQsdmWalletIntoStore();
      expect(read(walletFile(A, 'passphrase.safe'))).toBe('dpapi-A-unlocked');
    });
  });

  describe('restoring a wallet that is already in Hive', () => {
    it('refreshes only that wallet and keeps its previous files as .bak', () => {
      withActiveWallet(A, 'dpapi-A');
      addQsdmWalletToStore({
        keystorePath: writeKeystoreFile(B),
        source: 'imported',
        persistPassphrase: persistAs('dpapi-B-old'),
      });

      const refreshed = refreshQsdmStoredWallet({
        keystorePath: writeKeystoreFile(B),
        persistPassphrase: persistAs('dpapi-B-new'),
      });

      expect(refreshed).toEqual({ address: B, isActive: false });
      expect(read(walletFile(B, 'passphrase.safe'))).toBe('dpapi-B-new');
      const backups = fs
        .readdirSync(path.join(mockSignerDir, 'wallets', B))
        .filter((name) => name.startsWith('passphrase.safe.bak-'));
      expect(backups).toHaveLength(1);
      expect(read(slot('passphrase.safe'))).toBe('dpapi-A');
    });

    it('reloads the active slot after refreshing the active wallet', () => {
      withActiveWallet(A, 'dpapi-A-old');
      ensureQsdmWalletStore();
      const refreshed = refreshQsdmStoredWallet({
        keystorePath: writeKeystoreFile(A),
        persistPassphrase: persistAs('dpapi-A-new'),
      });
      expect(refreshed.isActive).toBe(true);
      reloadActiveQsdmWalletFromStore();
      expect(read(slot('passphrase.safe'))).toBe('dpapi-A-new');
    });
  });

  describe('renaming and removing', () => {
    beforeEach(() => {
      withActiveWallet(A, 'dpapi-A');
      addQsdmWalletToStore({
        keystorePath: writeKeystoreFile(B),
        source: 'imported',
        persistPassphrase: persistAs('dpapi-B'),
      });
    });

    it('renames a wallet', () => {
      expect(renameQsdmStoredWallet(B, '  Ops   wallet ').label).toBe(
        'Ops wallet'
      );
      expect(listQsdmWallets().wallets[1].label).toBe('Ops wallet');
    });

    it('needs the full address to confirm', () => {
      expect(() => forgetQsdmStoredWallet(B, B.slice(0, 63))).toThrow(
        'does not match'
      );
      expect(listQsdmWallets().wallets).toHaveLength(2);
    });

    it('never removes the active wallet', () => {
      expect(() => forgetQsdmStoredWallet(A, A)).toThrow('Switch to another');
    });

    it('moves the wallet folder to the trash instead of deleting it', () => {
      const { trashPath } = forgetQsdmStoredWallet(B, B.toUpperCase());

      expect(listQsdmWallets().wallets.map((w) => w.address)).toEqual([A]);
      expect(fs.existsSync(path.join(mockSignerDir, 'wallets', B))).toBe(false);
      expect(read(path.join(trashPath, 'wallet.json'))).toBe(keystore(B));
      expect(read(path.join(trashPath, 'passphrase.safe'))).toBe('dpapi-B');
      expect(path.dirname(trashPath)).toBe(
        path.join(mockSignerDir, 'wallets', '.trash')
      );
    });
  });

  describe('recovering a wallet an older Hive set aside', () => {
    beforeEach(() => {
      withActiveWallet(B, 'dpapi-B');
      fs.writeFileSync(slot('wallet.json.bak-1795029829858'), keystore(A));
      fs.writeFileSync(slot('passphrase.safe.bak-1795029829860'), 'dpapi-A');
    });

    it('adds it back with its passphrase entry, without switching', () => {
      const recovered = recoverQsdmBackupWallet(
        'wallet.json.bak-1795029829858'
      );

      expect(recovered.address).toBe(A);
      expect(read(walletFile(A, 'wallet.json'))).toBe(keystore(A));
      expect(read(walletFile(A, 'passphrase.safe'))).toBe('dpapi-A');
      const list = listQsdmWallets();
      expect(list.active).toBe(B);
      expect(list.recoverable).toEqual([]);
      expect(list.wallets.find((w) => w.address === A)).toEqual(
        expect.objectContaining({
          label: 'Recovered wallet',
          source: 'recovered',
        })
      );
      // The backup files themselves stay where they are.
      expect(fs.existsSync(slot('wallet.json.bak-1795029829858'))).toBe(true);
      expect(read(slot('wallet.json'))).toBe(keystore(B));

      activateQsdmStoredWallet(A);
      expect(read(slot('passphrase.safe'))).toBe('dpapi-A');
    });

    it('accepts only a backup file name in the signer folder', () => {
      expect(() =>
        recoverQsdmBackupWallet('../wallet.json.bak-1795029829858')
      ).toThrow('Not a wallet backup');
      expect(() => recoverQsdmBackupWallet('wallet.json')).toThrow(
        'Not a wallet backup'
      );
      expect(() =>
        recoverQsdmBackupWallet('wallet.json.bak-1111111111111')
      ).toThrow('missing');
    });
  });
});
