import {
  QsdmVirtualComputeCancelRequest,
  QsdmVirtualComputeSubmitRequest,
} from 'models/api/qsdm';

import {
  cancelQsdmVirtualComputeJob as cancelJob,
  getQsdmVirtualComputeJobs as getJobs,
  getQsdmVirtualComputeResources as getResources,
  submitQsdmVirtualComputeJob as submitJob,
} from '../../services/qsdmVirtualComputeRuntime';

export const getQsdmVirtualComputeResources = async () => getResources();

export const getQsdmVirtualComputeJobs = async () => getJobs();

export const submitQsdmVirtualComputeJob = async (
  _event: Event,
  payload: QsdmVirtualComputeSubmitRequest
) => submitJob(payload);

export const cancelQsdmVirtualComputeJob = async (
  _event: Event,
  payload: QsdmVirtualComputeCancelRequest
) => cancelJob(payload);
