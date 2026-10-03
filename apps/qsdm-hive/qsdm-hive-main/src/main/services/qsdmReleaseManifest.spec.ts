import crypto from 'crypto';

import {
  getQsdmHiveReleaseManifestUrl,
  getQsdmReleaseTrustKey,
  getVerifiedQsdmHiveRelease,
  parseAndValidateQsdmHiveReleaseManifest,
  QSDM_HIVE_RELEASE_BASE_URL,
  QsdmReleaseArtifact,
  resetVerifiedQsdmHiveReleaseCacheForTests,
} from './qsdmReleaseManifest';

const TRUST_ROOT_MODULE =
  '../../../../../../QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json';

// The repository ships a placeholder trust root until the operator drops in
// the new public key. Tests use synthetic public key bytes of the right shape
// (signature checks are stubbed); they are not a real signing key.
jest.mock(
  '../../../../../../QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json',
  () => {
    // eslint-disable-next-line global-require, @typescript-eslint/no-var-requires
    const nodeCrypto = require('crypto');
    const publicKey = nodeCrypto
      .createHash('sha512')
      .update('qsdm-test-only')
      .digest()
      .toString('hex')
      .repeat(41)
      .slice(0, 5184);
    const keyId = nodeCrypto
      .createHash('sha256')
      .update(Buffer.from(publicKey, 'hex'))
      .digest('hex');
    return {
      schema: 'qsdm.release-trust-key.v1',
      key_id: keyId,
      algorithm: 'ML-DSA-87',
      public_key: publicKey,
      address: keyId,
      created_at: '2026-10-04T00:00:00.000Z',
    };
  }
);

const updaterMetadata = Buffer.from(
  [
    'version: 1.3.96',
    'files:',
    '  - url: qsdm-hive-1.3.96-win-x64.exe',
    'path: qsdm-hive-1.3.96-win-x64.exe',
  ].join('\n')
);

const artifact = (
  name: string,
  role: QsdmReleaseArtifact['role'],
  bytes: Buffer
) => ({
  name,
  platform: 'windows' as const,
  role,
  size: bytes.length,
  sha256: crypto.createHash('sha256').update(bytes).digest('hex'),
});

const buildManifest = (expiresAt = '2026-09-01T00:00:00.000Z') => ({
  schema: 'qsdm.release-manifest.v1' as const,
  product: 'qsdm-hive' as const,
  channel: 'stable' as const,
  platform: 'windows' as const,
  version: '1.3.96',
  commit: 'a'.repeat(40),
  issued_at: '2026-07-18T00:00:00.000Z',
  expires_at: expiresAt,
  key_id: getQsdmReleaseTrustKey().key_id,
  artifacts: [
    artifact('latest.yml', 'updater-manifest', updaterMetadata),
    artifact(
      'qsdm-hive-1.3.96-win-x64.exe',
      'installer',
      Buffer.from('installer')
    ),
  ],
});

