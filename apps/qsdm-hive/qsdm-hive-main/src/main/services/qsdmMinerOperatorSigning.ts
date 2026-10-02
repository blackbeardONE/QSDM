import fs from 'fs';
import os from 'os';
import path from 'path';

import { QsdmTaskActionSignerStatus } from 'models/api/qsdm';

// cspell:ignore qsdmminer

// Public QSDM mining (HL2) rejects every proof that does not carry an ML-DSA
// operator_sig made with the enrollment owner's wallet key. Hive enrolls the
// miner with its own signer wallet and forces reward_address to that signer,
// so the Hive signer is the only key whose signatures can be accepted. This
// module decides how the packaged qsdmminer-console gets that key:
//
// - "hive": the Hive wallet is unlocked. Hive passes
//   --operator-keystore=<wallet.json> and --operator-passphrase-file=<the
//   per-launch passphrase file Hive already maintains>. The miner gives
//   command-line flags precedence over miner.toml, so these flags override any
//   operator_* lines in miner.toml for this launch (the lines are never edited).
// - "miner-config": the Hive wallet is locked, but miner.toml still carries a
//   complete manual operator_* setup for the same wallet as the Hive signer.
//   Hive passes no operator flags and the miner signs from miner.toml.
// - "locked": nothing can sign. Hive refuses to start a miner whose proofs the
//   network would reject and asks the user to unlock the wallet.
//
// Only paths and public wallet addresses are handled here. The passphrase
// content is never read, logged or placed on a command line.

export const QSDM_MINER_UNLOCK_WALLET_MESSAGE =
  'Unlock your QSDM wallet in Hive to mine.';

export type QsdmMinerOperatorSigningMode = 'hive' | 'miner-config' | 'locked';

export type QsdmMinerOperatorSigningPlan = {
  mode: QsdmMinerOperatorSigningMode;
  ready: boolean;
  /** Public wallet address whose ML-DSA key signs proofs. */
  address?: string;
  /** Keystore path used by the miner. A path, not key material. */
  keystorePath?: string;
  /** Passphrase file path used by the miner. Never the passphrase itself. */
  passphraseFile?: string;
  /** miner.toml has operator_* lines that Hive's flags override. */
  overridesMinerConfig: boolean;
  /** Safe for task logs and the UI. */
  message: string;
  /** Additional safe-for-log notes. */
  warnings: string[];
};

export type QsdmMinerOperatorSigningSummary = Pick<
  QsdmMinerOperatorSigningPlan,
  'mode' | 'ready' | 'address' | 'message'
>;

type SignerStatus = Pick<
  QsdmTaskActionSignerStatus,
  'ready' | 'sender' | 'keystorePath' | 'passphraseFile' | 'reason'
>;

export type ResolveQsdmMinerOperatorSigningOptions = {
  signerStatus: SignerStatus;
  minerConfig?: string;
  fileExists?: (filePath: string) => boolean;
  readKeystoreAddress?: (keystorePath: string) => string;
  homeDir?: string;
};

const sameAddress = (left?: string, right?: string) =>
  !!left?.trim() && left.trim().toLowerCase() === right?.trim().toLowerCase();

const defaultFileExists = (filePath: string) => {
  if (!filePath) return false;
  try {
    return fs.statSync(filePath).isFile();
  } catch {
    return false;
  }
};

// Reads only the public "address" field that Hive already shows for its
// signer wallet. Key material in the same file is never touched.
export const readQsdmKeystoreAddress = (keystorePath: string) => {
  try {
    const stat = fs.statSync(keystorePath);
    if (!stat.isFile() || stat.size > 2 * 1024 * 1024) return '';
    const parsed = JSON.parse(fs.readFileSync(keystorePath, 'utf8')) as {
      address?: unknown;
    };
    return typeof parsed.address === 'string' ? parsed.address.trim() : '';
  } catch {
    return '';
  }
};

