import { Event } from 'electron';

import {
  getQsdmPulsePoolStatus,
  performQsdmPulseAction,
  setQsdmPulseSettings,
} from 'main/services/qsdmPulsePool';
import {
  QsdmPulseActionResult,
  QsdmPulsePoolSettings,
  QsdmPulsePoolStatus,
} from 'models/api/qsdm';

export const getQsdmPulsePool = async (): Promise<QsdmPulsePoolStatus> =>
  getQsdmPulsePoolStatus();

export const setQsdmPulsePool = async (
  _event: Event,
  payload: Partial<QsdmPulsePoolSettings>
): Promise<QsdmPulsePoolStatus> => {
  setQsdmPulseSettings(payload || {});
  return getQsdmPulsePoolStatus();
};

export const runQsdmPulsePoolAction = async (
  _event: Event,
  payload: { op: 'join' | 'leave' | 'reclaim' }
): Promise<QsdmPulseActionResult> => {
  if (!payload || !['join', 'leave', 'reclaim'].includes(payload.op)) {
    throw new Error('unknown PULSE action');
  }
  return performQsdmPulseAction({ op: payload.op });
};
