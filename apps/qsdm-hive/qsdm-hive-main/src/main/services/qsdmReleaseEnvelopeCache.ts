/**
 * The saved signed release envelope, so that a website outage does not lock
 * QSDM Hive.
 *
 * After every successful check of the release feed, Hive saves the exact
 * envelope bytes it verified, one file per platform and release base URL, in
 * `<userData>/release-envelope-cache/`. When a later check fails (offline, a
 * DNS failure, a timeout, a server error, or an envelope that does not
 * verify), Hive reads that file and verifies it again in full: the pinned
 * key, the ML-DSA-87 signature, the schema, product, channel and platform,
 * and the signed validity window with the usual clock skew. A copy that
 * passes is used exactly as a fresh envelope would be, so a version mismatch
 * still requires the update. The envelope's own `expires_at` is the only time
 * limit; there is no other grace period. A missing, corrupt, tampered or
 * expired copy is ignored, and the check fails closed with the feed's error,
 * as before.
 *
 * Rollback protection: an envelope from the feed that verifies but was issued
 * before the saved, still valid envelope is ignored, and the saved one stays
 * in use. A renewal (the same version signed again) and a new release are
 * always issued later, so they replace the saved copy.
 */
import crypto from 'crypto';
import fs from 'fs';
import path from 'path';
import { URL } from 'url';

import {
  getQsdmHiveReleaseManifestUrl,
  getVerifiedQsdmHiveRelease,
  QSDM_HIVE_RELEASE_ENVELOPE_MAX_BYTES,
  VerificationDependencies,
  VerifiedQsdmHiveRelease,
  VerifiedQsdmHiveReleaseEnvelope,
  verifyQsdmHiveReleaseEnvelope,
} from './qsdmReleaseManifest';

export const RELEASE_ENVELOPE_CACHE_FOLDER_NAME = 'release-envelope-cache';

export type QsdmHiveReleaseForVersionPolicy =
  VerifiedQsdmHiveReleaseEnvelope & {
    // 'network': the envelope the feed sent just now.
    // 'cache': the saved envelope, verified again just now.
    source: 'network' | 'cache';
    // Why the feed's envelope was not used, when source is 'cache'.
    feedError?: string;
  };

export type ReleaseEnvelopeCacheOptions = {
  platform?: NodeJS.Platform;
  baseUrl: string;
  // The folder for saved envelopes. Without one, nothing is saved or read.
  cacheDirectory: string | null;
  qsdmCliPath?: string;
  dependencies?: VerificationDependencies;
};

type SavedEnvelopeBytes = {
  bytes?: Buffer;
  // Why the saved file cannot be used. Absent when there is no saved file.
  problem?: string;
};

type SavedRelease = {
  release?: VerifiedQsdmHiveReleaseEnvelope;
  problem?: string;
};

const checksInFlight = new Map<
  string,
  Promise<QsdmHiveReleaseForVersionPolicy>
>();

export function getQsdmHiveReleaseEnvelopeCachePath(
  cacheDirectory: string,
  platform: NodeJS.Platform,
  baseUrl: string
) {
  const manifestUrl = getQsdmHiveReleaseManifestUrl(platform, baseUrl);
  const envelopeName = path.posix.basename(
    new URL(manifestUrl).pathname,
    '.json'
  );
  const urlHash = crypto
    .createHash('sha256')
    .update(manifestUrl)
    .digest('hex')
    .slice(0, 16);
  return path.join(cacheDirectory, `${envelopeName}-${urlHash}.json`);
}

/**
 * The release the version policy enforces: the feed's envelope when it
 * verifies (it is then saved), otherwise the saved envelope when it still
 * verifies. When neither can be used, this throws the feed's own error.
 */
export function getQsdmHiveReleaseWithEnvelopeCache(
  options: ReleaseEnvelopeCacheOptions
): Promise<QsdmHiveReleaseForVersionPolicy> {
  const cachePath = resolveCachePath(options);
  if (!cachePath) {
    return checkRelease(options, null);
  }
  // Overlapping checks share one run, so that two of them cannot interleave
  // their reads and writes of the same saved file.
  const running = checksInFlight.get(cachePath);
  if (running) {
    return running;
  }
  const check = checkRelease(options, cachePath).finally(() => {
    checksInFlight.delete(cachePath);
  });
  checksInFlight.set(cachePath, check);
  return check;
}

function resolveCachePath({
  cacheDirectory,
  platform = process.platform,
  baseUrl,
}: ReleaseEnvelopeCacheOptions) {
  if (!cacheDirectory) {
    return null;
  }
  try {
    return getQsdmHiveReleaseEnvelopeCachePath(
      cacheDirectory,
      platform,
      baseUrl
    );
  } catch {
    // An unsupported platform or an invalid URL; the feed check reports it.
    return null;
  }
}

