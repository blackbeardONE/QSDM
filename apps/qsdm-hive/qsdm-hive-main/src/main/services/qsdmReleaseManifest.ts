import { spawnSync } from 'child_process';
import crypto from 'crypto';
import fs from 'fs';
import http from 'http';
import https from 'https';
import path from 'path';
import { URL } from 'url';

// This public trust root is shared deliberately with the release publisher.
// eslint-disable-next-line import/no-relative-packages
import releaseTrustKeyJson from '../../../../../../QSDM/deploy/release-trust/qsdm-hive-release-key.json';

// cspell:ignore blockmap
const DEFAULT_RELEASE_BASE_URL = 'https://qsdm.tech/downloads';
const RELEASE_CACHE_MS = 5 * 60 * 1000;
const REQUEST_TIMEOUT_MS = 12_000;
const MAX_REDIRECTS = 3;
const MAX_MANIFEST_BYTES = 1024 * 1024;
const MAX_UPDATER_MANIFEST_BYTES = 2 * 1024 * 1024;
const CLOCK_SKEW_MS = 5 * 60 * 1000;

type SupportedReleasePlatform = 'windows' | 'linux';
type ReleaseArtifactRole =
  | 'updater-manifest'
  | 'installer'
  | 'blockmap'
  | 'portable-archive'
  | 'checksums'
  | 'provenance'
  | 'evidence';

type ReleaseTrustKey = {
  schema: 'qsdm.release-trust-key.v1';
  key_id: string;
  algorithm: 'ML-DSA-87';
  public_key: string;
  address: string;
  created_at: string;
};

export type QsdmReleaseArtifact = {
  name: string;
  platform: SupportedReleasePlatform;
  role: ReleaseArtifactRole;
  size: number;
  sha256: string;
};

export type QsdmHiveReleaseManifest = {
  schema: 'qsdm.release-manifest.v1';
  product: 'qsdm-hive';
  channel: 'stable';
  platform: SupportedReleasePlatform;
  version: string;
  commit: string;
  issued_at: string;
  expires_at: string;
  key_id: string;
  artifacts: QsdmReleaseArtifact[];
};

export type VerifiedQsdmHiveRelease = {
  manifest: QsdmHiveReleaseManifest;
  manifestUrl: string;
  updaterManifestUrl: string;
  installerUrl: string;
  updaterManifest: Buffer;
  installer: QsdmReleaseArtifact;
};

type VerificationDependencies = {
  fetchBytes?: typeof fetchReleaseBytes;
  verifySignature?: (
    manifestBytes: Buffer,
    signatureHex: string,
    qsdmCliPath?: string
  ) => Promise<void>;
  now?: Date;
};

const RELEASE_ARTIFACT_ROLES = new Set<ReleaseArtifactRole>([
  'updater-manifest',
  'installer',
  'blockmap',
  'portable-archive',
  'checksums',
  'provenance',
  'evidence',
]);

const releaseTrustKey = validateReleaseTrustKey(releaseTrustKeyJson);
let cachedRelease: VerifiedQsdmHiveRelease | null = null;
let cachedReleaseUrl = '';
let cachedReleaseAt = 0;

export function resetVerifiedQsdmHiveReleaseCacheForTests() {
  cachedRelease = null;
  cachedReleaseUrl = '';
  cachedReleaseAt = 0;
}

export function getQsdmReleaseTrustKey() {
  return releaseTrustKey;
}

function validateReleaseTrustKey(value: unknown): ReleaseTrustKey {
  const key = value as Partial<ReleaseTrustKey>;
  if (
    !key ||
    key.schema !== 'qsdm.release-trust-key.v1' ||
    key.algorithm !== 'ML-DSA-87' ||
    typeof key.key_id !== 'string' ||
    !/^[0-9a-f]{64}$/.test(key.key_id) ||
    typeof key.address !== 'string' ||
    key.address !== key.key_id ||
    typeof key.public_key !== 'string' ||
    !/^[0-9a-f]{5184}$/.test(key.public_key) ||
    typeof key.created_at !== 'string' ||
    !Number.isFinite(Date.parse(key.created_at))
  ) {
    throw new Error('Pinned QSDM release trust key is invalid.');
  }
  const derivedKeyId = crypto
    .createHash('sha256')
    .update(Buffer.from(key.public_key, 'hex'))
    .digest('hex');
  if (derivedKeyId !== key.key_id) {
    throw new Error(
      'Pinned QSDM release trust key ID does not match its public key.'
    );
  }
  return key as ReleaseTrustKey;
}

