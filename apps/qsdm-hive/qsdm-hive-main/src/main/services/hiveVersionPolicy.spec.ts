import {
  canUseLocalHiveVersionPolicyOverrides,
  getDefaultHiveVersionManifestUrl,
  getCurrentHiveVersion,
  getHiveVersionManifestUrl,
  getHiveVersionPolicyStatus,
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
    expect(getHiveVersionManifestUrl()).toBe(
      'https://qsdm.tech/downloads/latest.yml'
    );
  });

  it('uses the platform-specific electron-builder manifest', () => {
    expect(getDefaultHiveVersionManifestUrl('win32', '1.3.95')).toBe(
      'https://qsdm.tech/downloads/latest.yml'
    );
    expect(getDefaultHiveVersionManifestUrl('linux', '1.3.95')).toBe(
      'https://qsdm.tech/downloads/latest-linux.yml'
    );
    expect(getDefaultHiveVersionManifestUrl('darwin', '1.3.95')).toBe(
      'https://qsdm.tech/downloads/latest-mac.yml'
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
      'https://qsdm.tech/downloads/latest.yml'
    );

    expect(manifest).toEqual({
      version: '1.3.46',
      downloadUrl: 'https://qsdm.tech/downloads/qsdm-hive-1.3.46-win-x64.exe',
    });
  });
});