async function checkRelease(
  options: ReleaseEnvelopeCacheOptions,
  cachePath: string | null
): Promise<QsdmHiveReleaseForVersionPolicy> {
  const {
    platform = process.platform,
    baseUrl,
    qsdmCliPath,
    dependencies = {},
  } = options;

  let fresh: VerifiedQsdmHiveRelease;
  try {
    fresh = await getVerifiedQsdmHiveRelease({
      platform,
      baseUrl,
      forceRefresh: true,
      qsdmCliPath,
      dependencies,
    });
  } catch (error) {
    if (!cachePath) {
      throw error;
    }
    const saved = await loadSavedRelease(cachePath, options);
    if (!saved.release) {
      if (saved.problem) {
        console.warn(
          `QSDM Hive cannot use its saved release envelope ${cachePath}: ${saved.problem}`
        );
      }
      throw error;
    }
    const feedError = describeError(error);
    const { manifest } = saved.release;
    console.warn(
      `QSDM Hive could not verify the release feed (${feedError}). It is using the signed release it saved earlier: Hive ${manifest.version}, issued ${manifest.issued_at}, valid until ${manifest.expires_at}.`
    );
    return { ...saved.release, source: 'cache', feedError };
  }

  const fromFeed: QsdmHiveReleaseForVersionPolicy = {
    manifest: fresh.manifest,
    manifestUrl: fresh.manifestUrl,
    installerUrl: fresh.installerUrl,
    envelope: fresh.envelope,
    source: 'network',
  };
  if (!cachePath) {
    return fromFeed;
  }

  const savedBytes = await readSavedEnvelopeBytes(cachePath);
  if (savedBytes.bytes && savedBytes.bytes.equals(fresh.envelope)) {
    return fromFeed;
  }
  const saved: SavedRelease = savedBytes.bytes
    ? await verifySavedEnvelope(savedBytes.bytes, options)
    : { problem: savedBytes.problem };
  if (
    saved.release &&
    Date.parse(fresh.manifest.issued_at) <
      Date.parse(saved.release.manifest.issued_at)
  ) {
    const feedError = `The release feed sent a signed release issued at ${fresh.manifest.issued_at} (Hive ${fresh.manifest.version}), older than the one Hive saved, issued at ${saved.release.manifest.issued_at} (Hive ${saved.release.manifest.version}). Hive ignored the older release and kept the saved one.`;
    console.warn(`QSDM Hive refused a release rollback. ${feedError}`);
    return { ...saved.release, source: 'cache', feedError };
  }
  if (saved.problem) {
    console.warn(
      `QSDM Hive is replacing its saved release envelope ${cachePath}: ${saved.problem}`
    );
  }
  await saveEnvelope(cachePath, fresh.envelope);
  return fromFeed;
}

async function loadSavedRelease(
  cachePath: string,
  options: ReleaseEnvelopeCacheOptions
): Promise<SavedRelease> {
  const saved = await readSavedEnvelopeBytes(cachePath);
  if (!saved.bytes) {
    return { problem: saved.problem };
  }
  return verifySavedEnvelope(saved.bytes, options);
}

async function readSavedEnvelopeBytes(
  cachePath: string
): Promise<SavedEnvelopeBytes> {
  try {
    const stat = await fs.promises.stat(cachePath);
    if (!stat.isFile()) {
      return { problem: 'it is not a file.' };
    }
    if (stat.size > QSDM_HIVE_RELEASE_ENVELOPE_MAX_BYTES) {
      return { problem: 'it is larger than a release envelope can be.' };
    }
    return { bytes: await fs.promises.readFile(cachePath) };
  } catch (error) {
    if ((error as NodeJS.ErrnoException | null)?.code === 'ENOENT') {
      return {};
    }
    return { problem: `it could not be read (${describeError(error)}).` };
  }
}

async function verifySavedEnvelope(
  bytes: Buffer,
  {
    platform = process.platform,
    baseUrl,
    qsdmCliPath,
    dependencies = {},
  }: ReleaseEnvelopeCacheOptions
): Promise<SavedRelease> {
  try {
    return {
      release: await verifyQsdmHiveReleaseEnvelope(bytes, {
        platform,
        baseUrl,
        qsdmCliPath,
        dependencies,
      }),
    };
  } catch (error) {
    return { problem: `it did not verify (${describeError(error)}).` };
  }
}

// Writes a temporary file next to the saved one, flushes it to disk and
// renames it over the saved one, so a partly written envelope never exists
// under the saved name. A failure is logged and otherwise ignored: the saved
// copy only ever adds a fallback.
async function saveEnvelope(cachePath: string, envelope: Buffer) {
  const temporaryPath = `${cachePath}.tmp-${process.pid}-${crypto
    .randomBytes(6)
    .toString('hex')}`;
  try {
    await fs.promises.mkdir(path.dirname(cachePath), {
      recursive: true,
      mode: 0o700,
    });
    const file = await fs.promises.open(temporaryPath, 'wx', 0o600);
    try {
      await file.writeFile(envelope);
      await file.sync();
    } finally {
      await file.close();
    }
    await fs.promises.rename(temporaryPath, cachePath);
  } catch (error) {
    console.warn(
      `QSDM Hive could not save the verified release envelope to ${cachePath}: ${describeError(
        error
      )}`
    );
    await fs.promises.unlink(temporaryPath).catch(() => undefined);
  }
}

function describeError(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