export function getQsdmHiveReleaseManifestUrl(
  platform: NodeJS.Platform = process.platform,
  baseUrl = DEFAULT_RELEASE_BASE_URL
) {
  const releasePlatform = normalizePlatform(platform);
  return new URL(
    `qsdm-hive-release-${releasePlatform}.json`,
    ensureTrailingSlash(baseUrl)
  ).toString();
}

export async function getVerifiedQsdmHiveRelease({
  platform = process.platform,
  baseUrl = DEFAULT_RELEASE_BASE_URL,
  forceRefresh = false,
  qsdmCliPath,
  dependencies = {},
}: {
  platform?: NodeJS.Platform;
  baseUrl?: string;
  forceRefresh?: boolean;
  qsdmCliPath?: string;
  dependencies?: VerificationDependencies;
} = {}): Promise<VerifiedQsdmHiveRelease> {
  const manifestUrl = getQsdmHiveReleaseManifestUrl(platform, baseUrl);
  const now = dependencies.now || new Date();
  if (
    !forceRefresh &&
    cachedRelease &&
    cachedReleaseUrl === manifestUrl &&
    now.getTime() - cachedReleaseAt < RELEASE_CACHE_MS
  ) {
    return cachedRelease;
  }

  const fetchBytes = dependencies.fetchBytes || fetchReleaseBytes;
  const verifySignature =
    dependencies.verifySignature || verifyManifestWithBundledQsdmCli;
  const envelopeBytes = await fetchBytes(manifestUrl, MAX_MANIFEST_BYTES);
  const { manifestBytes, signatureHex } =
    parseSignedReleaseEnvelope(envelopeBytes);

  await verifySignature(manifestBytes, signatureHex, qsdmCliPath);
  const releasePlatform = normalizePlatform(platform);
  const manifest = parseAndValidateQsdmHiveReleaseManifest(
    manifestBytes,
    releasePlatform,
    now
  );
  const updaterArtifact = requireSingleArtifact(manifest, 'updater-manifest');
  const installer = requireSingleArtifact(manifest, 'installer');
  const updaterManifestUrl = resolveArtifactUrl(
    updaterArtifact.name,
    manifestUrl
  );
  const installerUrl = resolveArtifactUrl(installer.name, manifestUrl);
  const updaterManifest = await fetchBytes(
    updaterManifestUrl,
    MAX_UPDATER_MANIFEST_BYTES
  );
  assertArtifactBytes(updaterArtifact, updaterManifest);
  assertUpdaterManifestMatchesRelease(
    updaterManifest.toString('utf8'),
    manifest.version,
    installer.name
  );

  const verified = {
    manifest,
    manifestUrl,
    updaterManifestUrl,
    installerUrl,
    updaterManifest,
    installer,
  };
  cachedRelease = verified;
  cachedReleaseUrl = manifestUrl;
  cachedReleaseAt = now.getTime();
  return verified;
}

