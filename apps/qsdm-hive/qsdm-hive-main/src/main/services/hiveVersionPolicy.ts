import { URL } from 'url';

import { Endpoints } from 'config/endpoints';

import { app } from '../app';

import {
  getQsdmHiveReleaseManifestUrl,
  getVerifiedQsdmHiveRelease,
} from './qsdmReleaseManifest';

const DEFAULT_MANIFEST_BASE_URL = 'https://qsdm.tech/downloads';
const UNSIGNED_PREVIEW_MANIFEST_BASE_URL =
  'https://qsdm.tech/downloads/unsigned-preview';
const DEFAULT_DOWNLOAD_URL = 'https://qsdm.tech/download.html';
const POLICY_CACHE_MS = 5 * 60 * 1000;

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

export function getHiveSignedReleaseManifestUrl(
  platform: NodeJS.Platform = process.platform
) {
  const baseUrl = getHiveReleaseBaseUrl();
  return getQsdmHiveReleaseManifestUrl(platform, baseUrl);
}

export function isUnsignedPreviewHiveVersion(version: string) {
  return /^\d+\.\d+\.\d+-unsigned-preview\.\d+$/.test(version.trim());
}

export function getDefaultHiveVersionManifestUrl(
  platform: NodeJS.Platform,
  currentVersion = getCurrentHiveVersion()
) {
  const manifestName =
    platform === 'linux'
      ? 'latest-linux.yml'
      : platform === 'darwin'
      ? 'latest-mac.yml'
      : 'latest.yml';

  const manifestBaseUrl = isUnsignedPreviewHiveVersion(currentVersion)
    ? UNSIGNED_PREVIEW_MANIFEST_BASE_URL
    : DEFAULT_MANIFEST_BASE_URL;

  return `${manifestBaseUrl}/${manifestName}`;
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
  const manifestUrl = getHiveSignedReleaseManifestUrl();
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
    const release = await getVerifiedQsdmHiveRelease({
      platform: process.platform,
      baseUrl: getHiveReleaseBaseUrl(),
      forceRefresh: true,
    });

    return buildPolicyStatus({
      currentVersion,
      requiredVersion: release.manifest.version,
      manifestUrl: release.manifestUrl,
      downloadUrl: release.installerUrl || fallbackDownloadUrl,
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

function getHiveReleaseBaseUrl() {
  if (!canUseLocalHiveVersionPolicyOverrides()) {
    return DEFAULT_MANIFEST_BASE_URL;
  }

  const explicitSignedManifest =
    process.env.QSDM_HIVE_RELEASE_MANIFEST_URL?.trim();
  if (explicitSignedManifest) {
    return new URL('.', explicitSignedManifest).toString();
  }
  return (
    process.env.QSDM_HIVE_UPDATE_BASE_URL?.trim() || DEFAULT_MANIFEST_BASE_URL
  );
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
