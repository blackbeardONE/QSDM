import http from 'http';
import https from 'https';
import { URL } from 'url';

import { Endpoints } from 'config/endpoints';

import { app } from '../app';

const DEFAULT_MANIFEST_BASE_URL = 'https://qsdm.tech/downloads';
const DEFAULT_DOWNLOAD_URL = 'https://qsdm.tech/download.html';
const POLICY_CACHE_MS = 5 * 60 * 1000;
const REQUEST_TIMEOUT_MS = 10000;
const MAX_REDIRECTS = 5;

export type HiveVersionPolicyStatus = {
  compatible: boolean;
  updateRequired: boolean;
  currentVersion: string;
  requiredVersion: string | null;
  manifestUrl: string;
  downloadUrl: string;
  checkedAt: string;
  reason:
    | 'current'
    | 'version-mismatch'
    | 'manifest-unavailable'
    | 'policy-disabled';
  error?: string;
};

export type HiveVersionPolicyOptions = {
  forceRefresh?: boolean;
};

type HiveReleaseManifest = {
  version: string | null;
  downloadUrl?: string;
};

let cachedStatus: HiveVersionPolicyStatus | null = null;
let cachedStatusAt = 0;

export const VERSION_POLICY_ALLOWED_ENDPOINTS = new Set<string>([
  Endpoints.GET_HIVE_VERSION_POLICY,
  Endpoints.DOWNLOAD_APP_UPDATE,
  Endpoints.CHECK_APP_UPDATE,
  Endpoints.GET_VERSION,
  Endpoints.GET_PLATFORM,
  Endpoints.COPY_TEXT_TO_CLIPBOARD,
  Endpoints.OPEN_BROWSER_WINDOW,
  Endpoints.APP_RELAUNCH,
  Endpoints.QUIT_APP,
  Endpoints.GET_BRANDING_CONFIG,
  Endpoints.GET_BRAND_LOGO,
]);

export function resetHiveVersionPolicyCacheForTests() {
  cachedStatus = null;
  cachedStatusAt = 0;
}

export function getCurrentHiveVersion() {
  return String(
    app.getVersion?.() || process.env.npm_package_version || ''
  ).trim();
}

export function getHiveVersionManifestUrl(
  platform: NodeJS.Platform = process.platform
) {
  const defaultManifestUrl = getDefaultHiveVersionManifestUrl(platform);

  if (!canUseLocalHiveVersionPolicyOverrides()) {
    return defaultManifestUrl;
  }

  return (
    process.env.QSDM_HIVE_VERSION_MANIFEST_URL?.trim() || defaultManifestUrl
  );
}

export function getDefaultHiveVersionManifestUrl(platform: NodeJS.Platform) {
  const manifestName =
    platform === 'linux'
      ? 'latest-linux.yml'
      : platform === 'darwin'
      ? 'latest-mac.yml'
      : 'latest.yml';

  return `${DEFAULT_MANIFEST_BASE_URL}/${manifestName}`;
}

export function parseHiveReleaseManifest(
  manifestText: string,
  manifestUrl: string
): HiveReleaseManifest {
  const trimmed = manifestText.trim();

  if (!trimmed) {
    return { version: null };
  }

  try {
    const json = JSON.parse(trimmed) as {
      version?: unknown;
      path?: unknown;
      downloadUrl?: unknown;
      files?: Array<{ url?: unknown }>;
    };
    const version = typeof json.version === 'string' ? json.version.trim() : '';
    const rawDownload =
      (typeof json.downloadUrl === 'string' && json.downloadUrl) ||
      (typeof json.path === 'string' && json.path) ||
      (Array.isArray(json.files) &&
        typeof json.files[0]?.url === 'string' &&
        json.files[0].url) ||
      undefined;

    return {
      version: version || null,
      downloadUrl: resolveManifestUrl(rawDownload, manifestUrl),
    };
  } catch {
    // Electron-builder publishes YAML by default; parse the tiny subset we need.
  }

  const version =
    /^version:\s*['"]?([^'"\r\n]+)['"]?\s*$/im.exec(trimmed)?.[1]?.trim() ||
    null;
  const pathValue = /^path:\s*['"]?([^'"\r\n]+)['"]?\s*$/im
    .exec(trimmed)?.[1]
    ?.trim();
  const fileUrl = /^\s*-\s*url:\s*['"]?([^'"\r\n]+)['"]?\s*$/im
    .exec(trimmed)?.[1]
    ?.trim();

  return {
    version,
    downloadUrl: resolveManifestUrl(pathValue || fileUrl, manifestUrl),
  };
}

export async function getHiveVersionPolicyStatus(
  options: HiveVersionPolicyOptions = {}
): Promise<HiveVersionPolicyStatus> {
  const now = Date.now();
  if (
    !options.forceRefresh &&
    cachedStatus &&
    now - cachedStatusAt < POLICY_CACHE_MS
  ) {
    return cachedStatus;
  }

  const status = await resolveHiveVersionPolicyStatus();
  cachedStatus = status;
  cachedStatusAt = now;
  return status;
}

