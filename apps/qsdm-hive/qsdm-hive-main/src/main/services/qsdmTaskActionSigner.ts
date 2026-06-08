import fs from 'fs';
import path from 'path';
import { spawnSync } from 'child_process';

import {
  QSDM_CORE_API_URL,
  QSDM_ENABLE_LOCAL_SIGNED_LOOP,
  QSDM_WALLET_ADDRESS,
} from 'config/qsdm';
import { QsdmTaskActionSignerStatus } from 'models/api/qsdm';
import { getAppDataPath } from 'main/node/helpers/getAppDataPath';

const readEnv = (key: string, fallback = '') => {
  const value = process.env[key];
  return value?.trim() || fallback;
};

const normalizeEnvPath = (value: string) =>
  value.replace(/\r/g, '\\r').replace(/\n/g, '\\n').replace(/\t/g, '\\t');

const uniquePaths = (values: string[]) =>
  Array.from(
    new Set(values.filter(Boolean).map((value) => path.resolve(value)))
  );

const autoDiscoveryEnabled = () =>
  readEnv('QSDM_DISABLE_LOCAL_SIGNER_DISCOVERY', '0') !== '1' &&
  process.env.NODE_ENV !== 'test';

const localCoreApiEnabled = () => {
  try {
    const host = new URL(QSDM_CORE_API_URL).hostname.toLowerCase();
    return host === '127.0.0.1' || host === 'localhost' || host === '::1';
  } catch {
    return false;
  }
};

const getAncestorRoots = (start: string, maxDepth = 8) => {
  const roots: string[] = [];
  let current = path.resolve(start);

  for (let depth = 0; depth <= maxDepth; depth += 1) {
    roots.push(current);
    const parent = path.dirname(current);
    if (parent === current) break;
    current = parent;
  }

  return roots;
};

const getCommonLocalWorkspaceRoots = () => {
  const roots: string[] = [];
  const home = readEnv('USERPROFILE', readEnv('HOME', ''));

  if (home) {
    roots.push(
      path.join(home, 'Projects', 'QSDM+'),
      path.join(home, 'Projects', 'QSDM'),
      path.join(home, 'Documents', 'QSDM+'),
      path.join(home, 'Documents', 'QSDM')
    );
  }

  if (process.platform === 'win32') {
    for (let code = 65; code <= 90; code += 1) {
      const drive = `${String.fromCharCode(code)}:\\`;
      roots.push(
        path.join(drive, 'Projects', 'QSDM+'),
        path.join(drive, 'Projects', 'QSDM'),
        path.join(drive, 'QSDM+'),
        path.join(drive, 'QSDM')
      );
    }
  }

  return roots;
};

const getCandidateRoots = () =>
  uniquePaths([
    readEnv('QSDM_WORKSPACE_ROOT', ''),
    readEnv('QSDM_REPO_ROOT', ''),
    ...getAncestorRoots(process.cwd()),
    ...getAncestorRoots(__dirname),
    ...getCommonLocalWorkspaceRoots(),
  ]);

const getPreferredLocalSignerRoot = () =>
  getCandidateRoots().find((root) =>
    fileExists(path.join(root, 'QSDM', 'source', 'cmd', 'qsdmcli', 'main.go'))
  );

const getAppDataSignerPaths = () => {
  const signerDir = path.join(getAppDataPath(), 'hive-signer');

  return {
    signerDir,
    keystorePath: path.join(signerDir, 'wallet.json'),
    passphraseFile: path.join(signerDir, 'passphrase.txt'),
  };
};

export const getQsdmDefaultLocalSignerPaths = () => {
  const appDataSigner = getAppDataSignerPaths();
  const workspaceRoot = getPreferredLocalSignerRoot();

  if (!workspaceRoot) {
    return appDataSigner;
  }

  const signerDir = path.join(
    workspaceRoot,
    'QSDM',
    'source',
    '.cache',
    'local-validator',
    'hive-signer'
  );

  return {
    signerDir,
    keystorePath: path.join(signerDir, 'wallet.json'),
    passphraseFile: path.join(signerDir, 'passphrase.txt'),
  };
};

const findFirstExistingFile = (candidates: string[]) =>
  candidates.find((candidate) => {
    try {
      return fs.existsSync(candidate) && fs.statSync(candidate).isFile();
    } catch {
      return false;
    }
  }) || '';

