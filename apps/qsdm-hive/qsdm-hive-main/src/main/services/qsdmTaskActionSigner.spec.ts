/**
 * @jest-environment node
 */

import fs from 'fs';
import os from 'os';
import path from 'path';

const originalEnv = process.env;

describe('qsdmTaskActionSigner', () => {
  let tmpDir = '';

  beforeEach(() => {
    jest.resetModules();
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-signer-service-'));
    process.env = {
      ...originalEnv,
      NODE_ENV: 'test',
    };
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
    process.env = originalEnv;
  });

  it('uses the active CLI keystore address instead of a stale configured sender', async () => {
    const keystorePath = path.join(tmpDir, 'wallet.json');
    fs.writeFileSync(
      keystorePath,
      JSON.stringify({
        type: 'qsdm-keystore',
        address: 'new-keystore-address',
      })
    );

    process.env.QSDM_TASK_ACTION_SIGNER = 'cli';
    process.env.QSDM_TASK_ACTION_CLI_PATH = 'qsdmcli';
    process.env.QSDM_TASK_ACTION_KEYSTORE_PATH = keystorePath;
    process.env.QSDM_TASK_ACTION_SENDER = 'old-stale-address';

    const { getQsdmTaskActionSender } = await import('./qsdmTaskActionSigner');

    expect(getQsdmTaskActionSender()).toBe('new-keystore-address');
  });

  it('uses a local backup signer that matches the configured QSDM wallet', async () => {
    const signerDir = path.join(tmpDir, 'hive-signer');
    fs.mkdirSync(signerDir, { recursive: true });
    const keystorePath = path.join(signerDir, 'wallet.json');
    const passphraseFile = path.join(signerDir, 'passphrase.txt');
    const backupKeystorePath = path.join(
      signerDir,
      'wallet.json.bak-123'
    );
    const backupPassphraseFile = path.join(
      signerDir,
      'passphrase.txt.bak-124'
    );

    fs.writeFileSync(
      keystorePath,
      JSON.stringify({
        type: 'qsdm-keystore',
        address: 'new-keystore-address',
      })
    );
    fs.writeFileSync(passphraseFile, 'new-passphrase');
    fs.writeFileSync(
      backupKeystorePath,
      JSON.stringify({
        type: 'qsdm-keystore',
        address: 'old-configured-address',
      })
    );
    fs.writeFileSync(backupPassphraseFile, 'old-passphrase');
    fs.utimesSync(keystorePath, new Date(1000), new Date(1000));
    fs.utimesSync(passphraseFile, new Date(1000), new Date(1000));
    fs.utimesSync(backupKeystorePath, new Date(2000), new Date(2000));
    fs.utimesSync(backupPassphraseFile, new Date(2001), new Date(2001));

    process.env.QSDM_TASK_ACTION_SIGNER = 'cli';
    process.env.QSDM_TASK_ACTION_CLI_PATH = 'qsdmcli';
    process.env.QSDM_TASK_ACTION_KEYSTORE_PATH = keystorePath;
    process.env.QSDM_TASK_ACTION_PASSPHRASE_FILE = passphraseFile;
    process.env.QSDM_WALLET_ADDRESS = 'old-configured-address';

    const {
      getQsdmTaskActionSender,
      getQsdmTaskActionKeystorePath,
      getQsdmTaskActionPassphraseFile,
    } = await import('./qsdmTaskActionSigner');

    expect(getQsdmTaskActionSender()).toBe('old-configured-address');
    expect(getQsdmTaskActionKeystorePath()).toBe(backupKeystorePath);
    expect(getQsdmTaskActionPassphraseFile()).toBe(backupPassphraseFile);
  });

  it('records the imported signer sender in process env', async () => {
    const { activateQsdmImportedSignerPaths, getQsdmTaskActionSender } =
      await import('./qsdmTaskActionSigner');

    const keystorePath = path.join(tmpDir, 'wallet.json');
    const passphraseFile = path.join(tmpDir, 'passphrase.txt');
    fs.writeFileSync(
      keystorePath,
      JSON.stringify({
        type: 'qsdm-keystore',
        address: 'imported-address',
      })
    );

    activateQsdmImportedSignerPaths({
      keystorePath,
      passphraseFile,
      sender: 'imported-address',
    });

    expect(process.env.QSDM_TASK_ACTION_SENDER).toBe('imported-address');
    expect(getQsdmTaskActionSender()).toBe('imported-address');
  });

  it('does not report ready when the configured keystore is missing', async () => {
    const passphraseFile = path.join(tmpDir, 'passphrase.txt');
    fs.writeFileSync(passphraseFile, 'test-passphrase');
    process.env.QSDM_TASK_ACTION_SIGNER = 'cli';
    process.env.QSDM_TASK_ACTION_CLI_PATH = 'qsdmcli';
    process.env.QSDM_TASK_ACTION_KEYSTORE_PATH = path.join(
      tmpDir,
      'missing-wallet.json'
    );
    process.env.QSDM_TASK_ACTION_PASSPHRASE_FILE = passphraseFile;
    process.env.QSDM_TASK_ACTION_SENDER = 'configured-address';

    const { getQsdmTaskActionSignerStatus } = await import(
      './qsdmTaskActionSigner'
    );
    const status = getQsdmTaskActionSignerStatus();

    expect(status.ready).toBe(false);
    expect(status.checks.keystore).toBe(false);
    expect(status.reason).toContain('keystore');
  });

  it('enables signed actions through the exact official gateway', async () => {
    const keystorePath = path.join(tmpDir, 'wallet.json');
    fs.writeFileSync(
      keystorePath,
      JSON.stringify({
        type: 'qsdm-keystore',
        address: 'gateway-signer-address',
      })
    );

    const gateway =
      'https://api.qsdm.tech/attest/home-validator/api/v1';
    process.env.QSDM_GATEWAY_API_URL = gateway;
    process.env.QSDM_CORE_API_URL = gateway;
    process.env.QSDM_TASK_ACTION_SIGNER = 'cli';
    process.env.QSDM_TASK_ACTION_KEYSTORE_PATH = keystorePath;

    const { getQsdmLocalSignedLoopEnabled } = await import(
      './qsdmTaskActionSigner'
    );

    expect(getQsdmLocalSignedLoopEnabled()).toBe(true);
  });

  it('does not enable signed actions for an arbitrary remote Core URL', async () => {
    const keystorePath = path.join(tmpDir, 'wallet.json');
    fs.writeFileSync(
      keystorePath,
      JSON.stringify({
        type: 'qsdm-keystore',
        address: 'custom-signer-address',
      })
    );

    process.env.QSDM_CORE_API_URL = 'https://example.invalid/api/v1';
    process.env.QSDM_TASK_ACTION_SIGNER = 'cli';
    process.env.QSDM_TASK_ACTION_KEYSTORE_PATH = keystorePath;

    const { getQsdmLocalSignedLoopEnabled } = await import(
      './qsdmTaskActionSigner'
    );

    expect(getQsdmLocalSignedLoopEnabled()).toBe(false);
  });

  it('does not trust a custom URL merely because it is configured as the gateway', async () => {
    const keystorePath = path.join(tmpDir, 'wallet.json');
    fs.writeFileSync(
      keystorePath,
      JSON.stringify({
        type: 'qsdm-keystore',
        address: 'custom-gateway-signer-address',
      })
    );

    const customGateway = 'https://gateway.example.invalid/api/v1';
    process.env.QSDM_GATEWAY_API_URL = customGateway;
    process.env.QSDM_CORE_API_URL = customGateway;
    process.env.QSDM_TASK_ACTION_SIGNER = 'cli';
    process.env.QSDM_TASK_ACTION_KEYSTORE_PATH = keystorePath;

    const { getQsdmLocalSignedLoopEnabled } = await import(
      './qsdmTaskActionSigner'
    );

    expect(getQsdmLocalSignedLoopEnabled()).toBe(false);
  });
});
