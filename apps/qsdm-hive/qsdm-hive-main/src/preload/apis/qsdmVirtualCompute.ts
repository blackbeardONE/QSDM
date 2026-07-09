import config from 'config';
import {
  QsdmVirtualComputeCancelRequest,
  QsdmVirtualComputeJob,
  QsdmVirtualComputeJobList,
  QsdmVirtualComputeResourcesResponse,
  QsdmVirtualComputeSubmitRequest,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export const getQsdmVirtualComputeResources =
  (): Promise<QsdmVirtualComputeResourcesResponse> =>
    sendMessage(config.endpoints.GET_QSDM_VIRTUAL_COMPUTE_RESOURCES, undefined);

export const getQsdmVirtualComputeJobs =
  (): Promise<QsdmVirtualComputeJobList> =>
    sendMessage(config.endpoints.GET_QSDM_VIRTUAL_COMPUTE_JOBS, undefined);

export const submitQsdmVirtualComputeJob = (
  payload: QsdmVirtualComputeSubmitRequest
): Promise<QsdmVirtualComputeJob> =>
  sendMessage(config.endpoints.SUBMIT_QSDM_VIRTUAL_COMPUTE_JOB, payload);

export const cancelQsdmVirtualComputeJob = (
  payload: QsdmVirtualComputeCancelRequest
): Promise<QsdmVirtualComputeJob> =>
  sendMessage(config.endpoints.CANCEL_QSDM_VIRTUAL_COMPUTE_JOB, payload);