const sameAddress = (left?: string, right?: string) =>
  !!left?.trim() && left.trim().toLowerCase() === right?.trim().toLowerCase();

const fileMtimeMs = (candidate: string) => {
  try {
    return fs.statSync(candidate).mtimeMs;
  } catch {
    return Number.POSITIVE_INFINITY;
  }
};

const copyFilePrivate = (source: string, destination: string) => {
  fs.copyFileSync(source, destination);
  try {
    fs.chmodSync(destination, 0o600);
  } catch {
    // Windows may ignore POSIX file modes; the user profile ACL still applies.
  }
};

const readWalletAddressFromJson = (keystorePath: string) => {
  try {
    const wallet = JSON.parse(fs.readFileSync(keystorePath, 'utf-8')) as {
      address?: string;
    };
    return wallet.address?.trim() || '';
  } catch {
    return '';
  }
};

const getSignerDirCandidates = (keystorePath: string) =>
  uniquePaths([
    keystorePath ? path.dirname(keystorePath) : '',
    getAppDataSignerPaths().signerDir,
    getQsdmDefaultLocalSignerPaths().signerDir,
    getDiscoveredLocalSigner().keystorePath
      ? path.dirname(getDiscoveredLocalSigner().keystorePath)
      : '',
  ]);

const listFiles = (dir: string, startsWith: string) => {
  try {
    return fs
      .readdirSync(dir)
      .filter((name) => name.startsWith(startsWith))
      .map((name) => path.join(dir, name))
      .filter((candidate) => fs.statSync(candidate).isFile());
  } catch {
    return [];
  }
};

const findPassphraseForWallet = (
  walletPath: string,
  fallbackPassphraseFile: string
) => {
  const dir = path.dirname(walletPath);
  const walletName = path.basename(walletPath);
  const defaultPassphrase = path.join(dir, 'passphrase.txt');

  if (walletName === 'wallet.json' && fileExists(defaultPassphrase)) {
    return defaultPassphrase;
  }

  const backupSuffix = walletName.replace(/^wallet\.json\.bak-/, '');
  const exactBackup =
    backupSuffix && backupSuffix !== walletName
      ? path.join(dir, `passphrase.txt.bak-${backupSuffix}`)
      : '';
  if (exactBackup && fileExists(exactBackup)) {
    return exactBackup;
  }

  const passphraseCandidates = listFiles(dir, 'passphrase.txt');
  if (passphraseCandidates.length > 0) {
    const walletMtime = fileMtimeMs(walletPath);
    return passphraseCandidates.sort(
      (left, right) =>
        Math.abs(fileMtimeMs(left) - walletMtime) -
        Math.abs(fileMtimeMs(right) - walletMtime)
    )[0];
  }

  return fallbackPassphraseFile;
};

type LocalSignerPaths = {
  cliPath: string;
  keystorePath: string;
  passphraseFile: string;
};

const sameResolvedPath = (left: string, right: string) =>
  !!left &&
  !!right &&
  path.resolve(left).toLowerCase() === path.resolve(right).toLowerCase();

const mirrorDiscoveredSignerToAppData = (
  discovered: LocalSignerPaths
): LocalSignerPaths => {
  const appDataSigner = getAppDataSignerPaths();

  if (
    !discovered.keystorePath ||
    !discovered.passphraseFile ||
    sameResolvedPath(discovered.keystorePath, appDataSigner.keystorePath)
  ) {
    return discovered;
  }

  if (
    fileExists(appDataSigner.keystorePath) ||
    fileExists(appDataSigner.passphraseFile)
  ) {
    return discovered;
  }

  try {
    fs.mkdirSync(appDataSigner.signerDir, { recursive: true, mode: 0o700 });
    copyFilePrivate(discovered.keystorePath, appDataSigner.keystorePath);
    copyFilePrivate(discovered.passphraseFile, appDataSigner.passphraseFile);

    return {
      ...discovered,
      keystorePath: appDataSigner.keystorePath,
      passphraseFile: appDataSigner.passphraseFile,
    };
  } catch (error) {
    console.warn(
      'QSDM local signer was discovered but could not be mirrored into app data.',
      error
    );
    return discovered;
  }
};