const unescapeTomlBasicString = (value: string) =>
  value.replace(
    /\\(["\\btnfr]|u[0-9a-fA-F]{4}|U[0-9a-fA-F]{8})/g,
    (_match, escape: string) => {
      switch (escape[0]) {
        case 'b':
          return '\b';
        case 't':
          return '\t';
        case 'n':
          return '\n';
        case 'f':
          return '\f';
        case 'r':
          return '\r';
        case 'u':
        case 'U':
          return String.fromCodePoint(parseInt(escape.slice(1), 16));
        default:
          return escape;
      }
    }
  );

// Reads one top-level string key from miner.toml. Supports TOML basic
// ("C:\\path"), literal ('C:\path') and bare values, matching what the
// qsdmminer-console wizard, Hive and the public miner guide write.
export const readQsdmMinerTomlString = (config: string, key: string) => {
  const escapedKey = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = config.match(
    new RegExp(
      `^[ \\t]*${escapedKey}[ \\t]*=[ \\t]*(?:"((?:[^"\\\\\\r\\n]|\\\\.)*)"|'([^'\\r\\n]*)'|([^#\\r\\n]*))`,
      'm'
    )
  );
  if (!match) return '';
  if (match[1] !== undefined) return unescapeTomlBasicString(match[1]).trim();
  return (match[2] ?? match[3] ?? '').trim();
};

export const resolveQsdmMinerOperatorSigning = ({
  signerStatus,
  minerConfig = '',
  fileExists = defaultFileExists,
  readKeystoreAddress = readQsdmKeystoreAddress,
  homeDir = os.homedir(),
}: ResolveQsdmMinerOperatorSigningOptions): QsdmMinerOperatorSigningPlan => {
  const warnings: string[] = [];
  const signer = signerStatus.sender?.trim() || '';
  const hiveKeystore = signerStatus.keystorePath?.trim() || '';
  const hivePassphraseFile = signerStatus.passphraseFile?.trim() || '';
  const tomlKeystore = readQsdmMinerTomlString(
    minerConfig,
    'operator_keystore_path'
  );
  const tomlPassphraseFile = readQsdmMinerTomlString(
    minerConfig,
    'operator_passphrase_file'
  );
  const tomlConfigured = Boolean(tomlKeystore || tomlPassphraseFile);
  // qsdmminer-console falls back to ~/.qsdm/wallet.json when only the
  // passphrase file is configured.
  const tomlEffectiveKeystore =
    tomlKeystore ||
    (tomlPassphraseFile ? path.join(homeDir, '.qsdm', 'wallet.json') : '');

  const hiveKeystoreExists = !!hiveKeystore && fileExists(hiveKeystore);
  const hivePassphraseExists =
    !!hivePassphraseFile && fileExists(hivePassphraseFile);
  const hiveKeystoreAddress = hiveKeystoreExists
    ? readKeystoreAddress(hiveKeystore)
    : '';
  const hiveKeystoreMatchesSigner =
    !hiveKeystoreAddress || sameAddress(hiveKeystoreAddress, signer);

  if (
    signerStatus.ready &&
    signer &&
    hiveKeystoreExists &&
    hivePassphraseExists &&
    hiveKeystoreMatchesSigner
  ) {
    if (tomlConfigured) {
      const tomlAddress =
        tomlEffectiveKeystore && fileExists(tomlEffectiveKeystore)
          ? readKeystoreAddress(tomlEffectiveKeystore)
          : '';
      warnings.push(
        tomlAddress && !sameAddress(tomlAddress, signer)
          ? `miner.toml operator_keystore_path points at wallet ${tomlAddress}, which is not the enrollment owner. Hive signs with its wallet ${signer} instead; command-line flags override miner.toml.`
          : 'miner.toml operator_keystore_path / operator_passphrase_file are kept unchanged but overridden by the Hive wallet for this launch (command-line flags override miner.toml). A separately saved operator passphrase file is no longer needed.'
      );
    }
    return {
      mode: 'hive',
      ready: true,
      address: signer,
      keystorePath: hiveKeystore,
      passphraseFile: hivePassphraseFile,
      overridesMinerConfig: tomlConfigured,
      message: `Operator signing: enabled automatically with the Hive wallet ${signer}.`,
      warnings,
    };
  }

  // Hive cannot sign right now. Honour a complete manual miner.toml setup, but
  // only for the enrollment owner's wallet: any other key is rejected upstream.
  if (
    tomlPassphraseFile &&
    tomlEffectiveKeystore &&
    fileExists(tomlEffectiveKeystore) &&
    fileExists(tomlPassphraseFile)
  ) {
    const tomlAddress = readKeystoreAddress(tomlEffectiveKeystore);
    if (signer && sameAddress(tomlAddress, signer)) {
      return {
        mode: 'miner-config',
        ready: true,
        address: tomlAddress,
        keystorePath: tomlEffectiveKeystore,
        passphraseFile: tomlPassphraseFile,
        overridesMinerConfig: false,
        message: `Operator signing: using operator_keystore_path / operator_passphrase_file from miner.toml (wallet ${tomlAddress}) because the Hive wallet is locked.`,
        warnings,
      };
    }
    if (tomlAddress && signer) {
      warnings.push(
        `miner.toml operator_keystore_path points at wallet ${tomlAddress}, not the enrollment owner ${signer}; it cannot sign accepted proofs.`
      );
    }
  }

  let reason = signerStatus.reason || '';
  if (!signer) {
    reason = 'No QSDM wallet is configured in Hive.';
  } else if (!hiveKeystoreMatchesSigner) {
    reason = `The Hive wallet file belongs to ${hiveKeystoreAddress}, not the active signer ${signer}.`;
  } else if (!hiveKeystoreExists) {
    reason = 'The Hive wallet file was not found.';
  } else if (!hivePassphraseExists) {
    reason = 'The wallet passphrase is not available in this Hive session.';
  }

  return {
    mode: 'locked',
    ready: false,
    address: signer || undefined,
    overridesMinerConfig: false,
    message: `${QSDM_MINER_UNLOCK_WALLET_MESSAGE} Public QSDM mining only accepts proofs signed by the enrollment owner's wallet, so Hive will not start a miner whose proofs would be rejected. Open Settings > Wallet, unlock the wallet with its passphrase, then start the QSDM Miner again.${
      reason ? ` (${reason})` : ''
    }`,
    warnings,
  };
};

export const buildQsdmMinerOperatorSigningArgs = (
  plan?: QsdmMinerOperatorSigningPlan
) =>
  plan?.mode === 'hive' &&
  plan.ready &&
  plan.keystorePath &&
  plan.passphraseFile
    ? [
        `--operator-keystore=${plan.keystorePath}`,
        `--operator-passphrase-file=${plan.passphraseFile}`,
      ]
    : [];

export const summarizeQsdmMinerOperatorSigning = (
  plan: QsdmMinerOperatorSigningPlan
): QsdmMinerOperatorSigningSummary => ({
  mode: plan.mode,
  ready: plan.ready,
  address: plan.address,
  message: plan.message,
});

// Defense in depth for text that reaches task.log, error dialogs or crash
// output: miner stdout/stderr, the miner.log tail and the launch command.
// Paths (including --operator-passphrase-file=<path> and
// operator_passphrase_file = '<path>') stay readable; labelled secret values
// (hmac_key=..., passphrase=..., "passphrase": "...", private_key=...) and long
// hex blobs such as ML-DSA keys or signatures do not.
export const redactQsdmMinerSecrets = (text: string) =>
  text
    .replace(/(hmac(?:[_ -]?key)?\s*[=:]\s*)\S+/gi, '$1[redacted]')
    .replace(
      /((?:^|[^A-Za-z0-9])[A-Za-z0-9_.-]*passphrase\s*=\s*)("[^"\r\n]*"|'[^'\r\n]*'|\S+)/gim,
      '$1[redacted]'
    )
    .replace(
      /("[A-Za-z0-9_.-]*passphrase"\s*:\s*)"(?:[^"\\]|\\.)*"/gi,
      '$1"[redacted]"'
    )
    .replace(/(private[_ -]?key(?:[_ -]?hex)?\s*[=:]\s*)\S+/gi, '$1[redacted]')
    .replace(/\b[0-9a-fA-F]{512,}\b/g, '[redacted-hex]');

