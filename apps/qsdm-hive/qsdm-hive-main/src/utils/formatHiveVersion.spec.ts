import { formatHiveVersion } from './formatHiveVersion';

describe('formatHiveVersion', () => {
  it('shows the public release version without the internal channel suffix', () => {
    expect(formatHiveVersion('1.3.95-unsigned-preview.1')).toBe('1.3.95');
  });

  it('leaves ordinary release versions unchanged', () => {
    expect(formatHiveVersion('1.3.95')).toBe('1.3.95');
  });

  it('preserves empty values', () => {
    expect(formatHiveVersion(undefined)).toBeUndefined();
    expect(formatHiveVersion(null)).toBeNull();
  });
});
