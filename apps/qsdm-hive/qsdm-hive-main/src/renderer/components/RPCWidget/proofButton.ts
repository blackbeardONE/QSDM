import type { QsdmCoreStatusResponse } from 'models/api/qsdm';

/**
 * The "Proof" button runs the paid-task self-test (fund, start, stake,
 * submit, claim). While paid task work is paused that always fails, and
 * QSDM Core does not report whether paid task actions are enabled, so the
 * button is shown only when the operator explicitly opted in with
 * QSDM_ENABLE_LOCAL_SIGNED_LOOP=1.
 */
export const isQsdmProofButtonVisible = (status?: QsdmCoreStatusResponse) =>
  !!status?.taskSigner?.proofLoopEnabled;
