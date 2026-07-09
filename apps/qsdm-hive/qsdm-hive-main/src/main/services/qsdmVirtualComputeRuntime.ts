import axios, { Method } from 'axios';
import { randomBytes } from 'crypto';
import fs from 'fs';

import {
  QsdmVirtualComputeCancelRequest,
  QsdmVirtualComputeJob,
  QsdmVirtualComputeJobList,
  QsdmVirtualComputeResourcesResponse,
  QsdmVirtualComputeSubmitRequest,
} from 'models/api/qsdm';

import {
  getQsdmComputeGatewayEndpoint,
  getQsdmComputeGatewayTokenFile,
} from './qsdmSystemTasks';

const MAX_GATEWAY_RESPONSE_BYTES = 256 * 1024;
const DEFAULT_DEADLINE_SECONDS = 300;

const positiveInteger = (
  value: number | undefined,
  fallback: number,
  maximum: number,
  label: string
) => {
  const candidate = value ?? fallback;
  if (
    !Number.isSafeInteger(candidate) ||
    candidate < 1 ||
    candidate > maximum
  ) {
    throw new Error(`${label} must be an integer between 1 and ${maximum}.`);
  }
  return candidate;
};

const gatewayCredential = () => {
  const tokenFile = getQsdmComputeGatewayTokenFile();
  if (!fs.existsSync(tokenFile)) {
    throw new Error('Start Mother Hive before using pooled resources.');
  }
  const token = fs.readFileSync(tokenFile, 'utf8').trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(token)) {
    throw new Error('Virtual Compute Runtime credential is invalid.');
  }
  return token;
};

const gatewayRequest = async <T>(
  method: Method,
  requestPath: string,
  data?: Record<string, unknown>
): Promise<T> => {
  const endpoint = getQsdmComputeGatewayEndpoint();
  if (!/^http:\/\/(?:127\.0\.0\.1|localhost):\d+$/.test(endpoint)) {
    throw new Error('Virtual Compute Runtime must use a loopback endpoint.');
  }

  try {
    const response = await axios.request<T>({
      url: `${endpoint}${requestPath}`,
      method,
      data,
      headers: { Authorization: `Bearer ${gatewayCredential()}` },
      timeout: 15_000,
      maxBodyLength: 16 * 1024,
      maxContentLength: MAX_GATEWAY_RESPONSE_BYTES,
      responseType: 'json',
      proxy: false,
    });
    return response.data;
  } catch (error) {
    if (axios.isAxiosError(error)) {
      const detail =
        error.response?.data && typeof error.response.data === 'object'
          ? String((error.response.data as { error?: unknown }).error || '')
          : '';
      throw new Error(
        detail ||
          (error.code === 'ECONNREFUSED'
            ? 'Start Mother Hive before using pooled resources.'
            : `Virtual Compute Runtime request failed: ${error.message}`)
      );
    }
    throw error;
  }
};

export const getQsdmVirtualComputeResources = () =>
  gatewayRequest<QsdmVirtualComputeResourcesResponse>('GET', '/v1/resources');

export const getQsdmVirtualComputeJobs = () =>
  gatewayRequest<QsdmVirtualComputeJobList>('GET', '/v1/jobs?limit=20');

export const submitQsdmVirtualComputeJob = (
  request: QsdmVirtualComputeSubmitRequest
) => {
  if (!['cpu', 'gpu', 'ram'].includes(request.resource)) {
    throw new Error('Virtual Compute resource must be CPU, GPU, or RAM.');
  }
  const deadlineSeconds = positiveInteger(
    request.deadlineSeconds,
    DEFAULT_DEADLINE_SECONDS,
    3600,
    'Deadline'
  );
  if (deadlineSeconds < 30) {
    throw new Error('Deadline must be at least 30 seconds.');
  }

  const body: Record<string, unknown> = {
    client_request_id: `hive-${Date.now()}-${randomBytes(6).toString('hex')}`,
    resource: request.resource,
    deadline_seconds: deadlineSeconds,
  };
  if (request.resource === 'ram') {
    body.memory_mib = positiveInteger(
      request.memoryMiB,
      16,
      1024,
      'RAM budget'
    );
  } else {
    body.units = positiveInteger(
      request.units,
      request.resource === 'gpu' ? 1_000_000 : 100_000,
      request.resource === 'gpu' ? 100_000_000 : 20_000_000,
      `${request.resource.toUpperCase()} work budget`
    );
  }
  return gatewayRequest<QsdmVirtualComputeJob>('POST', '/v1/jobs', body);
};

export const cancelQsdmVirtualComputeJob = (
  request: QsdmVirtualComputeCancelRequest
) => {
  const jobId = request.jobId.trim().toLowerCase();
  if (!/^[0-9a-f]{32}$/.test(jobId)) {
    throw new Error('Virtual Compute job ID is invalid.');
  }
  return gatewayRequest<QsdmVirtualComputeJob>('DELETE', `/v1/jobs/${jobId}`);
};
