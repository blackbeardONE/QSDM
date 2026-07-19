import { execFileSync } from 'child_process';
import { createHash } from 'crypto';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { getAppDataPath } from 'main/node/helpers/getAppDataPath';

// cspell:ignore abcdefghijklmnop habkkkednignfkoffhpbjahcjbikkahh HKCU
const NATIVE_HOST_NAME = 'tech.qsdm.hive_wallet';

// Public key material is safe to ship. Chromium uses it only to keep the
// unpacked/store extension ID stable; wallet keys never enter the extension.
export const QSDM_WALLET_EXTENSION_PUBLIC_KEY =
  'MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsHFgzuSZnQ2vWQ8EvlpUWU52nITYq9niLfQh7Qf/O4x9xFzM4dyypGl3gqqkcyc85lUZ//FH4xNd6kYB8PxKgR0NwhlHTMWOgFrHWRpsSvvRSMakpgVewVymn0DnvJOj0Pl8wIshbSh2XAYNI0xyMi5zuWK4kIPABhTh1VFLzd45g27fyz36Yyj+ZI7XCOPiRL5qNPJ+Ou9oBvPEnuBhFdQQrKR8pGYqKl/o8nb4Ynv+5wtooh8D1nZwoR2YA6JjwiFN6tzmc1egtNmAiIYG3Cn58jItYANsA6f9Gq8PwR0HjodGRgDXOWq525Q/dOAmnwLjAt/9L1HW5NBy5xrYhQIDAQAB';

export const QSDM_WALLET_EXTENSION_ID = 'habkkkednignfkoffhpbjahcjbikkahh';

interface ExtensionManifest {
  key?: unknown;
}

interface NativeHostRegistrationOptions {
  platform?: NodeJS.Platform;
  resourcesPath?: string;
  appDataPath?: string;
  homeDirectory?: string;
  extensionManifestPath?: string;
  nativeHostPath?: string;
  registryWriter?: (registryPath: string, manifestPath: string) => void;
}

export interface NativeHostRegistrationResult {
  installed: boolean;
  extensionId: string;
  manifestPath?: string;
  browsers: string[];
  reason?: string;
}

export const deriveChromiumExtensionId = (publicKey: string) => {
  const keyBytes = Buffer.from(publicKey, 'base64');
  if (!keyBytes.length || keyBytes.toString('base64') !== publicKey) {
    throw new Error('QSDM wallet extension public key is invalid');
  }
  const digest = createHash('sha256').update(keyBytes).digest();
  const alphabet = 'abcdefghijklmnop';
  return [...digest.subarray(0, 16)]
    .map(
      (value) => `${alphabet[Math.floor(value / 16)]}${alphabet[value % 16]}`
    )
    .join('');
};

const writePrivateJson = (filePath: string, value: unknown) => {
  fs.mkdirSync(path.dirname(filePath), { recursive: true, mode: 0o700 });
  const temporaryPath = `${filePath}.tmp-${process.pid}`;
  fs.writeFileSync(temporaryPath, `${JSON.stringify(value, null, 2)}\n`, {
    mode: 0o600,
  });
  fs.renameSync(temporaryPath, filePath);
  try {
    fs.chmodSync(filePath, 0o600);
  } catch {
    // Windows protects the file with the current user's profile ACL.
  }
};

const getRuntimeResourcesPath = () =>
  (process as NodeJS.Process & { resourcesPath?: string }).resourcesPath || '';

const defaultRegistryWriter = (registryPath: string, manifestPath: string) => {
  const regExecutable = path.join(
    process.env.SystemRoot || 'C:\\Windows',
    'System32',
    'reg.exe'
  );
  execFileSync(
    regExecutable,
    ['ADD', registryPath, '/ve', '/t', 'REG_SZ', '/d', manifestPath, '/f'],
    { windowsHide: true, stdio: 'ignore' }
  );
};

