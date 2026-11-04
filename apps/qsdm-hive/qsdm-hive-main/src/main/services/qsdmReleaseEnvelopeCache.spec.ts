/**
 * @jest-environment node
 */

import crypto from 'crypto';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  getQsdmHiveReleaseEnvelopeCachePath,
  getQsdmHiveReleaseWithEnvelopeCache,
} from './qsdmReleaseEnvelopeCache';
import {
  getQsdmReleaseTrustKey,
  resetVerifiedQsdmHiveReleaseCacheForTests,
} from './qsdmReleaseManifest';

// Tests replace the pinned v2 trust root with synthetic public key bytes of
// the right shape, as qsdmReleaseManifest.spec.ts does; they are not a real
// signing key.
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

const BASE_URL = 'https://qsdm.test/downloads/hive-v2';
const NOW = new Date('2026-11-04T12:00:00.000Z');
const DAY_MS = 24 * 60 * 60 * 1000;
const daysFromNow = (days: number) => new Date(NOW.getTime() + days * DAY_MS);

// A stand-in for the ML-DSA-87 signature: a digest of the exact manifest
// bytes, stretched to the signature's hex length. verifySignature recomputes
// it, so changing the signed bytes or the signature fails like a real
// signature check would.
const fakeSignature = (manifestBytes: Buffer) =>
  crypto
    .createHash('sha512')
    .update(manifestBytes)
    .digest('hex')
    .repeat(73)
    .slice(0, 9254);

const verifySignature = jest.fn(
  async (manifestBytes: Buffer, signatureHex: string) => {
    if (signatureHex !== fakeSignature(manifestBytes)) {
      throw new Error(
        'QSDM release signature verification failed: test signature mismatch'
      );
    }
  }
);

type SignedRelease = {
  envelope: Buffer;
  updaterMetadata: Buffer;
};

const sha256 = (bytes: Buffer) =>
  crypto.createHash('sha256').update(bytes).digest('hex');

function signRelease({
  version = '1.4.22',
  platform = 'windows',
  issuedAt = daysFromNow(-1),
  expiresAt = new Date(issuedAt.getTime() + 90 * DAY_MS),
}: {
  version?: string;
  platform?: 'windows' | 'linux';
  issuedAt?: Date;
  expiresAt?: Date;
} = {}): SignedRelease {
  const installerName =
    platform === 'windows'
      ? `qsdm-hive-${version}-win-x64.exe`
      : `qsdm-hive-${version}-linux-x64.AppImage`;
  const updaterMetadata = Buffer.from(
    [
      `version: ${version}`,
      'files:',
      `  - url: ${installerName}`,
      `path: ${installerName}`,
    ].join('\n')
  );
  const artifact = (
    name: string,
    role: 'updater-manifest' | 'installer',
    bytes: Buffer
  ) => ({ name, platform, role, size: bytes.length, sha256: sha256(bytes) });
  const manifestBytes = Buffer.from(
    `${JSON.stringify(
      {
        schema: 'qsdm.release-manifest.v1',
        product: 'qsdm-hive',
        channel: 'stable',
        platform,
        version,
        commit: 'b'.repeat(40),
        issued_at: issuedAt.toISOString(),
        expires_at: expiresAt.toISOString(),
        key_id: getQsdmReleaseTrustKey().key_id,
        artifacts: [
          artifact(
            platform === 'windows' ? 'latest.yml' : 'latest-linux.yml',
            'updater-manifest',
            updaterMetadata
          ),
          artifact(
            installerName,
            'installer',
            Buffer.from(`installer ${version}`)
          ),
        ],
      },
      null,
      2
    )}\n`
  );
  // Indented on purpose: the saved file must hold these exact bytes, not a
  // re-serialized copy.
  const envelope = Buffer.from(
    `${JSON.stringify(
      {
        schema: 'qsdm.signed-release.v1',
        algorithm: 'ML-DSA-87',
        key_id: getQsdmReleaseTrustKey().key_id,
        manifest_base64: manifestBytes.toString('base64'),
        signature: fakeSignature(manifestBytes),
      },
      null,
      3
    )}\n`
  );
  return { envelope, updaterMetadata };
}

