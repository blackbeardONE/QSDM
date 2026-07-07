import type { RawTaskData } from 'models';

export const QSDM_SUPPORTED_TASK_CAPABILITIES = new Set(['generic-proof-v1']);

const versionParts = (version: string) => {
  const normalized = version.trim().replace(/^v/i, '').split(/[-+]/, 1)[0];
  const parts = normalized.split('.').map((part) => Number.parseInt(part, 10));
  return parts.length === 3 && parts.every(Number.isFinite) ? parts : undefined;
};

export const compareQsdmHiveVersions = (left: string, right: string) => {
  const leftParts = versionParts(left);
  const rightParts = versionParts(right);
  if (!leftParts || !rightParts) return 0;
  for (let index = 0; index < 3; index += 1) {
    if (leftParts[index] !== rightParts[index]) {
      return leftParts[index] < rightParts[index] ? -1 : 1;
    }
  }
  return 0;
};

export const getQsdmTaskRuntimeCompatibilityIssue = (
  task: RawTaskData,
  hiveVersion: string
): string | undefined => {
  const { manifest } = task;
  if (!manifest) return undefined;
  if (manifest.schema_version !== 1) {
    return `Task manifest schema ${manifest.schema_version} is not supported.`;
  }

  const minimumVersion = manifest.runtime.min_hive_version;
  if (
    minimumVersion &&
    compareQsdmHiveVersions(hiveVersion, minimumVersion) < 0
  ) {
    return `Task requires QSDM Hive ${minimumVersion} or newer.`;
  }

  if (manifest.runtime.kind === 'wasm') {
    return 'This QSDM Hive release does not yet include the sandboxed WASM task runtime.';
  }
  if (
    manifest.runtime.kind !== 'capability' ||
    !manifest.runtime.capability ||
    !QSDM_SUPPORTED_TASK_CAPABILITIES.has(manifest.runtime.capability)
  ) {
    return `Task capability ${
      manifest.runtime.capability || 'unknown'
    } is not supported by this QSDM Hive release.`;
  }
  return undefined;
};
