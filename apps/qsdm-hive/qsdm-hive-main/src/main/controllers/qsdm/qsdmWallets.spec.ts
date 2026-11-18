/**
 * @jest-environment node
 */

import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  activateQsdmWalletController,
  assertQsdmWalletSwitchAllowed,
  forgetQsdmWalletController,
  listQsdmWalletsController,
  recoverQsdmWalletController,
  renameQsdmWalletController,
} from './qsdmWallets';

import type { Event } from 'electron';

let mockSignerDir = '';
const mockRunningTasks: Record<string, unknown> = {};
const mockInitializeSecrets = jest.fn();

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

jest.mock('main/services/qsdmSignerSecretStore', () => ({
  initializeQsdmSignerSecretStore: () => mockInitializeSecrets(),
}));

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    get RUNNING_TASKS() {
      return mockRunningTasks;
    },
  },
}));

const A = 'a'.repeat(64);
const B = 'b'.repeat(64);
const event = {} as Event;
const keystore = (address: string) =>
  JSON.stringify({ type: 'qsdm-keystore', address });

describe('QSDM wallet list controllers', () => {
  const savedSender = process.env.QSDM_TASK_ACTION_SENDER;

  beforeEach(() => {
    mockSignerDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-wallet-ctl-'));
    Object.keys(mockRunningTasks).forEach(
      (key) => delete mockRunningTasks[key]
    );
    mockInitializeSecrets.mockReset();
    mockInitializeSecrets.mockReturnValue({ configured: true });
    // Hive 1.4.24 state: B imported over A, A kept as a .bak file with its passphrase entry.
    fs.writeFileSync(path.join(mockSignerDir, 'wallet.json'), keystore(B));
    fs.writeFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'safe-B');
    fs.writeFileSync(
      path.join(mockSignerDir, 'wallet.json.bak-1795029829858'),
      keystore(A)
    );
    fs.writeFileSync(
      path.join(mockSignerDir, 'passphrase.safe.bak-1795029829860'),
      'safe-A'
    );
  });

  afterEach(() => {
    fs.rmSync(mockSignerDir, { recursive: true, force: true });
    if (savedSender === undefined) delete process.env.QSDM_TASK_ACTION_SENDER;
    else process.env.QSDM_TASK_ACTION_SENDER = savedSender;
  });

  it("brings back the owner's replaced wallet and switches the signer to it", async () => {
    const before = await listQsdmWalletsController();
    expect(before.active).toBe(B);
    expect(before.minerRunning).toBe(false);
    expect(before.recoverable.map((r) => r.address)).toEqual([A]);

    await expect(
      recoverQsdmWalletController(event, {
        backupFile: before.recoverable[0].backupFile,
      })
    ).resolves.toEqual({ address: A });

    const switched = await activateQsdmWalletController(event, { address: A });
    expect(switched).toEqual({ address: A, unlocked: true });
    expect(mockInitializeSecrets).toHaveBeenCalledTimes(1);
    expect(process.env.QSDM_TASK_ACTION_SENDER).toBe(A);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'utf-8')
    ).toBe('safe-A');

    const after = await listQsdmWalletsController();
    expect(after.active).toBe(A);
    expect(after.wallets.map((w) => w.address)).toEqual([B, A]);
    expect(after.recoverable).toEqual([]);
  });

  it('reports a switched wallet that still needs its passphrase', async () => {
    fs.rmSync(path.join(mockSignerDir, 'passphrase.safe.bak-1795029829860'));
    await recoverQsdmWalletController(event, {
      backupFile: 'wallet.json.bak-1795029829858',
    });
    mockInitializeSecrets.mockReturnValue({ configured: false });

    await expect(
      activateQsdmWalletController(event, { address: A })
    ).resolves.toEqual({ address: A, unlocked: false });
  });

  it.each(['qsdm-system-miner', 'qsdm-system-pool-miner'])(
    'refuses to switch while %s runs',
    async (taskId) => {
      await recoverQsdmWalletController(event, {
        backupFile: 'wallet.json.bak-1795029829858',
      });
      mockRunningTasks[taskId] = {};

      expect((await listQsdmWalletsController()).minerRunning).toBe(true);
      await expect(
        activateQsdmWalletController(event, { address: A })
      ).rejects.toThrow('Stop the QSDM Miner');
      expect(() => assertQsdmWalletSwitchAllowed(true)).toThrow(
        'Stop the QSDM Miner'
      );
      expect(() => assertQsdmWalletSwitchAllowed(false)).not.toThrow();
      expect(
        fs.readFileSync(path.join(mockSignerDir, 'wallet.json'), 'utf-8')
      ).toBe(keystore(B));
      expect(mockInitializeSecrets).not.toHaveBeenCalled();
    }
  );

  it('lets other tasks run while switching', async () => {
    await recoverQsdmWalletController(event, {
      backupFile: 'wallet.json.bak-1795029829858',
    });
    mockRunningTasks['some-other-task'] = {};
    await expect(
      activateQsdmWalletController(event, { address: A })
    ).resolves.toEqual(expect.objectContaining({ address: A }));
  });

  it('renames and forgets through the store rules', async () => {
    await recoverQsdmWalletController(event, {
      backupFile: 'wallet.json.bak-1795029829858',
    });
    await expect(
      renameQsdmWalletController(event, { address: A, label: 'Ops' })
    ).resolves.toEqual(expect.objectContaining({ label: 'Ops' }));
    await expect(
      forgetQsdmWalletController(event, { address: B, confirmAddress: B })
    ).rejects.toThrow('Switch to another');
    await expect(
      forgetQsdmWalletController(event, { address: A, confirmAddress: A })
    ).resolves.toEqual({ address: A });
    expect((await listQsdmWalletsController()).wallets).toHaveLength(1);
  });
});