const validatePackagedExtension = (manifestPath: string) => {
  const manifest = JSON.parse(
    fs.readFileSync(manifestPath, 'utf-8')
  ) as ExtensionManifest;
  if (manifest.key !== QSDM_WALLET_EXTENSION_PUBLIC_KEY) {
    throw new Error('Packaged QSDM wallet extension key does not match Hive');
  }
  const extensionId = deriveChromiumExtensionId(manifest.key);
  if (extensionId !== QSDM_WALLET_EXTENSION_ID) {
    throw new Error('Packaged QSDM wallet extension ID failed validation');
  }
};

export const registerQsdmWalletProviderNativeHost = (
  options: NativeHostRegistrationOptions = {}
): NativeHostRegistrationResult => {
  const platform = options.platform || process.platform;
  if (platform !== 'win32' && platform !== 'linux') {
    return {
      installed: false,
      extensionId: QSDM_WALLET_EXTENSION_ID,
      browsers: [],
      reason: `Browser wallet bridge is not supported on ${platform}`,
    };
  }

  const resourcesPath = options.resourcesPath || getRuntimeResourcesPath();
  const executableName =
    platform === 'win32'
      ? 'qsdm-hive-wallet-host.exe'
      : 'qsdm-hive-wallet-host';
  const extensionManifestPath =
    options.extensionManifestPath ||
    path.join(resourcesPath, 'wallet-extension', 'manifest.json');
  const nativeHostPath = path.resolve(
    options.nativeHostPath || path.join(resourcesPath, 'native', executableName)
  );

  if (!fs.existsSync(extensionManifestPath)) {
    throw new Error(
      `Packaged QSDM wallet extension is missing: ${extensionManifestPath}`
    );
  }
  if (!fs.existsSync(nativeHostPath)) {
    throw new Error(`QSDM wallet native host is missing: ${nativeHostPath}`);
  }
  validatePackagedExtension(extensionManifestPath);

  const appDataPath = options.appDataPath || getAppDataPath(false);
  const sharedManifestPath = path.join(
    appDataPath,
    'wallet-provider',
    'native-messaging',
    `${NATIVE_HOST_NAME}.json`
  );
  const nativeManifest = {
    name: NATIVE_HOST_NAME,
    description: 'QSDM Wallet secure native bridge',
    path: nativeHostPath,
    type: 'stdio',
    allowed_origins: [`chrome-extension://${QSDM_WALLET_EXTENSION_ID}/`],
  };
  writePrivateJson(sharedManifestPath, nativeManifest);

  if (platform === 'win32') {
    const registryWriter = options.registryWriter || defaultRegistryWriter;
    const registryTargets = [
      [
        'Chrome',
        `HKCU\\Software\\Google\\Chrome\\NativeMessagingHosts\\${NATIVE_HOST_NAME}`,
      ],
      [
        'Edge',
        `HKCU\\Software\\Microsoft\\Edge\\NativeMessagingHosts\\${NATIVE_HOST_NAME}`,
      ],
    ] as const;
    registryTargets.forEach(([, registryPath]) =>
      registryWriter(registryPath, sharedManifestPath)
    );
    return {
      installed: true,
      extensionId: QSDM_WALLET_EXTENSION_ID,
      manifestPath: sharedManifestPath,
      browsers: registryTargets.map(([browser]) => browser),
    };
  }

  const homeDirectory = options.homeDirectory || os.homedir();
  const browserDirectories = [
    ['Chrome', '.config/google-chrome/NativeMessagingHosts'],
    ['Chromium', '.config/chromium/NativeMessagingHosts'],
    ['Edge', '.config/microsoft-edge/NativeMessagingHosts'],
    ['Brave', '.config/BraveSoftware/Brave-Browser/NativeMessagingHosts'],
  ] as const;
  browserDirectories.forEach(([, relativeDirectory]) => {
    writePrivateJson(
      path.join(homeDirectory, relativeDirectory, `${NATIVE_HOST_NAME}.json`),
      nativeManifest
    );
  });
  return {
    installed: true,
    extensionId: QSDM_WALLET_EXTENSION_ID,
    manifestPath: sharedManifestPath,
    browsers: browserDirectories.map(([browser]) => browser),
  };
};