const getBaseTaskActionSignerPaths = () => {
  const discovered = mirrorDiscoveredSignerToAppData(getDiscoveredLocalSigner());

  return {
    cliPath: normalizeEnvPath(
      readEnv(
        'QSDM_TASK_ACTION_CLI_PATH',
        readEnv('QSDM_CLI_PATH', discovered.cliPath || 'qsdmcli')
      )
    ),
    keystorePath: normalizeEnvPath(
      readEnv(
        'QSDM_TASK_ACTION_KEYSTORE_PATH',
        readEnv('QSDM_KEYSTORE_PATH', discovered.keystorePath)
      )
    ),
    passphraseFile: normalizeEnvPath(
      readEnv(
        'QSDM_TASK_ACTION_PASSPHRASE_FILE',
        readEnv('QSDM_PASSPHRASE_FILE', discovered.passphraseFile)
      )
    ),
  };
};

const findLocalSignerForAddress = ({
  address,
  cliPath,
  keystorePath,
  passphraseFile,
}: {
  address: string;
  cliPath: string;
  keystorePath: string;
  passphraseFile: string;
}) => {
  if (!address) return undefined;

  for (const signerDir of getSignerDirCandidates(keystorePath)) {
    for (const walletPath of listFiles(signerDir, 'wallet.json')) {
      const walletAddress =
        readWalletAddressFromJson(walletPath) ||
        readWalletAddress(cliPath, walletPath);

      if (sameAddress(walletAddress, address)) {
        return {
          address: walletAddress,
          keystorePath: walletPath,
          passphraseFile: findPassphraseForWallet(
            walletPath,
            passphraseFile
          ),
        };
      }
    }
  }

  return undefined;
};

const getDiscoveredLocalSigner = () => {
  if (!autoDiscoveryEnabled()) {
    return { cliPath: '', keystorePath: '', passphraseFile: '' };
  }

  const roots = getCandidateRoots();
  const cliPath = findFirstExistingFile(
    roots.flatMap((root) => [
      path.join(
        root,
        'apps',
        'qsdm-hive',
        'qsdm-hive-main',
        'release',
        'native-smoke',
        'qsdmcli-smoke.exe'
      ),
      path.join(root, 'release', 'native-smoke', 'qsdmcli-smoke.exe'),
      path.join(
        root,
        'QSDM',
        'source',
        '.cache',
        'local-validator',
        'qsdmcli.exe'
      ),
    ])
  );
  const keystorePath = findFirstExistingFile([
    getAppDataSignerPaths().keystorePath,
    ...roots.flatMap((root) => [
      path.join(
        root,
        'QSDM',
        'source',
        '.cache',
        'local-validator',
        'hive-signer',
        'wallet.json'
      ),
      path.join(
        root,
        'source',
        '.cache',
        'local-validator',
        'hive-signer',
        'wallet.json'
      ),
    ]),
  ]);
  const passphraseFile = findFirstExistingFile([
    getAppDataSignerPaths().passphraseFile,
    ...roots.flatMap((root) => [
      path.join(
        root,
        'QSDM',
        'source',
        '.cache',
        'local-validator',
        'hive-signer',
        'passphrase.txt'
      ),
      path.join(
        root,
        'source',
        '.cache',
        'local-validator',
        'hive-signer',
        'passphrase.txt'
      ),
    ]),
  ]);

  return { cliPath, keystorePath, passphraseFile };
};

const readWalletAddress = (cliPath: string, keystorePath: string) => {
  if (!cliPath || !keystorePath) return '';

  try {
    const wallet = JSON.parse(fs.readFileSync(keystorePath, 'utf-8')) as {
      address?: string;
    };
    if (wallet.address?.trim()) return wallet.address.trim();
  } catch {
    // Fall back to the CLI for older keystore formats that do not store address.
  }

  try {
    const result = spawnSync(
      cliPath,
      ['wallet', 'show', '--in', keystorePath, '--json'],
      {
        encoding: 'utf-8',
        windowsHide: true,
        timeout: 5000,
      }
    );
    if (result.status !== 0 || !result.stdout.trim()) return '';
    const parsed = JSON.parse(result.stdout) as { address?: string };
    return parsed.address?.trim() || '';
  } catch {
    return '';
  }
};

