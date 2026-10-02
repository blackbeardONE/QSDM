// cspell:words qsdmminer
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  buildQsdmMinerOperatorSigningArgs,
  describeAdoptedQsdmMinerOperatorSigning,
  QSDM_MINER_UNLOCK_WALLET_MESSAGE,
  readQsdmKeystoreAddress,
  readQsdmMinerTomlString,
  redactQsdmMinerSecrets,
  resolveQsdmMinerOperatorSigning,
  summarizeQsdmMinerOperatorSigning,
  writeQsdmMinerLaunchRecord,
} from './qsdmMinerOperatorSigning';

const signer =
  'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc';
const otherWallet =
  'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd';
const hiveKeystore =
  'C:\\Users\\miner\\AppData\\Roaming\\QSDM-Hive\\hive-signer\\wallet.json';
const sessionPassphrase =
  'C:\\Users\\miner\\AppData\\Local\\Temp\\qsdm-hive-signer-42-x\\passphrase.txt';
const manualPassphrase = 'C:\\Users\\miner\\.qsdm\\operator-passphrase.txt';
const otherKeystore = 'D:\\wallets\\other.json';

const readySigner = {
  ready: true,
  sender: signer,
  keystorePath: hiveKeystore,
  passphraseFile: sessionPassphrase,
};

const resolve = ({
  signerStatus = readySigner as Parameters<
    typeof resolveQsdmMinerOperatorSigning
  >[0]['signerStatus'],
  minerConfig = '',
  existing = [hiveKeystore, sessionPassphrase],
  addresses = { [hiveKeystore]: signer } as Record<string, string>,
} = {}) =>
  resolveQsdmMinerOperatorSigning({
    signerStatus,
    minerConfig,
    fileExists: (filePath) => existing.includes(filePath),
    readKeystoreAddress: (filePath) => addresses[filePath] || '',
    homeDir: 'C:\\Users\\miner',
  });