// The release feed: serves one signed release, or fails every request.
const feedServing = (release: SignedRelease) =>
  jest.fn(async (url: string) => {
    if (url.endsWith('-v2.json')) return release.envelope;
    if (url.endsWith('.yml')) return release.updaterMetadata;
    throw new Error(`Unexpected release URL ${url}`);
  });

const feedDown = () =>
  jest.fn(async (url: string): Promise<Buffer> => {
    throw new Error(`getaddrinfo ENOTFOUND ${new URL(url).hostname}`);
  });

describe('saved QSDM signed release envelope', () => {
  let tempRoot: string;
  let cacheDirectory: string;
  let savedPath: string;
  let warn: jest.SpyInstance;

  const check = (
    fetchBytes: ReturnType<typeof feedServing>,
    now = NOW,
    platform: NodeJS.Platform = 'win32'
  ) =>
    getQsdmHiveReleaseWithEnvelopeCache({
      platform,
      baseUrl: BASE_URL,
      cacheDirectory,
      dependencies: { fetchBytes, verifySignature, now },
    });

  const saveDirectly = (bytes: Buffer | string) => {
    fs.mkdirSync(cacheDirectory, { recursive: true });
    fs.writeFileSync(savedPath, bytes);
  };

  beforeEach(() => {
    resetVerifiedQsdmHiveReleaseCacheForTests();
    verifySignature.mockClear();
    tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-hive-envelope-'));
    cacheDirectory = path.join(tempRoot, 'release-envelope-cache');
    savedPath = getQsdmHiveReleaseEnvelopeCachePath(
      cacheDirectory,
      'win32',
      BASE_URL
    );
    warn = jest.spyOn(console, 'warn').mockImplementation(() => undefined);
  });

  afterEach(() => {
    warn.mockRestore();
    fs.rmSync(tempRoot, { recursive: true, force: true });
  });

  it('saves the exact verified envelope bytes, then uses them during an outage', async () => {
    const release = signRelease();

    const online = await check(feedServing(release));
    expect(online.source).toBe('network');
    expect(online.feedError).toBeUndefined();
    expect(online.manifest.version).toBe('1.4.22');
    expect(fs.readFileSync(savedPath).equals(release.envelope)).toBe(true);
    // Only the saved file: the temporary file was renamed over it.
    expect(fs.readdirSync(cacheDirectory)).toEqual([path.basename(savedPath)]);

    verifySignature.mockClear();
    const offline = await check(feedDown());
    expect(offline).toEqual(
      expect.objectContaining({
        source: 'cache',
        feedError: 'getaddrinfo ENOTFOUND qsdm.test',
        manifestUrl: `${BASE_URL}/qsdm-hive-release-windows-v2.json`,
        installerUrl: `${BASE_URL}/qsdm-hive-1.4.22-win-x64.exe`,
      })
    );
    expect(offline.manifest).toEqual(online.manifest);
    // The saved envelope is verified again, signature included.
    expect(verifySignature).toHaveBeenCalledTimes(1);
  });

  it('keeps one saved envelope per platform and per release base URL', () => {
    const linuxPath = getQsdmHiveReleaseEnvelopeCachePath(
      cacheDirectory,
      'linux',
      BASE_URL
    );
    const otherFeedPath = getQsdmHiveReleaseEnvelopeCachePath(
      cacheDirectory,
      'win32',
      'https://staging.qsdm.test/downloads/hive-v2'
    );

    expect(path.dirname(savedPath)).toBe(cacheDirectory);
    expect(path.basename(savedPath)).toMatch(
      /^qsdm-hive-release-windows-v2-[0-9a-f]{16}\.json$/
    );
    expect(path.basename(linuxPath)).toMatch(
      /^qsdm-hive-release-linux-v2-[0-9a-f]{16}\.json$/
    );
    expect(new Set([savedPath, linuxPath, otherFeedPath]).size).toBe(3);
  });

  it('fails closed with the feed error when nothing was saved', async () => {
    await expect(check(feedDown())).rejects.toThrow(
      'getaddrinfo ENOTFOUND qsdm.test'
    );
    expect(fs.existsSync(cacheDirectory)).toBe(false);
  });

  it('uses the saved envelope up to its own signed expiry and not after', async () => {
    const issuedAt = daysFromNow(-1);
    const expiresAt = daysFromNow(89);
    await check(feedServing(signRelease({ issuedAt, expiresAt })));

    const justBefore = new Date(expiresAt.getTime() - 1000);
    await expect(check(feedDown(), justBefore)).resolves.toEqual(
      expect.objectContaining({ source: 'cache' })
    );

    await expect(check(feedDown(), expiresAt)).rejects.toThrow(
      'getaddrinfo ENOTFOUND qsdm.test'
    );
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('has expired'));
  });

  type Envelope = Record<string, string>;
  const tamperings: Array<[string, (envelope: Envelope) => Envelope]> = [
    [
      'its signed manifest was changed',
      (envelope) => {
        const manifest = JSON.parse(
          Buffer.from(envelope.manifest_base64, 'base64').toString('utf8')
        ) as { version: string };
        manifest.version = '1.4.99';
        return {
          ...envelope,
          manifest_base64: Buffer.from(JSON.stringify(manifest)).toString(
            'base64'
          ),
        };
      },
    ],
    [
      'its signature was changed',
      (envelope) => ({
        ...envelope,
        signature: `${
          envelope.signature.startsWith('0') ? '1' : '0'
        }${envelope.signature.slice(1)}`,
      }),
    ],
  ];

  it.each(tamperings)(
    'fails closed when the saved envelope fails its signature check: %s',
    async (_case, tamper) => {
      await check(feedServing(signRelease()));
      const saved = JSON.parse(fs.readFileSync(savedPath, 'utf8')) as Envelope;
      saveDirectly(JSON.stringify(tamper(saved)));

      await expect(check(feedDown())).rejects.toThrow(
        'getaddrinfo ENOTFOUND qsdm.test'
      );
      expect(warn).toHaveBeenCalledWith(
        expect.stringContaining('signature verification failed')
      );
    }
  );

  it.each([
    ['not JSON', 'this is not a release envelope'],
    ['cut short', signRelease().envelope.subarray(0, 200)],
    ['empty', ''],
    ['larger than any envelope', Buffer.alloc(1024 * 1024 + 1, 0x20)],
  ])('ignores a saved file that is %s', async (_case, bytes) => {
    saveDirectly(bytes);

    await expect(check(feedDown())).rejects.toThrow(
      'getaddrinfo ENOTFOUND qsdm.test'
    );

    // A verified feed envelope replaces the bad file.
    const release = signRelease();
    await expect(check(feedServing(release))).resolves.toEqual(
      expect.objectContaining({ source: 'network' })
    );
    expect(fs.readFileSync(savedPath).equals(release.envelope)).toBe(true);
  });

  it('ignores a saved file it cannot read, and never fails a good feed check over it', async () => {
    // A folder where the saved file belongs: it can be neither read nor
    // replaced.
    fs.mkdirSync(savedPath, { recursive: true });

    await expect(check(feedDown())).rejects.toThrow(
      'getaddrinfo ENOTFOUND qsdm.test'
    );
    await expect(check(feedServing(signRelease()))).resolves.toEqual(
      expect.objectContaining({ source: 'network' })
    );
    expect(warn).toHaveBeenCalledWith(
      expect.stringContaining('could not save the verified release envelope')
    );
    expect(fs.readdirSync(cacheDirectory)).toEqual([path.basename(savedPath)]);
  });

  it('never fails a good feed check when the cache folder cannot be created', async () => {
    fs.writeFileSync(cacheDirectory, 'a file where the cache folder belongs');

    await expect(check(feedServing(signRelease()))).resolves.toEqual(
      expect.objectContaining({ source: 'network' })
    );
  });

  it('ignores a saved envelope for another platform', async () => {
    saveDirectly(signRelease({ platform: 'linux' }).envelope);

    await expect(check(feedDown())).rejects.toThrow(
      'getaddrinfo ENOTFOUND qsdm.test'
    );
    expect(warn).toHaveBeenCalledWith(
      expect.stringContaining('targets linux, not windows')
    );
  });

  it.each([
    ['an older release', '1.4.21'],
    ['an older signing of the same release', '1.4.23'],
  ])(
    'refuses a rollback to %s and keeps the newer saved envelope',
    async (_case, olderVersion) => {
      const newer = signRelease({
        version: '1.4.23',
        issuedAt: daysFromNow(-1),
      });
      const older = signRelease({
        version: olderVersion,
        issuedAt: daysFromNow(-20),
      });
      await check(feedServing(newer));

      const result = await check(feedServing(older));

      expect(result.source).toBe('cache');
      expect(result.manifest.version).toBe('1.4.23');
      expect(result.manifest.issued_at).toBe(daysFromNow(-1).toISOString());
      expect(result.feedError).toContain('older than the one Hive saved');
      expect(warn).toHaveBeenCalledWith(
        expect.stringContaining('refused a release rollback')
      );
      expect(fs.readFileSync(savedPath).equals(newer.envelope)).toBe(true);
    }
  );

  it('accepts a renewal of the same release, which replaces the saved envelope', async () => {
    const first = signRelease({
      issuedAt: daysFromNow(-80),
      expiresAt: daysFromNow(10),
    });
    const renewal = signRelease({ issuedAt: daysFromNow(-1) });
    await check(feedServing(first));

    const renewed = await check(feedServing(renewal));
    expect(renewed.source).toBe('network');
    expect(renewed.manifest.issued_at).toBe(daysFromNow(-1).toISOString());
    expect(fs.readFileSync(savedPath).equals(renewal.envelope)).toBe(true);

    // The renewal, not the first signing, now carries Hive through an outage
    // after the first signing has expired.
    await expect(check(feedDown(), daysFromNow(30))).resolves.toEqual(
      expect.objectContaining({ source: 'cache' })
    );
  });

  it('accepts a newer release, which replaces the saved envelope', async () => {
    await check(feedServing(signRelease({ issuedAt: daysFromNow(-5) })));
    const next = signRelease({ version: '1.4.23', issuedAt: daysFromNow(-1) });

    const result = await check(feedServing(next));

    expect(result.source).toBe('network');
    expect(result.manifest.version).toBe('1.4.23');
    expect(fs.readFileSync(savedPath).equals(next.envelope)).toBe(true);
  });

  it('falls back to the saved envelope when the feed sends one that does not verify', async () => {
    const good = signRelease();
    await check(feedServing(good));
    const forged = signRelease({ version: '1.4.99', issuedAt: NOW });
    const forgedEnvelope = JSON.parse(
      forged.envelope.toString('utf8')
    ) as Envelope;
    forgedEnvelope.signature = fakeSignature(Buffer.from('something else'));

    const result = await check(
      feedServing({
        ...forged,
        envelope: Buffer.from(JSON.stringify(forgedEnvelope)),
      })
    );

    expect(result.source).toBe('cache');
    expect(result.manifest.version).toBe('1.4.22');
    expect(result.feedError).toContain('signature verification failed');
    expect(fs.readFileSync(savedPath).equals(good.envelope)).toBe(true);
  });

  it('falls back to the saved envelope when the updater metadata cannot be fetched', async () => {
    const release = signRelease();
    await check(feedServing(release));
    const envelopeOnly = jest.fn(async (url: string) => {
      if (url.endsWith('-v2.json')) return release.envelope;
      throw new Error('QSDM release metadata request failed with status 503.');
    });

    await expect(check(envelopeOnly)).resolves.toEqual(
      expect.objectContaining({
        source: 'cache',
        feedError: 'QSDM release metadata request failed with status 503.',
      })
    );
  });

  it('shares one check between overlapping calls', async () => {
    const feed = feedServing(signRelease());

    const [first, second] = await Promise.all([check(feed), check(feed)]);

    expect(second).toBe(first);
    // One envelope and one latest.yml request, not two of each.
    expect(feed).toHaveBeenCalledTimes(2);
  });

  it('works without a cache folder exactly as before', async () => {
    const run = (fetchBytes: ReturnType<typeof feedServing>) =>
      getQsdmHiveReleaseWithEnvelopeCache({
        platform: 'win32',
        baseUrl: BASE_URL,
        cacheDirectory: null,
        dependencies: { fetchBytes, verifySignature, now: NOW },
      });

    await expect(run(feedServing(signRelease()))).resolves.toEqual(
      expect.objectContaining({ source: 'network' })
    );
    await expect(run(feedDown())).rejects.toThrow(
      'getaddrinfo ENOTFOUND qsdm.test'
    );
  });
});
