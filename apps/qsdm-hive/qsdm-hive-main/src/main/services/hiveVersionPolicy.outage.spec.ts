/**
 * @jest-environment node
 */

// The version policy end to end through a release feed outage: the real
// policy, release verification and saved-envelope code, with only the HTTPS
// requests and the bundled qsdmcli signature check replaced.

import { spawnSync } from 'child_process';
import crypto from 'crypto';
import { EventEmitter } from 'events';
import fs from 'fs';
import https from 'https';
import os from 'os';
import path from 'path';

import { Endpoints } from 'config/endpoints';

import { app } from '../app';

import {
  assertHiveVersionPolicyAllowsEndpoint,
  getHiveVersionPolicyStatus,
  resetHiveVersionPolicyCacheForTests,
} from './hiveVersionPolicy';
import {
  getQsdmHiveReleaseEnvelopeCachePath,
  RELEASE_ENVELOPE_CACHE_FOLDER_NAME,
} from './qsdmReleaseEnvelopeCache';
import {
  getQsdmReleaseTrustKey,
  resetVerifiedQsdmHiveReleaseCacheForTests,
} from './qsdmReleaseManifest';

jest.mock('https', () => ({ get: jest.fn() }));
jest.mock('child_process', () => ({ spawnSync: jest.fn() }));

// Synthetic trust root of the right shape, as in qsdmReleaseManifest.spec.ts;
// not a real signing key.
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
const INSTALLED_VERSION = '1.4.22';
const DAY_MS = 24 * 60 * 60 * 1000;
const daysFromNow = (days: number) => new Date(Date.now() + days * DAY_MS);

// Signed updates exist for Windows and Linux only.
const releasePlatform = process.platform === 'win32' ? 'windows' : 'linux';
const describeOnReleasePlatform = ['win32', 'linux'].includes(process.platform)
  ? describe
  : describe.skip;

// Stand-in for an ML-DSA-87 signature, checked by the qsdmcli stand-in below.
const fakeSignature = (manifestBytes: Buffer) =>
  crypto
    .createHash('sha512')
    .update(manifestBytes)
    .digest('hex')
    .repeat(73)
    .slice(0, 9254);

type SignedRelease = {
  envelope: Buffer;
  updaterName: string;
  updaterMetadata: Buffer;
  installerName: string;
};

function signRelease({
  version,
  issuedAt = daysFromNow(-1),
  expiresAt = new Date(issuedAt.getTime() + 90 * DAY_MS),
}: {
  version: string;
  issuedAt?: Date;
  expiresAt?: Date;
}): SignedRelease {
  const installerName =
    releasePlatform === 'windows'
      ? `qsdm-hive-${version}-win-x64.exe`
      : `qsdm-hive-${version}-linux-x64.AppImage`;
  const updaterName =
    releasePlatform === 'windows' ? 'latest.yml' : 'latest-linux.yml';
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
  ) => ({
    name,
    platform: releasePlatform,
    role,
    size: bytes.length,
    sha256: crypto.createHash('sha256').update(bytes).digest('hex'),
  });
  const manifestBytes = Buffer.from(
    JSON.stringify({
      schema: 'qsdm.release-manifest.v1',
      product: 'qsdm-hive',
      channel: 'stable',
      platform: releasePlatform,
      version,
      commit: 'c'.repeat(40),
      issued_at: issuedAt.toISOString(),
      expires_at: expiresAt.toISOString(),
      key_id: getQsdmReleaseTrustKey().key_id,
      artifacts: [
        artifact(updaterName, 'updater-manifest', updaterMetadata),
        artifact(installerName, 'installer', Buffer.from(version)),
      ],
    })
  );
  const envelope = Buffer.from(
    JSON.stringify({
      schema: 'qsdm.signed-release.v1',
      algorithm: 'ML-DSA-87',
      key_id: getQsdmReleaseTrustKey().key_id,
      manifest_base64: manifestBytes.toString('base64'),
      signature: fakeSignature(manifestBytes),
    })
  );
  return { envelope, updaterName, updaterMetadata, installerName };
}

type FeedReply = { status: number; body?: Buffer } | { error: Error };
// URL -> reply. A URL with no reply is refused, as during an outage.
const feed = new Map<string, FeedReply>();

const serve = (release: SignedRelease) => {
  feed.clear();
  feed.set(`${BASE_URL}/qsdm-hive-release-${releasePlatform}-v2.json`, {
    status: 200,
    body: release.envelope,
  });
  feed.set(`${BASE_URL}/${release.updaterName}`, {
    status: 200,
    body: release.updaterMetadata,
  });
};

const OUTAGES: Array<[string, () => void]> = [
  ['the connection is refused', () => feed.clear()],
  [
    'the website answers 503',
    () => {
      feed.clear();
      feed.set(`${BASE_URL}/qsdm-hive-release-${releasePlatform}-v2.json`, {
        status: 503,
      });
    },
  ],
];

