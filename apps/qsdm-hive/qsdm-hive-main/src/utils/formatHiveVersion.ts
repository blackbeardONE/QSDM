const INTERNAL_RELEASE_SUFFIX = /-unsigned-preview\.\d+$/;

export function formatHiveVersion(version?: string | null) {
  if (!version) {
    return version;
  }

  return version.replace(INTERNAL_RELEASE_SUFFIX, '');
}
