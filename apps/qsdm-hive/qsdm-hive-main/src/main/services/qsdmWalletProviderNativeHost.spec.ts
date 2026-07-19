import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  deriveChromiumExtensionId,
  QSDM_WALLET_EXTENSION_ID,
  QSDM_WALLET_EXTENSION_PUBLIC_KEY,
  registerQsdmWalletProviderNativeHost,
} from './qsdmWalletProviderNativeHost';

const createFixture = () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-native-host-'));
  const resourcesPath = path.join(root, 'resources');
  const extensionManifestPath = path.join(
    resourcesPath,
    'wallet-extension',
    'manifest.json'
  );
  const nativeHostPath = path.join(
    resourcesPath,
    'native',
    'qsdm-hive-wallet-host'
  );
  fs.mkdirSync(path.dirname(extensionManifestPath), { recursive: true });
  fs.mkdirSync(path.dirname(nativeHostPath), { recursive: true });
  fs.writeFileSync(
    extensionManifestPath,
    JSON.stringify({ key: QSDM_WALLET_EXTENSION_PUBLIC_KEY })
  );
  fs.writeFileSync(nativeHostPath, 'host');
  return { root, resourcesPath, extensionManifestPath, nativeHostPath };
};

describe('qsdmWalletProviderNativeHost', () => {
  it('pins the official Chromium extension ID', () => {
    expect(deriveChromiumExtensionId(QSDM_WALLET_EXTENSION_PUBLIC_KEY)).toBe(
      QSDM_WALLET_EXTENSION_ID
    );
  });

  it('registers the current-user Windows native host for Chrome and Edge', () => {
    const fixture = createFixture();
    const registrations: Array<[string, string]> = [];
    const result = registerQsdmWalletProviderNativeHost({
      platform: 'win32',
      resourcesPath: fixture.resourcesPath,
      appDataPath: path.join(fixture.root, 'app-data'),
      nativeHostPath: fixture.nativeHostPath,
      registryWriter: (registryPath, manifestPath) =>
        registrations.push([registryPath, manifestPath]),
    });

    expect(result.installed).toBe(true);
    expect(result.browsers).toEqual(['Chrome', 'Edge']);
    expect(registrations).toHaveLength(2);
    const manifest = JSON.parse(
      fs.readFileSync(result.manifestPath as string, 'utf-8')
    ) as { path: string; allowed_origins: string[] };
    expect(manifest.path).toBe(path.resolve(fixture.nativeHostPath));
    expect(manifest.allowed_origins).toEqual([
      `chrome-extension://${QSDM_WALLET_EXTENSION_ID}/`,
    ]);
  });

  it('writes private Linux native-host manifests for supported browsers', () => {
    const fixture = createFixture();
    const homeDirectory = path.join(fixture.root, 'home');
    const result = registerQsdmWalletProviderNativeHost({
      platform: 'linux',
      resourcesPath: fixture.resourcesPath,
      appDataPath: path.join(fixture.root, 'app-data'),
      homeDirectory,
      nativeHostPath: fixture.nativeHostPath,
    });

    expect(result.installed).toBe(true);
    expect(result.browsers).toEqual(['Chrome', 'Chromium', 'Edge', 'Brave']);
    const chromeManifest = path.join(
      homeDirectory,
      '.config/google-chrome/NativeMessagingHosts',
      'tech.qsdm.hive_wallet.json'
    );
    expect(fs.existsSync(chromeManifest)).toBe(true);
    const privatePermissions = fs.statSync(chromeManifest).mode % 64 === 0;
    expect(process.platform === 'win32' || privatePermissions).toBe(true);
  });

  it('can refresh an existing registration after Hive updates', () => {
    const fixture = createFixture();
    const options = {
      platform: 'linux' as const,
      resourcesPath: fixture.resourcesPath,
      appDataPath: path.join(fixture.root, 'app-data'),
      homeDirectory: path.join(fixture.root, 'home'),
      nativeHostPath: fixture.nativeHostPath,
    };

    const first = registerQsdmWalletProviderNativeHost(options);
    const second = registerQsdmWalletProviderNativeHost(options);

    expect(second).toEqual(first);
  });

  it('rejects an extension manifest with a different public key', () => {
    const fixture = createFixture();
    fs.writeFileSync(
      fixture.extensionManifestPath,
      JSON.stringify({ key: Buffer.from('different').toString('base64') })
    );

    expect(() =>
      registerQsdmWalletProviderNativeHost({
        platform: 'linux',
        resourcesPath: fixture.resourcesPath,
        appDataPath: path.join(fixture.root, 'app-data'),
        homeDirectory: path.join(fixture.root, 'home'),
        nativeHostPath: fixture.nativeHostPath,
      })
    ).toThrow('extension key does not match Hive');
  });
});