const fakeHttpsGet = (
  url: URL,
  _options: unknown,
  onResponse: (response: EventEmitter) => void
) => {
  const request = Object.assign(new EventEmitter(), {
    destroy(error?: Error) {
      if (error) request.emit('error', error);
    },
  });
  const reply = feed.get(url.toString()) ?? {
    error: Object.assign(new Error(`connect ECONNREFUSED ${url.host}`), {
      code: 'ECONNREFUSED',
    }),
  };
  process.nextTick(() => {
    if ('error' in reply) {
      request.emit('error', reply.error);
      return;
    }
    const response = Object.assign(new EventEmitter(), {
      statusCode: reply.status,
      headers: {},
      resume: () => undefined,
    });
    onResponse(response);
    if (reply.body) response.emit('data', reply.body);
    response.emit('end');
  });
  return request;
};

// The bundled `qsdmcli wallet verify`, reduced to the stand-in signature.
const fakeQsdmCliVerify = (
  _cliPath: string,
  args: string[],
  options: { input: Buffer }
) => {
  const publicKey = args[args.indexOf('--public-key') + 1];
  const signature = args[args.indexOf('--signature') + 1];
  const valid =
    publicKey === getQsdmReleaseTrustKey().public_key &&
    signature === fakeSignature(options.input);
  return valid
    ? { status: 0, stdout: 'signature valid', stderr: '' }
    : { status: 1, stdout: '', stderr: 'signature is invalid' };
};

