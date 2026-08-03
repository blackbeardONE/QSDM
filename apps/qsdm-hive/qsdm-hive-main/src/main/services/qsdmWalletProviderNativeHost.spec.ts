import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  deriveChromiumExtensionId,
  QSDM_WALLET_EXTENSION_ID,
  QSDM_WALLET_FIREFOX_EXTENSION_ID,
  QSDM_WALLET_EXTENSION_PUBLIC_KEY,
  QSDM_WALLET_INTERIM_CRX_EXTENSION_ID,
  QSDM_WALLET_STORE_EXTENSION_ID,
  QSDM_WALLET_TRUSTED_EXTENSION_IDS,
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
    JSON.stringify({
      key: QSDM_WALLET_EXTENSION_PUBLIC_KEY,
      browser_specific_settings: {
        gecko: { id: QSDM_WALLET_FIREFOX_EXTENSION_ID },
      },
    })
  );
  fs.writeFileSync(nativeHostPath, 'host');
  return { root, resourcesPath, extensionManifestPath, nativeHostPath };
};

describe('qsdmWalletProviderNativeHost', () => {
  it('pins the official Chromium extension ID', () => {
    expect(deriveChromiumExtensionId(QSDM_WALLET_EXTENSION_PUBLIC_KEY)).toBe(
      QSDM_WALLET_EXTENSION_ID
    );
    expect(QSDM_WALLET_STORE_EXTENSION_ID).toMatch(/^[a-p]{32}$/);
    expect(QSDM_WALLET_STORE_EXTENSION_ID).not.toBe(
      QSDM_WALLET_EXTENSION_ID
    );
    expect(QSDM_WALLET_INTERIM_CRX_EXTENSION_ID).toMatch(/^[a-p]{32}$/);
    expect(QSDM_WALLET_INTERIM_CRX_EXTENSION_ID).not.toBe(
      QSDM_WALLET_EXTENSION_ID
    );
    expect(QSDM_WALLET_INTERIM_CRX_EXTENSION_ID).not.toBe(
      QSDM_WALLET_STORE_EXTENSION_ID
    );
    expect(new Set(QSDM_WALLET_TRUSTED_EXTENSION_IDS).size).toBe(3);
  });

  it('registers the current-user Windows native host for Chromium and Firefox', () => {
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
    expect(result.browsers).toEqual(['Chrome', 'Edge', 'Firefox']);
    expect(registrations).toHaveLength(3);
    const manifest = JSON.parse(
      fs.readFileSync(result.manifestPath as string, 'utf-8')
    ) as { path: string; allowed_origins: string[] };
    expect(manifest.path).toBe(path.resolve(fixture.nativeHostPath));
    expect(manifest.allowed_origins).toEqual(
      QSDM_WALLET_TRUSTED_EXTENSION_IDS.map(
        (extensionId) => `chrome-extension://${extensionId}/`
      )
    );
    const firefoxManifest = JSON.parse(
      fs.readFileSync(result.firefoxManifestPath as string, 'utf-8')
    ) as { path: string; allowed_extensions: string[] };
    expect(firefoxManifest.path).toBe(path.resolve(fixture.nativeHostPath));
    expect(firefoxManifest.allowed_extensions).toEqual([
      QSDM_WALLET_FIREFOX_EXTENSION_ID,
    ]);
    expect(registrations[2][1]).toBe(result.firefoxManifestPath);
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
    expect(result.browsers).toEqual([
      'Chrome',
      'Chromium',
      'Edge',
      'Brave',
      'Firefox',
    ]);
    const chromeManifest = path.join(
      homeDirectory,
      '.config/google-chrome/NativeMessagingHosts',
      'tech.qsdm.hive_wallet.json'
    );
    expect(fs.existsSync(chromeManifest)).toBe(true);
    expect(
      fs.existsSync(
        path.join(
          homeDirectory,
          '.mozilla/native-messaging-hosts',
          'tech.qsdm.hive_wallet.json'
        )
      )
    ).toBe(true);
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
