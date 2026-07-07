import { selectQsdmWalletAddress } from './qsdmWalletAddress';

describe('selectQsdmWalletAddress', () => {
  it('uses an explicitly requested address first', () => {
    expect(
      selectQsdmWalletAddress({
        requestedAddress: ' requested ',
        signerAddress: 'active-signer',
        configuredAddress: 'configured-fallback',
      })
    ).toBe('requested');
  });

  it('uses the active signer before a configured fallback', () => {
    expect(
      selectQsdmWalletAddress({
        signerAddress: ' active-signer ',
        configuredAddress: 'configured-fallback',
      })
    ).toBe('active-signer');
  });

  it('uses the configured address only when no active signer exists', () => {
    expect(
      selectQsdmWalletAddress({
        configuredAddress: ' configured-fallback ',
      })
    ).toBe('configured-fallback');
  });
});