describeOnReleasePlatform(
  'Hive version policy during a release feed outage',
  () => {
    const originalEnv = process.env;
    const originalResourcesPath = Object.getOwnPropertyDescriptor(
      process,
      'resourcesPath'
    );
    let tempRoot: string;
    let userData: string;
    let warn: jest.SpyInstance;

    const savedEnvelopePath = () =>
      getQsdmHiveReleaseEnvelopeCachePath(
        path.join(userData, RELEASE_ENVELOPE_CACHE_FOLDER_NAME),
        process.platform,
        BASE_URL
      );

    const checkPolicy = () =>
      getHiveVersionPolicyStatus({ forceRefresh: true });

    beforeEach(() => {
      resetHiveVersionPolicyCacheForTests();
      resetVerifiedQsdmHiveReleaseCacheForTests();
      process.env = { ...originalEnv };
      delete process.env.QSDM_HIVE_DISABLE_VERSION_POLICY;
      delete process.env.QSDM_HIVE_REQUIRED_VERSION;
      delete process.env.QSDM_HIVE_RELEASE_MANIFEST_URL;
      delete process.env.QSDM_HIVE_DOWNLOAD_URL;
      // Turns the policy on under jest and points it at the test feed.
      process.env.QSDM_HIVE_VERSION_MANIFEST_URL = `${BASE_URL}/latest.yml`;
      process.env.QSDM_HIVE_UPDATE_BASE_URL = BASE_URL;

      tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-hive-outage-'));
      userData = path.join(tempRoot, 'userData');
      // The packaged app's resources folder, holding a stand-in qsdmcli.
      const resources = path.join(tempRoot, 'resources');
      fs.mkdirSync(path.join(resources, 'native'), { recursive: true });
      fs.writeFileSync(
        path.join(
          resources,
          'native',
          process.platform === 'win32' ? 'qsdmcli.exe' : 'qsdmcli'
        ),
        ''
      );
      Object.defineProperty(process, 'resourcesPath', {
        value: resources,
        configurable: true,
        writable: true,
      });

      jest.spyOn(app, 'getPath').mockImplementation(() => userData);
      jest.spyOn(app, 'getVersion').mockReturnValue(INSTALLED_VERSION);
      (https.get as unknown as jest.Mock).mockImplementation(fakeHttpsGet);
      (spawnSync as unknown as jest.Mock).mockImplementation(fakeQsdmCliVerify);
      feed.clear();
      warn = jest.spyOn(console, 'warn').mockImplementation(() => undefined);
    });

    afterEach(() => {
      warn.mockRestore();
      (spawnSync as unknown as jest.Mock).mockReset();
      (https.get as unknown as jest.Mock).mockReset();
      if (originalResourcesPath) {
        Object.defineProperty(process, 'resourcesPath', originalResourcesPath);
      } else {
        delete (process as { resourcesPath?: string }).resourcesPath;
      }
      process.env = originalEnv;
      fs.rmSync(tempRoot, { recursive: true, force: true });
    });

    it.each(OUTAGES)(
      'stays compatible when %s and the saved release matches this Hive',
      async (_outage, startOutage) => {
        serve(signRelease({ version: INSTALLED_VERSION }));
        const online = await checkPolicy();
        expect(online).toEqual(
          expect.objectContaining({ compatible: true, reason: 'current' })
        );
        expect(online.fromCache).toBeUndefined();
        expect(fs.existsSync(savedEnvelopePath())).toBe(true);

        startOutage();
        (spawnSync as unknown as jest.Mock).mockClear();
        const offline = await checkPolicy();

        expect(offline).toEqual(
          expect.objectContaining({
            compatible: true,
            updateRequired: false,
            reason: 'current',
            requiredVersion: INSTALLED_VERSION,
            fromCache: true,
            downloadUrl: `${BASE_URL}/qsdm-hive-${INSTALLED_VERSION}-${
              releasePlatform === 'windows'
                ? 'win-x64.exe'
                : 'linux-x64.AppImage'
            }`,
          })
        );
        expect(offline.error).toMatch(/ECONNREFUSED|status 503/);
        // The saved envelope went through the bundled verifier again.
        expect(spawnSync).toHaveBeenCalledTimes(1);
        await expect(
          assertHiveVersionPolicyAllowsEndpoint(Endpoints.START_TASK)
        ).resolves.toBeUndefined();
      }
    );

    it.each(OUTAGES)(
      'still requires the update when %s and the saved release is newer',
      async (_outage, startOutage) => {
        serve(signRelease({ version: '1.4.23' }));
        expect(await checkPolicy()).toEqual(
          expect.objectContaining({
            compatible: false,
            reason: 'version-mismatch',
          })
        );

        startOutage();
        const offline = await checkPolicy();

        expect(offline).toEqual(
          expect.objectContaining({
            compatible: false,
            updateRequired: true,
            reason: 'version-mismatch',
            currentVersion: INSTALLED_VERSION,
            requiredVersion: '1.4.23',
            fromCache: true,
          })
        );
        await expect(
          assertHiveVersionPolicyAllowsEndpoint(Endpoints.START_TASK)
        ).rejects.toThrow('Required version: 1.4.23');
      }
    );

    it('fails closed during an outage when no release was saved', async () => {
      const offline = await checkPolicy();

      expect(offline).toEqual(
        expect.objectContaining({
          compatible: false,
          updateRequired: true,
          reason: 'manifest-unavailable',
          requiredVersion: null,
        })
      );
      expect(offline.error).toContain('ECONNREFUSED');
      expect(offline.fromCache).toBeUndefined();
      await expect(
        assertHiveVersionPolicyAllowsEndpoint(Endpoints.START_TASK)
      ).rejects.toThrow('blocked by version policy');
    });

    it('fails closed during an outage when the saved release has expired', async () => {
      fs.mkdirSync(path.dirname(savedEnvelopePath()), { recursive: true });
      fs.writeFileSync(
        savedEnvelopePath(),
        signRelease({
          version: INSTALLED_VERSION,
          issuedAt: daysFromNow(-100),
          expiresAt: daysFromNow(-10),
        }).envelope
      );

      const offline = await checkPolicy();

      expect(offline).toEqual(
        expect.objectContaining({
          compatible: false,
          reason: 'manifest-unavailable',
        })
      );
      expect(offline.error).toContain('ECONNREFUSED');
      expect(offline.fromCache).toBeUndefined();
    });

    it('fails closed during an outage when the saved release was tampered with', async () => {
      serve(signRelease({ version: '1.4.23' }));
      await checkPolicy();
      // Someone edits the saved copy to require the installed version instead.
      const saved = JSON.parse(
        fs.readFileSync(savedEnvelopePath(), 'utf8')
      ) as Record<string, string>;
      const manifest = JSON.parse(
        Buffer.from(saved.manifest_base64, 'base64').toString('utf8')
      ) as { version: string };
      manifest.version = INSTALLED_VERSION;
      saved.manifest_base64 = Buffer.from(JSON.stringify(manifest)).toString(
        'base64'
      );
      fs.writeFileSync(savedEnvelopePath(), JSON.stringify(saved));

      feed.clear();
      const offline = await checkPolicy();

      expect(offline).toEqual(
        expect.objectContaining({
          compatible: false,
          reason: 'manifest-unavailable',
        })
      );
      expect(warn).toHaveBeenCalledWith(
        expect.stringContaining('signature verification failed')
      );
    });

    it('keeps requiring the newer saved release when the feed rolls back', async () => {
      serve(signRelease({ version: '1.4.23', issuedAt: daysFromNow(-1) }));
      await checkPolicy();

      // The feed goes back to an older, still valid signed release that this
      // Hive would match.
      serve(
        signRelease({ version: INSTALLED_VERSION, issuedAt: daysFromNow(-30) })
      );
      const rolledBack = await checkPolicy();

      expect(rolledBack).toEqual(
        expect.objectContaining({
          compatible: false,
          reason: 'version-mismatch',
          requiredVersion: '1.4.23',
          fromCache: true,
        })
      );
      expect(rolledBack.error).toContain('older than the one Hive saved');
      expect(warn).toHaveBeenCalledWith(
        expect.stringContaining('refused a release rollback')
      );
    });
  }
);
