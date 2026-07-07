import { Event } from 'electron';

import {
  HiveVersionPolicyOptions,
  HiveVersionPolicyStatus,
  getHiveVersionPolicyStatus,
} from '../../services/hiveVersionPolicy';

export const getHiveVersionPolicy = async (
  _: Event,
  options?: HiveVersionPolicyOptions
): Promise<HiveVersionPolicyStatus> => {
  return getHiveVersionPolicyStatus(options);
};
