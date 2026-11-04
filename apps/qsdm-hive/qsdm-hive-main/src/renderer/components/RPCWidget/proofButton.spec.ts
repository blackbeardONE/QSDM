import { isQsdmProofButtonVisible } from './proofButton';

import type { QsdmCoreStatusResponse } from 'models/api/qsdm';

const statusWithSigner = (
  taskSigner: Partial<NonNullable<QsdmCoreStatusResponse['taskSigner']>>
) =>
  ({
    healthy: true,
    taskSigner: {
      mode: 'cli',
      configured: true,
      ready: true,
      localLoopEnabled: true,
      checks: {
        sender: true,
        cliMode: true,
        cliPath: true,
        keystore: true,
        passphrase: true,
      },
      ...taskSigner,
    },
  } as unknown as QsdmCoreStatusResponse);

describe('isQsdmProofButtonVisible', () => {
  it('hides the Proof button while status is unknown', () => {
    expect(isQsdmProofButtonVisible(undefined)).toBe(false);
    expect(
      isQsdmProofButtonVisible({ healthy: true } as QsdmCoreStatusResponse)
    ).toBe(false);
  });

  it('hides the Proof button for a ready Hive signer without the explicit opt-in', () => {
    expect(isQsdmProofButtonVisible(statusWithSigner({}))).toBe(false);
    expect(
      isQsdmProofButtonVisible(statusWithSigner({ proofLoopEnabled: false }))
    ).toBe(false);
  });

  it('shows the Proof button only with QSDM_ENABLE_LOCAL_SIGNED_LOOP=1', () => {
    expect(
      isQsdmProofButtonVisible(statusWithSigner({ proofLoopEnabled: true }))
    ).toBe(true);
  });
});
