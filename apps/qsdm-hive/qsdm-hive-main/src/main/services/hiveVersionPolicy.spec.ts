import {
  canUseLocalHiveVersionPolicyOverrides,
  getDefaultHiveVersionManifestUrl,
  getCurrentHiveVersion,
  getHiveSignedReleaseManifestUrl,
  getHiveVersionManifestUrl,
  getHiveVersionPolicyStatus,
  getHiveVersionReferencePlatform,
  hasOwnSignedReleaseChannel,
  isUnsignedPreviewHiveVersion,
  parseHiveReleaseManifest,
  resetHiveVersionPolicyCacheForTests,
} from './hiveVersionPolicy';

const originalEnv = process.env;

describe('hiveVersionPolicy', () => {
  beforeEach(() => {
    jest.resetModules();
    resetHiveVersionPolicyCacheForTests();
    process.env = { ...originalEnv };
    delete process.env.QSDM_HIVE_DISABLE_VERSION_POLICY;
    delete process.env.QSDM_HIVE_VERSION_MANIFEST_URL;
    delete process.env.QSDM_HIVE_DOWNLOAD_URL;
    delete process.env.QSDM_HIVE_REQUIRED_VERSION;
  });

  afterAll(() => {
    process.env = originalEnv;
  });

  it('checks a Mac build against the signed Linux release and sends it to the download page', () => {
    expect(getHiveVersionReferencePlatform('darwin')).toBe('linux');
    expect(getHiveVersionReferencePlatform('linux')).toBe('linux');
    expect(getHiveVersionReferencePlatform('win32')).toBe('win32');
    expect(hasOwnSignedReleaseChannel('darwin')).toBe(false);
    expect(hasOwnSignedReleaseChannel('linux')).toBe(true);
    expect(hasOwnSignedReleaseChannel('win32')).toBe(true);
    // macOS used to throw here ("signed updates do not support darwin"), which
    // blocked every guarded endpoint of a Mac build.
    expect(getHiveSignedReleaseManifestUrl('darwin')).toBe(
      getHiveSignedReleaseManifestUrl('linux')
    );
  });

  it('tells the update screen that only a Mac build cannot update itself', async () => {
    const original = Object.getOwnPropertyDescriptor(process, 'platform');
    process.env.QSDM_HIVE_REQUIRED_VERSION = '999.0.0';
    try {
      for (const [platform, selfUpdates] of [
        ['darwin', false],
        ['linux', true],
        ['win32', true],
      ] as const) {
        Object.defineProperty(process, 'platform', { value: platform });
        // eslint-disable-next-line no-await-in-loop
        const status = await getHiveVersionPolicyStatus({ forceRefresh: true });
        expect(status.updateRequired).toBe(true);
        expect(status.selfUpdates).toBe(selfUpdates);
      }
    } finally {
      if (original) Object.defineProperty(process, 'platform', original);
    }
  });

  it('accepts only the exact required Hive version', async () => {
    process.env.QSDM_HIVE_REQUIRED_VERSION = getCurrentHiveVersion();
    const status = await getHiveVersionPolicyStatus({ forceRefresh: true });

    expect(status.compatible).toBe(true);
    expect(status.updateRequired).toBe(false);
    expect(status.reason).toBe('current');
  });

  it('blocks Hive builds older than the required version', async () => {
    process.env.QSDM_HIVE_REQUIRED_VERSION = '999.0.0';
    const status = await getHiveVersionPolicyStatus({ forceRefresh: true });

    expect(status.compatible).toBe(false);
    expect(status.updateRequired).toBe(true);
    expect(status.reason).toBe('version-mismatch');
  });

  it('blocks Hive builds newer than the required version', async () => {
    process.env.QSDM_HIVE_REQUIRED_VERSION = '0.0.1';
    const status = await getHiveVersionPolicyStatus({ forceRefresh: true });

    expect(status.compatible).toBe(false);
    expect(status.updateRequired).toBe(true);
    expect(status.reason).toBe('version-mismatch');
  });

  it('does not allow local policy overrides in production runtimes', () => {
    process.env.NODE_ENV = 'production';
    process.env.QSDM_HIVE_DISABLE_VERSION_POLICY = '1';
    process.env.QSDM_HIVE_REQUIRED_VERSION = '999.0.0';
    process.env.QSDM_HIVE_VERSION_MANIFEST_URL =
      'https://example.invalid/latest.yml';

    expect(canUseLocalHiveVersionPolicyOverrides()).toBe(false);
    // Pin the platform: the default manifest name depends on the OS (see the
    // next test), and this check must not depend on the host running jest.
    expect(getHiveVersionManifestUrl('win32')).toBe(
      'https://qsdm.tech/downloads/hive-v2/latest.yml'
    );
  });

  it('uses the platform-specific electron-builder manifest', () => {
    expect(getDefaultHiveVersionManifestUrl('win32', '1.3.95')).toBe(
      'https://qsdm.tech/downloads/hive-v2/latest.yml'
    );
    expect(getDefaultHiveVersionManifestUrl('linux', '1.3.95')).toBe(
      'https://qsdm.tech/downloads/hive-v2/latest-linux.yml'
    );
    expect(getDefaultHiveVersionManifestUrl('darwin', '1.3.95')).toBe(
      'https://qsdm.tech/downloads/hive-v2/latest-mac.yml'
    );
  });

  it('isolates unsigned previews from the production release manifest', () => {
    const previewVersion = '1.3.95-unsigned-preview.1';

    expect(isUnsignedPreviewHiveVersion(previewVersion)).toBe(true);
    expect(isUnsignedPreviewHiveVersion('1.3.95')).toBe(false);
    expect(getDefaultHiveVersionManifestUrl('win32', previewVersion)).toBe(
      'https://qsdm.tech/downloads/unsigned-preview/latest.yml'
    );
  });

  it('parses electron-builder latest.yml release manifests', () => {
    const manifest = parseHiveReleaseManifest(
      [
        'version: 1.3.46',
        'files:',
        '  - url: qsdm-hive-1.3.46-win-x64.exe',
        'path: qsdm-hive-1.3.46-win-x64.exe',
      ].join('\n'),
      'https://qsdm.tech/downloads/hive-v2/latest.yml'
    );

    expect(manifest).toEqual({
      version: '1.3.46',
      downloadUrl:
        'https://qsdm.tech/downloads/hive-v2/qsdm-hive-1.3.46-win-x64.exe',
    });
  });
});
