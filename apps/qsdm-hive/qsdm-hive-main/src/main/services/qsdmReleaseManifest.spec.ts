import crypto from 'crypto';

import {
  getQsdmReleaseTrustKey,
  getVerifiedQsdmHiveRelease,
  parseAndValidateQsdmHiveReleaseManifest,
  QsdmReleaseArtifact,
  resetVerifiedQsdmHiveReleaseCacheForTests,
} from './qsdmReleaseManifest';

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
        'qsdm-hive-wallet-extension-0.2.0.zip',
        'portable-archive',
        Buffer.from('extension')
      ),
      artifact(
        'qsdm-hive-wallet-extension-0.2.0-SHA256SUMS.txt',
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
          name: 'qsdm-hive-wallet-extension-0.2.0.zip',
          role: 'portable-archive',
        }),
        expect.objectContaining({
          name: 'qsdm-hive-wallet-extension-0.2.0-SHA256SUMS.txt',
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
});