export async function assertHiveVersionPolicyAllowsEndpoint(
  endpoint: Endpoints | string
) {
  if (VERSION_POLICY_ALLOWED_ENDPOINTS.has(endpoint)) {
    return;
  }

  const status = await getHiveVersionPolicyStatus();
  if (!status.compatible) {
    throw new Error(
      `QSDM Hive ${
        status.currentVersion
      } is blocked by version policy. Required version: ${
        status.requiredVersion || 'unknown'
      }. Download the latest Hive from ${status.downloadUrl}.`
    );
  }
}

async function resolveHiveVersionPolicyStatus(): Promise<HiveVersionPolicyStatus> {
  const currentVersion = getCurrentHiveVersion();
  const manifestUrl = getHiveVersionManifestUrl();
  const fallbackDownloadUrl = getHiveDownloadUrl();

  if (isVersionPolicyDisabledForThisProcess()) {
    return {
      compatible: true,
      updateRequired: false,
      currentVersion,
      requiredVersion: currentVersion,
      manifestUrl,
      downloadUrl: fallbackDownloadUrl,
      checkedAt: new Date().toISOString(),
      reason: 'policy-disabled',
    };
  }

  const explicitRequiredVersion = canUseLocalHiveVersionPolicyOverrides()
    ? process.env.QSDM_HIVE_REQUIRED_VERSION?.trim()
    : '';
  if (explicitRequiredVersion) {
    return buildPolicyStatus({
      currentVersion,
      requiredVersion: explicitRequiredVersion,
      manifestUrl,
      downloadUrl: fallbackDownloadUrl,
    });
  }

  try {
    const manifestText = await fetchText(manifestUrl);
    const manifest = parseHiveReleaseManifest(manifestText, manifestUrl);
    if (!manifest.version) {
      throw new Error('Version manifest did not include a version field.');
    }

    return buildPolicyStatus({
      currentVersion,
      requiredVersion: manifest.version,
      manifestUrl,
      downloadUrl: manifest.downloadUrl || fallbackDownloadUrl,
    });
  } catch (error) {
    return {
      compatible: false,
      updateRequired: true,
      currentVersion,
      requiredVersion: null,
      manifestUrl,
      downloadUrl: fallbackDownloadUrl,
      checkedAt: new Date().toISOString(),
      reason: 'manifest-unavailable',
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

function buildPolicyStatus({
  currentVersion,
  requiredVersion,
  manifestUrl,
  downloadUrl,
}: {
  currentVersion: string;
  requiredVersion: string;
  manifestUrl: string;
  downloadUrl: string;
}): HiveVersionPolicyStatus {
  const compatible = currentVersion === requiredVersion;
  return {
    compatible,
    updateRequired: !compatible,
    currentVersion,
    requiredVersion,
    manifestUrl,
    downloadUrl,
    checkedAt: new Date().toISOString(),
    reason: compatible ? 'current' : 'version-mismatch',
  };
}

function resolveManifestUrl(rawUrl: unknown, manifestUrl: string) {
  if (typeof rawUrl !== 'string' || !rawUrl.trim()) {
    return undefined;
  }

  try {
    return new URL(rawUrl.trim(), manifestUrl).toString();
  } catch {
    return undefined;
  }
}

function getHiveDownloadUrl() {
  if (!canUseLocalHiveVersionPolicyOverrides()) {
    return DEFAULT_DOWNLOAD_URL;
  }

  return process.env.QSDM_HIVE_DOWNLOAD_URL?.trim() || DEFAULT_DOWNLOAD_URL;
}

function isVersionPolicyDisabledForThisProcess() {
  if (
    canUseLocalHiveVersionPolicyOverrides() &&
    process.env.QSDM_HIVE_DISABLE_VERSION_POLICY === '1'
  ) {
    return true;
  }

  return (
    process.env.NODE_ENV === 'test' &&
    !process.env.QSDM_HIVE_REQUIRED_VERSION &&
    !process.env.QSDM_HIVE_VERSION_MANIFEST_URL
  );
}

export function canUseLocalHiveVersionPolicyOverrides() {
  return !isProductionHiveRuntime();
}

function isProductionHiveRuntime() {
  return Boolean(app.isPackaged) || process.env.NODE_ENV === 'production';
}

function fetchText(url: string, redirects = 0): Promise<string> {
  return new Promise((resolve, reject) => {
    const parsedUrl = new URL(url);
    const client = parsedUrl.protocol === 'http:' ? http : https;
    const request = client.get(
      parsedUrl,
      {
        timeout: REQUEST_TIMEOUT_MS,
        headers: {
          Accept: 'application/x-yaml, application/json, text/plain, */*',
          'User-Agent': `QSDM-Hive/${getCurrentHiveVersion()}`,
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
            reject(new Error('Version manifest redirected too many times.'));
            return;
          }
          response.resume();
          fetchText(
            new URL(response.headers.location, url).toString(),
            redirects + 1
          )
            .then(resolve)
            .catch(reject);
          return;
        }

        if (response.statusCode !== 200) {
          response.resume();
          reject(
            new Error(
              `Version manifest request failed with status ${response.statusCode}`
            )
          );
          return;
        }

        let body = '';
        response.setEncoding('utf8');
        response.on('data', (chunk) => {
          body += chunk;
        });
        response.on('end', () => resolve(body));
      }
    );

    request.on('timeout', () => {
      request.destroy(new Error('Version manifest request timed out.'));
    });
    request.on('error', reject);
  });
}