const resolveTaskActionSigner = () => {
  const base = getBaseTaskActionSignerPaths();
  const configuredSender = readEnv(
    'QSDM_TASK_ACTION_SENDER',
    QSDM_WALLET_ADDRESS
  );
  const keystoreAddress = readWalletAddress(
    base.cliPath,
    base.keystorePath
  );

  if (
    getQsdmTaskActionSignerMode() === 'cli' &&
    configuredSender &&
    keystoreAddress &&
    !sameAddress(configuredSender, keystoreAddress)
  ) {
    const configuredSigner = findLocalSignerForAddress({
      address: configuredSender,
      cliPath: base.cliPath,
      keystorePath: base.keystorePath,
      passphraseFile: base.passphraseFile,
    });

    if (configuredSigner) {
      return {
        ...base,
        ...configuredSigner,
      };
    }
  }

  return {
    ...base,
    address:
      getQsdmTaskActionSignerMode() === 'cli' && keystoreAddress
        ? keystoreAddress
        : configuredSender || keystoreAddress,
  };
};

export const getQsdmTaskActionKeystoreAddress = () =>
  resolveTaskActionSigner().address || '';

export const getQsdmTaskActionSender = () => {
  return getQsdmTaskActionKeystoreAddress();
};

export const getQsdmTaskActionSignerMode = () =>
  readEnv(
    'QSDM_TASK_ACTION_SIGNER',
    getDiscoveredLocalSigner().cliPath ? 'cli' : ''
  ).toLowerCase();

export const getQsdmTaskActionCliPath = () =>
  resolveTaskActionSigner().cliPath;

export const getQsdmTaskActionKeystorePath = () =>
  resolveTaskActionSigner().keystorePath;

export const getQsdmTaskActionPassphraseFile = () =>
  resolveTaskActionSigner().passphraseFile;

export const activateQsdmImportedSignerPaths = ({
  keystorePath,
  passphraseFile,
  sender,
}: {
  keystorePath: string;
  passphraseFile: string;
  sender?: string;
}) => {
  process.env.QSDM_TASK_ACTION_KEYSTORE_PATH = keystorePath;
  process.env.QSDM_TASK_ACTION_PASSPHRASE_FILE = passphraseFile;
  process.env.QSDM_TASK_ACTION_SIGNER = 'cli';
  if (sender?.trim()) {
    process.env.QSDM_TASK_ACTION_SENDER = sender.trim();
  }
};

export const getQsdmLocalSignedLoopEnabled = () =>
  QSDM_ENABLE_LOCAL_SIGNED_LOOP ||
  (!!getDiscoveredLocalSigner().keystorePath && localCoreApiEnabled());

const fileExists = (candidate: string) => {
  if (!candidate) return false;

  try {
    return fs.existsSync(candidate);
  } catch {
    return false;
  }
};

const shouldCheckCliPath = (cliPath: string) =>
  path.isAbsolute(cliPath) ||
  cliPath.includes('/') ||
  cliPath.includes('\\') ||
  cliPath.toLowerCase().endsWith('.exe');

export const getQsdmTaskActionSignerStatus = (): QsdmTaskActionSignerStatus => {
  const mode = getQsdmTaskActionSignerMode();
  const sender = getQsdmTaskActionSender();
  const cliPath = getQsdmTaskActionCliPath();
  const keystorePath = getQsdmTaskActionKeystorePath();
  const passphraseFile = getQsdmTaskActionPassphraseFile();

  const cliExists = shouldCheckCliPath(cliPath) ? fileExists(cliPath) : true;
  const keystoreExists = keystorePath ? fileExists(keystorePath) : true;
  const passphraseExists = fileExists(passphraseFile);

  const checks = {
    sender: !!sender,
    cliMode: mode === 'cli',
    cliPath: cliExists,
    keystore: keystoreExists,
    passphrase: passphraseExists,
  };

  const missing = Object.entries(checks)
    .filter(([, ok]) => !ok)
    .map(([name]) => name);

  return {
    mode: mode || 'none',
    configured: mode === 'cli' && !!sender,
    ready: missing.length === 0,
    localLoopEnabled: getQsdmLocalSignedLoopEnabled(),
    sender: sender || undefined,
    cliPath,
    keystorePath: keystorePath || undefined,
    passphraseFile: passphraseFile || undefined,
    checks,
    reason:
      missing.length > 0
        ? `Signer is missing: ${missing.join(', ')}`
        : undefined,
  };
};