type QsdmMinerLaunchRecord = {
  schema: 'qsdm.hive-miner-launch.v1';
  pid: number;
  startedAt: string;
  operatorSigning: QsdmMinerOperatorSigningMode;
  signer?: string;
};

// Records (without secrets) how Hive launched its miner so a later Hive
// session can tell whether an adopted miner process signs its proofs.
export const writeQsdmMinerLaunchRecord = (
  recordPath: string,
  pid: number | undefined,
  plan: QsdmMinerOperatorSigningPlan,
  now = new Date()
) => {
  if (!pid) return;
  const record: QsdmMinerLaunchRecord = {
    schema: 'qsdm.hive-miner-launch.v1',
    pid,
    startedAt: now.toISOString(),
    operatorSigning: plan.mode,
    signer: plan.address,
  };
  fs.mkdirSync(path.dirname(recordPath), { recursive: true });
  fs.writeFileSync(recordPath, `${JSON.stringify(record, null, 2)}\n`, 'utf8');
};

export const describeAdoptedQsdmMinerOperatorSigning = (
  recordPath: string,
  pid: number,
  commandLine?: string
) => {
  if (commandLine?.includes('--operator-keystore=')) {
    return `Adopted QSDM Miner pid=${pid} was started with Hive operator signing flags.`;
  }
  try {
    const record = JSON.parse(
      fs.readFileSync(recordPath, 'utf8')
    ) as Partial<QsdmMinerLaunchRecord>;
    if (
      record.schema === 'qsdm.hive-miner-launch.v1' &&
      record.pid === pid &&
      (record.operatorSigning === 'hive' ||
        record.operatorSigning === 'miner-config')
    ) {
      return `Adopted QSDM Miner pid=${pid} was started by Hive with operator signing (${
        record.operatorSigning === 'hive' ? 'Hive wallet' : 'miner.toml'
      }${record.signer ? `, wallet ${record.signer}` : ''}).`;
    }
  } catch {
    // Missing or unreadable record: fall through to the warning.
  }
  return `Adopted QSDM Miner pid=${pid} has no Hive operator-signing launch record (it may predate Hive 1.4.21). If ~/.qsdm/miner.log does not show "operator_signature = enabled", stop and start the QSDM Miner in Hive so its proofs are signed.`;
};
