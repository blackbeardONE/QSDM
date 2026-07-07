import { formatUrl, isValidUrl } from './url';

describe('url utils', () => {
  it('formats bare domains without changing existing HTTP URLs', () => {
    expect(formatUrl('qsdm.tech/docs')).toBe('https://qsdm.tech/docs');
    expect(formatUrl('https://qsdm.tech/docs')).toBe(
      'https://qsdm.tech/docs'
    );
    expect(formatUrl('http://localhost:1212')).toBe('http://localhost:1212');
  });

  it('keeps blank URLs invalid instead of inventing a broken HTTPS URL', () => {
    expect(formatUrl('   ')).toBe('');
    expect(isValidUrl(formatUrl('   '))).toBe(false);
  });
});