function parseSignedReleaseEnvelope(envelopeBytes: Buffer) {
  let envelope: {
    schema?: unknown;
    algorithm?: unknown;
    key_id?: unknown;
    manifest_base64?: unknown;
    signature?: unknown;
  };
  try {
    envelope = JSON.parse(envelopeBytes.toString('utf8')) as typeof envelope;
  } catch {
    throw new Error('QSDM signed release envelope is not valid JSON.');
  }
  if (
    envelope.schema !== 'qsdm.signed-release.v1' ||
    envelope.algorithm !== 'ML-DSA-87' ||
    envelope.key_id !== releaseTrustKey.key_id
  ) {
    throw new Error('QSDM signed release envelope identity is invalid.');
  }
  if (
    typeof envelope.signature !== 'string' ||
    !/^[0-9a-f]{9254}$/.test(envelope.signature)
  ) {
    throw new Error(
      'QSDM release signature has an invalid ML-DSA-87 encoding.'
    );
  }
  if (
    typeof envelope.manifest_base64 !== 'string' ||
    !/^[A-Za-z0-9+/]+={0,2}$/.test(envelope.manifest_base64)
  ) {
    throw new Error('QSDM signed release payload encoding is invalid.');
  }
  const manifestBytes = Buffer.from(envelope.manifest_base64, 'base64');
  if (!manifestBytes.length || manifestBytes.length > MAX_MANIFEST_BYTES) {
    throw new Error('QSDM signed release payload size is invalid.');
  }
  return {
    manifestBytes,
    signatureHex: envelope.signature,
  };
}

export function parseAndValidateQsdmHiveReleaseManifest(
  manifestBytes: Buffer,
  expectedPlatform: SupportedReleasePlatform,
  now = new Date()
): QsdmHiveReleaseManifest {
  let manifest: QsdmHiveReleaseManifest;
  try {
    manifest = JSON.parse(
      manifestBytes.toString('utf8')
    ) as QsdmHiveReleaseManifest;
  } catch {
    throw new Error('QSDM release manifest is not valid JSON.');
  }

  if (
    manifest.schema !== 'qsdm.release-manifest.v1' ||
    manifest.product !== 'qsdm-hive' ||
    manifest.channel !== 'stable'
  ) {
    throw new Error('QSDM release manifest identity is invalid.');
  }
  if (manifest.platform !== expectedPlatform) {
    throw new Error(
      `QSDM release manifest targets ${manifest.platform}, not ${expectedPlatform}.`
    );
  }
  if (!/^\d+\.\d+\.\d+$/.test(manifest.version)) {
    throw new Error('QSDM release manifest version is invalid.');
  }
  if (!/^[0-9a-f]{40}$/.test(manifest.commit)) {
    throw new Error('QSDM release manifest commit is invalid.');
  }
  if (manifest.key_id !== releaseTrustKey.key_id) {
    throw new Error('QSDM release manifest was signed by an untrusted key.');
  }

  const issuedAt = Date.parse(manifest.issued_at);
  const expiresAt = Date.parse(manifest.expires_at);
  if (!Number.isFinite(issuedAt) || !Number.isFinite(expiresAt)) {
    throw new Error('QSDM release manifest validity timestamps are invalid.');
  }
  if (issuedAt > now.getTime() + CLOCK_SKEW_MS) {
    throw new Error('QSDM release manifest is dated in the future.');
  }
  if (expiresAt <= now.getTime()) {
    throw new Error('QSDM release manifest has expired.');
  }
  if (
    expiresAt <= issuedAt ||
    expiresAt - issuedAt > 120 * 24 * 60 * 60 * 1000
  ) {
    throw new Error('QSDM release manifest validity window is invalid.');
  }

  if (!Array.isArray(manifest.artifacts) || manifest.artifacts.length < 2) {
    throw new Error(
      'QSDM release manifest does not contain required artifacts.'
    );
  }
  if (manifest.artifacts.length > 32) {
    throw new Error('QSDM release manifest contains too many artifacts.');
  }
  const names = new Set<string>();
  for (const artifact of manifest.artifacts) {
    if (
      !artifact ||
      typeof artifact.name !== 'string' ||
      artifact.name !== path.basename(artifact.name) ||
      artifact.name.includes('..') ||
      artifact.name.includes('/') ||
      artifact.name.includes('\\')
    ) {
      throw new Error(
        'QSDM release manifest contains an unsafe artifact name.'
      );
    }
    if (names.has(artifact.name)) {
      throw new Error(`QSDM release manifest repeats ${artifact.name}.`);
    }
    names.add(artifact.name);
    if (artifact.platform !== expectedPlatform) {
      throw new Error(
        `QSDM release artifact ${artifact.name} has the wrong platform.`
      );
    }
    if (!RELEASE_ARTIFACT_ROLES.has(artifact.role)) {
      throw new Error(
        `QSDM release artifact ${artifact.name} has an invalid role.`
      );
    }
    if (!Number.isSafeInteger(artifact.size) || artifact.size <= 0) {
      throw new Error(
        `QSDM release artifact ${artifact.name} has an invalid size.`
      );
    }
    if (!/^[0-9a-f]{64}$/.test(artifact.sha256)) {
      throw new Error(
        `QSDM release artifact ${artifact.name} has an invalid SHA-256.`
      );
    }
  }
  requireSingleArtifact(manifest, 'updater-manifest');
  requireSingleArtifact(manifest, 'installer');
  return manifest;
}

