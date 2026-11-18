import config from 'config';
import {
  QsdmPulseActionResult,
  QsdmPulsePoolSettings,
  QsdmPulsePoolStatus,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export const getQsdmPulsePool = (): Promise<QsdmPulsePoolStatus> =>
  sendMessage(config.endpoints.GET_QSDM_PULSE_POOL, undefined);

export const setQsdmPulsePool = (
  payload: Partial<QsdmPulsePoolSettings>
): Promise<QsdmPulsePoolStatus> =>
  sendMessage(config.endpoints.SET_QSDM_PULSE_POOL, payload);

export const runQsdmPulsePoolAction = (payload: {
  op: 'join' | 'leave' | 'reclaim';
}): Promise<QsdmPulseActionResult> =>
  sendMessage(config.endpoints.RUN_QSDM_PULSE_POOL_ACTION, payload);