describe('qsdmMinerOperatorSigning', () => {
  describe('readQsdmMinerTomlString', () => {
    it('reads basic, literal and bare TOML strings', () => {
      const config = [
        'protocol = "v2"',
        'operator_keystore_path = "C:\\\\Users\\\\miner\\\\wallet.json"',
        "operator_passphrase_file = 'C:\\Users\\miner\\.qsdm\\operator-passphrase.txt'",
        'node_id = hive-node-1 # trailing comment',
        '',
      ].join('\n');

      expect(readQsdmMinerTomlString(config, 'operator_keystore_path')).toBe(
        'C:\\Users\\miner\\wallet.json'
      );
      expect(readQsdmMinerTomlString(config, 'operator_passphrase_file')).toBe(
        manualPassphrase
      );
      expect(readQsdmMinerTomlString(config, 'node_id')).toBe('hive-node-1');
      expect(readQsdmMinerTomlString(config, 'protocol')).toBe('v2');
    });

    it('ignores commented, missing and similarly named keys', () => {
      const config = [
        "# operator_keystore_path = 'C:\\old\\wallet.json'",
        "operator_keystore_path_backup = 'C:\\backup\\wallet.json'",
        '',
      ].join('\n');

      expect(readQsdmMinerTomlString(config, 'operator_keystore_path')).toBe(
        ''
      );
      expect(readQsdmMinerTomlString('', 'operator_passphrase_file')).toBe('');
    });
  });

  describe('resolveQsdmMinerOperatorSigning', () => {
    it('signs with the unlocked Hive wallet through file paths', () => {
      const plan = resolve();

      expect(plan).toMatchObject({
        mode: 'hive',
        ready: true,
        address: signer,
        keystorePath: hiveKeystore,
        passphraseFile: sessionPassphrase,
        overridesMinerConfig: false,
      });
      expect(plan.warnings).toEqual([]);
      expect(buildQsdmMinerOperatorSigningArgs(plan)).toEqual([
        `--operator-keystore=${hiveKeystore}`,
        `--operator-passphrase-file=${sessionPassphrase}`,
      ]);
    });

    it('overrides operator_* lines in miner.toml for the same wallet and notes it', () => {
      const plan = resolve({
        minerConfig: `operator_keystore_path = '${hiveKeystore}'\noperator_passphrase_file = '${manualPassphrase}'\n`,
        existing: [hiveKeystore, sessionPassphrase, manualPassphrase],
      });

      expect(plan.mode).toBe('hive');
      expect(plan.overridesMinerConfig).toBe(true);
      expect(plan.passphraseFile).toBe(sessionPassphrase);
      expect(plan.warnings).toHaveLength(1);
      expect(plan.warnings[0]).toContain('overridden by the Hive wallet');
    });

    it('warns when miner.toml points at a wallet that is not the enrollment owner', () => {
      const plan = resolve({
        minerConfig: `operator_keystore_path = '${otherKeystore}'\noperator_passphrase_file = '${manualPassphrase}'\n`,
        existing: [
          hiveKeystore,
          sessionPassphrase,
          otherKeystore,
          manualPassphrase,
        ],
        addresses: { [hiveKeystore]: signer, [otherKeystore]: otherWallet },
      });

      expect(plan.mode).toBe('hive');
      expect(plan.address).toBe(signer);
      expect(plan.warnings[0]).toContain(otherWallet);
      expect(plan.warnings[0]).toContain('not the enrollment owner');
    });

    it('is locked when the per-launch passphrase file is gone', () => {
      const plan = resolve({
        signerStatus: {
          ...readySigner,
          ready: false,
          reason: 'Signer is missing: passphrase',
        },
        existing: [hiveKeystore],
      });

      expect(plan.mode).toBe('locked');
      expect(plan.ready).toBe(false);
      expect(plan.address).toBe(signer);
      expect(plan.message.startsWith(QSDM_MINER_UNLOCK_WALLET_MESSAGE)).toBe(
        true
      );
      expect(plan.message).toContain(
        'The wallet passphrase is not available in this Hive session.'
      );
      expect(buildQsdmMinerOperatorSigningArgs(plan)).toEqual([]);
    });

    it('is locked even if the signer reports ready but the passphrase file disappeared', () => {
      const plan = resolve({ existing: [hiveKeystore] });

      expect(plan.mode).toBe('locked');
      expect(buildQsdmMinerOperatorSigningArgs(plan)).toEqual([]);
    });

    it('is locked when the Hive wallet file belongs to another address', () => {
      const plan = resolve({
        addresses: { [hiveKeystore]: otherWallet },
      });

      expect(plan.mode).toBe('locked');
      expect(plan.message).toContain(
        `The Hive wallet file belongs to ${otherWallet}, not the active signer ${signer}.`
      );
    });

    it('is locked without any Hive wallet', () => {
      const plan = resolve({
        signerStatus: { ready: false },
        existing: [],
        addresses: {},
      });

      expect(plan.mode).toBe('locked');
      expect(plan.address).toBeUndefined();
      expect(plan.message).toContain('No QSDM wallet is configured in Hive.');
    });

    it('falls back to a complete manual miner.toml setup for the same wallet while Hive is locked', () => {
      const plan = resolve({
        signerStatus: { ...readySigner, ready: false },
        minerConfig: `operator_keystore_path = '${hiveKeystore}'\noperator_passphrase_file = '${manualPassphrase}'\n`,
        existing: [hiveKeystore, manualPassphrase],
      });

      expect(plan).toMatchObject({
        mode: 'miner-config',
        ready: true,
        address: signer,
        keystorePath: hiveKeystore,
        passphraseFile: manualPassphrase,
      });
      expect(buildQsdmMinerOperatorSigningArgs(plan)).toEqual([]);
    });

    it('uses the miner default ~/.qsdm/wallet.json when miner.toml only names a passphrase file', () => {
      const defaultWallet = path.join(
        'C:\\Users\\miner',
        '.qsdm',
        'wallet.json'
      );
      const plan = resolve({
        signerStatus: { ...readySigner, ready: false },
        minerConfig: `operator_passphrase_file = '${manualPassphrase}'\n`,
        existing: [hiveKeystore, defaultWallet, manualPassphrase],
        addresses: { [hiveKeystore]: signer, [defaultWallet]: signer },
      });

      expect(plan.mode).toBe('miner-config');
      expect(plan.keystorePath).toBe(defaultWallet);
    });

    it('does not fall back to a miner.toml wallet that is not the enrollment owner', () => {
      const plan = resolve({
        signerStatus: { ...readySigner, ready: false },
        minerConfig: `operator_keystore_path = '${otherKeystore}'\noperator_passphrase_file = '${manualPassphrase}'\n`,
        existing: [hiveKeystore, otherKeystore, manualPassphrase],
        addresses: { [hiveKeystore]: signer, [otherKeystore]: otherWallet },
      });

      expect(plan.mode).toBe('locked');
      expect(plan.warnings[0]).toContain(otherWallet);
    });

    it('does not fall back to an incomplete miner.toml setup', () => {
      const plan = resolve({
        signerStatus: { ...readySigner, ready: false },
        minerConfig: `operator_keystore_path = '${hiveKeystore}'\n`,
        existing: [hiveKeystore],
      });

      expect(plan.mode).toBe('locked');
    });

    it('summarizes without paths', () => {
      const summary = summarizeQsdmMinerOperatorSigning(resolve());

      expect(summary).toEqual({
        mode: 'hive',
        ready: true,
        address: signer,
        message: `Operator signing: enabled automatically with the Hive wallet ${signer}.`,
      });
      expect(JSON.stringify(summary)).not.toContain('passphrase.txt');
    });
  });

  describe('redactQsdmMinerSecrets', () => {
    it('keeps passphrase file paths readable', () => {
      const text = [
        `--operator-passphrase-file=${sessionPassphrase}`,
        `operator_passphrase_file = '${manualPassphrase}'`,
        'read operator passphrase: open C:\\missing\\passphrase.txt: The system cannot find the file specified.',
        'hmac_key_path = "C:\\Users\\miner\\.qsdm\\miner-hmac.key"',
      ].join('\n');

      expect(redactQsdmMinerSecrets(text)).toBe(text);
    });

    it('redacts labelled secrets and long hex key material', () => {
      const redacted = redactQsdmMinerSecrets(
        [
          'passphrase = "quoted secret one"',
          'QSDM_OPERATOR_PASSPHRASE=env-secret-two',
          '{"passphrase": "json-secret-three"}',
          'private key: hex-secret-four',
          'hmac=hmac-secret-five',
          `${'0f'.repeat(2448)}`,
        ].join('\n')
      );

      expect(redacted).not.toMatch(/secret|0f0f0f0f/);
      expect(redacted).toContain('passphrase = [redacted]');
      expect(redacted).toContain('QSDM_OPERATOR_PASSPHRASE=[redacted]');
      expect(redacted).toContain('"passphrase": "[redacted]"');
      expect(redacted).toContain('[redacted-hex]');
    });
  });

  describe('launch records', () => {
    let root = '';
    let recordPath = '';

    beforeEach(() => {
      root = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-miner-launch-'));
      recordPath = path.join(root, 'namespace', 'miner', 'miner-launch.json');
    });

    afterEach(() => {
      fs.rmSync(root, { recursive: true, force: true });
    });

    it('records the signing mode without secrets and recognizes the same process later', () => {
      writeQsdmMinerLaunchRecord(
        recordPath,
        4321,
        resolve(),
        new Date('2026-10-03T00:00:00.000Z')
      );

      const record = fs.readFileSync(recordPath, 'utf8');
      expect(JSON.parse(record)).toEqual({
        schema: 'qsdm.hive-miner-launch.v1',
        pid: 4321,
        startedAt: '2026-10-03T00:00:00.000Z',
        operatorSigning: 'hive',
        signer,
      });
      expect(record).not.toContain('passphrase');
      expect(describeAdoptedQsdmMinerOperatorSigning(recordPath, 4321)).toBe(
        `Adopted QSDM Miner pid=4321 was started by Hive with operator signing (Hive wallet, wallet ${signer}).`
      );
    });

    it('warns about adopted miners it cannot confirm are signing', () => {
      writeQsdmMinerLaunchRecord(recordPath, 4321, resolve());

      expect(
        describeAdoptedQsdmMinerOperatorSigning(recordPath, 999)
      ).toContain('operator_signature = enabled');
      expect(
        describeAdoptedQsdmMinerOperatorSigning(
          path.join(root, 'missing.json'),
          999
        )
      ).toContain('has no Hive operator-signing launch record');
      expect(
        describeAdoptedQsdmMinerOperatorSigning(
          path.join(root, 'missing.json'),
          999,
          '/opt/qsdm-hive/resources/miner/qsdmminer-console --config=/home/u/.qsdm/miner.toml --operator-keystore=/home/u/.config/QSDM-Hive/hive-signer/wallet.json'
        )
      ).toBe(
        'Adopted QSDM Miner pid=999 was started with Hive operator signing flags.'
      );
    });
  });

  it('reads only the public address field of a keystore file', () => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-keystore-addr-'));
    try {
      const walletPath = path.join(root, 'wallet.json');
      fs.writeFileSync(walletPath, JSON.stringify({ address: ` ${signer} ` }));
      expect(readQsdmKeystoreAddress(walletPath)).toBe(signer);
      fs.writeFileSync(walletPath, 'not json');
      expect(readQsdmKeystoreAddress(walletPath)).toBe('');
      expect(readQsdmKeystoreAddress(path.join(root, 'missing.json'))).toBe('');
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  });
});