describe('QSDM signed release manifest', () => {
  beforeEach(() => resetVerifiedQsdmHiveReleaseCacheForTests());

  it('derives the pinned key ID from the ML-DSA-87 public key', () => {
    const trustKey = getQsdmReleaseTrustKey();
    expect(
      crypto
        .createHash('sha256')
        .update(Buffer.from(trustKey.public_key, 'hex'))
        .digest('hex')
    ).toBe(trustKey.key_id);
  });

  it('accepts a current manifest from the pinned release key', () => {
    const manifest = buildManifest();
    expect(
      parseAndValidateQsdmHiveReleaseManifest(
        Buffer.from(JSON.stringify(manifest)),
        'windows',
        new Date('2026-07-19T00:00:00.000Z')
      ).version
    ).toBe('1.3.96');
  });

  it('rejects an expired signed manifest', () => {
    const manifest = buildManifest('2026-07-18T12:00:00.000Z');
    expect(() =>
      parseAndValidateQsdmHiveReleaseManifest(
        Buffer.from(JSON.stringify(manifest)),
        'windows',
        new Date('2026-07-19T00:00:00.000Z')
      )
    ).toThrow('expired');
  });

  it('rejects an unrecognized artifact role', () => {
    const manifest = buildManifest();
    (manifest.artifacts[0] as { role: string }).role = 'executable-script';
    expect(() =>
      parseAndValidateQsdmHiveReleaseManifest(
        Buffer.from(JSON.stringify(manifest)),
        'windows',
        new Date('2026-07-19T00:00:00.000Z')
      )
    ).toThrow('invalid role');
  });

  it('accepts an authenticated wallet extension using stable artifact roles', () => {
    const manifest = buildManifest();
    manifest.artifacts.push(
      artifact(
        'qsdm-hive-wallet-extension-0.3.0.zip',
        'portable-archive',
        Buffer.from('extension')
      ),
      artifact(
        'qsdm-hive-wallet-extension-0.3.0-SHA256SUMS.txt',
        'checksums',
        Buffer.from('extension checksum')
      )
    );

    const parsed = parseAndValidateQsdmHiveReleaseManifest(
      Buffer.from(JSON.stringify(manifest)),
      'windows',
      new Date('2026-07-19T00:00:00.000Z')
    );

    expect(parsed.artifacts).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          name: 'qsdm-hive-wallet-extension-0.3.0.zip',
          role: 'portable-archive',
        }),
        expect.objectContaining({
          name: 'qsdm-hive-wallet-extension-0.3.0-SHA256SUMS.txt',
          role: 'checksums',
        }),
      ])
    );
  });

  it('rejects updater metadata that differs from the signed hash', async () => {
    const manifestBytes = Buffer.from(JSON.stringify(buildManifest()));
    const envelope = Buffer.from(
      JSON.stringify({
        schema: 'qsdm.signed-release.v1',
        algorithm: 'ML-DSA-87',
        key_id: getQsdmReleaseTrustKey().key_id,
        manifest_base64: manifestBytes.toString('base64'),
        signature: '00'.repeat(4627),
      })
    );
    const fetchBytes = jest
      .fn()
      .mockResolvedValueOnce(envelope)
      .mockResolvedValueOnce(Buffer.from('version: 9.9.9'));

    await expect(
      getVerifiedQsdmHiveRelease({
        platform: 'win32',
        baseUrl: 'https://qsdm.test/downloads',
        forceRefresh: true,
        dependencies: {
          fetchBytes,
          verifySignature: async () => undefined,
          now: new Date('2026-07-19T00:00:00.000Z'),
        },
      })
    ).rejects.toThrow('size does not match');
  });

  it('reads the v2 envelopes from the v2 release directory', () => {
    expect(QSDM_HIVE_RELEASE_BASE_URL).toBe(
      'https://qsdm.tech/downloads/hive-v2'
    );
    expect(getQsdmHiveReleaseManifestUrl('win32')).toBe(
      'https://qsdm.tech/downloads/hive-v2/qsdm-hive-release-windows-v2.json'
    );
    expect(getQsdmHiveReleaseManifestUrl('linux')).toBe(
      'https://qsdm.tech/downloads/hive-v2/qsdm-hive-release-linux-v2.json'
    );
  });

  it('resolves latest.yml and the installer inside the v2 directory', async () => {
    const manifestBytes = Buffer.from(JSON.stringify(buildManifest()));
    const envelope = Buffer.from(
      JSON.stringify({
        schema: 'qsdm.signed-release.v1',
        algorithm: 'ML-DSA-87',
        key_id: getQsdmReleaseTrustKey().key_id,
        manifest_base64: manifestBytes.toString('base64'),
        signature: '00'.repeat(4627),
      })
    );
    const fetchBytes = jest
      .fn()
      .mockResolvedValueOnce(envelope)
      .mockResolvedValueOnce(updaterMetadata);

    const release = await getVerifiedQsdmHiveRelease({
      platform: 'win32',
      forceRefresh: true,
      dependencies: {
        fetchBytes,
        verifySignature: async () => undefined,
        now: new Date('2026-07-19T00:00:00.000Z'),
      },
    });

    expect(fetchBytes.mock.calls.map((call) => call[0])).toEqual([
      'https://qsdm.tech/downloads/hive-v2/qsdm-hive-release-windows-v2.json',
      'https://qsdm.tech/downloads/hive-v2/latest.yml',
    ]);
    expect(release.installerUrl).toBe(
      'https://qsdm.tech/downloads/hive-v2/qsdm-hive-1.3.96-win-x64.exe'
    );
  });

  it('rejects an envelope signed by the previous (v1) release key', async () => {
    const v1KeyId =
      '10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1';
    const manifest = { ...buildManifest(), key_id: v1KeyId };
    const envelope = Buffer.from(
      JSON.stringify({
        schema: 'qsdm.signed-release.v1',
        algorithm: 'ML-DSA-87',
        key_id: v1KeyId,
        manifest_base64: Buffer.from(JSON.stringify(manifest)).toString(
          'base64'
        ),
        signature: '00'.repeat(4627),
      })
    );
    const verifySignature = jest.fn(async () => undefined);

    await expect(
      getVerifiedQsdmHiveRelease({
        platform: 'win32',
        forceRefresh: true,
        dependencies: {
          fetchBytes: jest.fn().mockResolvedValueOnce(envelope),
          verifySignature,
          now: new Date('2026-07-19T00:00:00.000Z'),
        },
      })
    ).rejects.toThrow('envelope identity is invalid');
    expect(verifySignature).not.toHaveBeenCalled();
  });

  it('fails closed without network access when built from the placeholder trust root', async () => {
    const fetchBytes = jest.fn();
    // resetModules drops the already-instantiated synthetic trust root mock.
    jest.resetModules();
    jest.doMock(TRUST_ROOT_MODULE, () => ({
      schema: 'qsdm.release-trust-key.v1',
      placeholder: true,
      key_id: '',
      algorithm: 'ML-DSA-87',
      public_key: '',
      address: '',
      created_at: '',
    }));
    // eslint-disable-next-line global-require
    const isolated: typeof import('./qsdmReleaseManifest') = require('./qsdmReleaseManifest');

    expect(() => isolated.getQsdmReleaseTrustKey()).toThrow(
      'placeholder trust root'
    );
    await expect(
      isolated.getVerifiedQsdmHiveRelease({
        platform: 'win32',
        forceRefresh: true,
        dependencies: { fetchBytes, verifySignature: async () => undefined },
      })
    ).rejects.toThrow('placeholder trust root');
    expect(fetchBytes).not.toHaveBeenCalled();
  });
});