export async function verifyDownloadedQsdmHiveUpdate(
  downloadedFile: string,
  release: VerifiedQsdmHiveRelease
) {
  if (
    !downloadedFile ||
    path.basename(downloadedFile) !== release.installer.name
  ) {
    throw new Error(
      'Downloaded QSDM Hive installer name does not match the signed manifest.'
    );
  }
  const stat = await fs.promises.stat(downloadedFile);
  if (stat.size !== release.installer.size) {
    throw new Error(
      'Downloaded QSDM Hive installer size does not match the signed manifest.'
    );
  }
  const hash = crypto.createHash('sha256');
  await new Promise<void>((resolve, reject) => {
    const stream = fs.createReadStream(downloadedFile);
    stream.on('data', (chunk) => hash.update(chunk));
    stream.on('error', reject);
    stream.on('end', resolve);
  });
  if (hash.digest('hex') !== release.installer.sha256) {
    throw new Error(
      'Downloaded QSDM Hive installer SHA-256 does not match the signed manifest.'
    );
  }
}

function requireSingleArtifact(
  manifest: QsdmHiveReleaseManifest,
  role: ReleaseArtifactRole
) {
  const matches = manifest.artifacts.filter(
    (artifact) => artifact.role === role
  );
  if (matches.length !== 1) {
    throw new Error(`QSDM release manifest must contain exactly one ${role}.`);
  }
  return matches[0];
}

function assertArtifactBytes(artifact: QsdmReleaseArtifact, bytes: Buffer) {
  if (bytes.length !== artifact.size) {
    throw new Error(
      `${artifact.name} size does not match the signed manifest.`
    );
  }
  const actualHash = crypto.createHash('sha256').update(bytes).digest('hex');
  if (actualHash !== artifact.sha256) {
    throw new Error(
      `${artifact.name} SHA-256 does not match the signed manifest.`
    );
  }
}

function assertUpdaterManifestMatchesRelease(
  updaterManifest: string,
  version: string,
  installerName: string
) {
  const manifestVersion = /^version:\s*['"]?([^'"\r\n]+)['"]?\s*$/im
    .exec(updaterManifest)?.[1]
    ?.trim();
  const manifestPath = /^path:\s*['"]?([^'"\r\n]+)['"]?\s*$/im
    .exec(updaterManifest)?.[1]
    ?.trim();
  if (manifestVersion !== version) {
    throw new Error(
      'Updater metadata version does not match the signed QSDM release.'
    );
  }
  if (manifestPath && manifestPath !== installerName) {
    throw new Error(
      'Updater metadata installer does not match the signed QSDM release.'
    );
  }
  if (
    !updaterManifest.includes(`url: ${installerName}`) &&
    manifestPath !== installerName
  ) {
    throw new Error(
      'Updater metadata does not reference the signed QSDM installer.'
    );
  }
}

async function verifyManifestWithBundledQsdmCli(
  manifestBytes: Buffer,
  signatureHex: string,
  qsdmCliPath?: string
) {
  const cliPath = qsdmCliPath || resolveBundledQsdmCliPath();
  const result = spawnSync(
    cliPath,
    [
      'wallet',
      'verify',
      '--public-key',
      releaseTrustKey.public_key,
      '--message-file',
      '-',
      '--signature',
      signatureHex,
    ],
    {
      input: manifestBytes,
      encoding: 'utf8',
      maxBuffer: 1024 * 1024,
      timeout: 15_000,
      windowsHide: true,
    }
  );
  if (result.error) {
    throw new Error(
      `Could not run the QSDM release verifier: ${result.error.message}`
    );
  }
  if (result.status !== 0) {
    throw new Error(
      `QSDM release signature verification failed: ${(
        result.stderr || ''
      ).trim()}`
    );
  }
}

function resolveBundledQsdmCliPath() {
  const cliName = process.platform === 'win32' ? 'qsdmcli.exe' : 'qsdmcli';
  const platformDir =
    process.platform === 'win32' ? 'windows' : process.platform;
  const candidates = [
    path.join(process.resourcesPath || '', 'native', cliName),
    path.join(process.cwd(), 'native', platformDir, 'x64', cliName),
  ];
  const cliPath = candidates.find(
    (candidate) => candidate && fs.existsSync(candidate)
  );
  if (!cliPath) {
    throw new Error('Bundled qsdmcli release verifier was not found.');
  }
  return cliPath;
}

function normalizePlatform(
  platform: NodeJS.Platform
): SupportedReleasePlatform {
  if (platform === 'win32') return 'windows';
  if (platform === 'linux') return 'linux';
  throw new Error(`QSDM signed updates do not support ${platform}.`);
}

function ensureTrailingSlash(url: string) {
  return url.endsWith('/') ? url : `${url}/`;
}

function resolveArtifactUrl(name: string, manifestUrl: string) {
  return new URL(name, manifestUrl).toString();
}

function fetchReleaseBytes(
  url: string,
  maxBytes: number,
  redirects = 0
): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const parsedUrl = new URL(url);
    if (parsedUrl.protocol !== 'https:' && parsedUrl.hostname !== '127.0.0.1') {
      reject(new Error('QSDM release metadata must use HTTPS.'));
      return;
    }
    const client = parsedUrl.protocol === 'http:' ? http : https;
    const request = client.get(
      parsedUrl,
      {
        timeout: REQUEST_TIMEOUT_MS,
        headers: {
          Accept: 'application/json, application/x-yaml, text/plain, */*',
          'User-Agent': 'QSDM-Hive-Release-Verifier',
        },
      },
      (response) => {
        if (
          response.statusCode &&
          response.statusCode >= 300 &&
          response.statusCode < 400 &&
          response.headers.location
        ) {
          if (redirects >= MAX_REDIRECTS) {
            response.resume();
            reject(
              new Error('QSDM release metadata redirected too many times.')
            );
            return;
          }
          const redirectedUrl = new URL(response.headers.location, url);
          if (redirectedUrl.origin !== parsedUrl.origin) {
            response.resume();
            reject(
              new Error('QSDM release metadata redirected to another origin.')
            );
            return;
          }
          response.resume();
          fetchReleaseBytes(redirectedUrl.toString(), maxBytes, redirects + 1)
            .then(resolve)
            .catch(reject);
          return;
        }
        if (response.statusCode !== 200) {
          response.resume();
          reject(
            new Error(
              `QSDM release metadata request failed with status ${response.statusCode}.`
            )
          );
          return;
        }
        const chunks: Buffer[] = [];
        let received = 0;
        response.on('data', (chunk: Buffer) => {
          received += chunk.length;
          if (received > maxBytes) {
            request.destroy(
              new Error('QSDM release metadata exceeded its size limit.')
            );
            return;
          }
          chunks.push(Buffer.from(chunk));
        });
        response.on('end', () => resolve(Buffer.concat(chunks)));
      }
    );
    request.on('timeout', () => {
      request.destroy(new Error('QSDM release metadata request timed out.'));
    });
    request.on('error', reject);
  });
}
