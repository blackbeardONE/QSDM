/**
 * @jest-environment node
 */

import axios from 'axios';
import fs from 'fs';

import {
  cancelQsdmVirtualComputeJob,
  getQsdmVirtualComputeResources,
  submitQsdmVirtualComputeJob,
} from './qsdmVirtualComputeRuntime';

jest.mock('axios', () => ({
  request: jest.fn(),
  isAxiosError: jest.fn((error: { isAxiosError?: boolean }) =>
    Boolean(error?.isAxiosError)
  ),
}));

jest.mock('./qsdmSystemTasks', () => ({
  getQsdmComputeGatewayEndpoint: () => 'http://127.0.0.1:7742',
  getQsdmComputeGatewayTokenFile: () => 'C:\\qsdm\\compute-gateway.token',
}));

const mockedRequest = axios.request as jest.Mock;
const mockedIsAxiosError = axios.isAxiosError as unknown as jest.Mock;
const gatewayToken = 'a'.repeat(64);

describe('qsdmVirtualComputeRuntime', () => {
  beforeEach(() => {
    mockedRequest.mockReset();
    mockedIsAxiosError.mockImplementation((error: { isAxiosError?: boolean }) =>
      Boolean(error?.isAxiosError)
    );
    jest.spyOn(fs, 'existsSync').mockReturnValue(true);
    jest.spyOn(fs, 'readFileSync').mockReturnValue(gatewayToken);
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  it('keeps the gateway credential in main and reads resource inventory', async () => {
    const resources = {
      version: 'qsdm-virtual-compute/v1',
      online_agents: 1,
      resources: {},
    };
    mockedRequest.mockResolvedValueOnce({ data: resources });

    await expect(getQsdmVirtualComputeResources()).resolves.toEqual(resources);
    expect(mockedRequest).toHaveBeenCalledWith(
      expect.objectContaining({
        url: 'http://127.0.0.1:7742/v1/resources',
        method: 'GET',
        proxy: false,
        headers: { Authorization: `Bearer ${gatewayToken}` },
      })
    );
  });

  it('translates bounded CPU and RAM requests to the gateway contract', async () => {
    mockedRequest.mockResolvedValue({ data: { id: 'b'.repeat(32) } });

    await submitQsdmVirtualComputeJob({ resource: 'cpu', units: 250_000 });
    await submitQsdmVirtualComputeJob({ resource: 'ram', memoryMiB: 32 });

    expect(mockedRequest).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        method: 'POST',
        url: 'http://127.0.0.1:7742/v1/jobs',
        data: expect.objectContaining({
          resource: 'cpu',
          units: 250_000,
          deadline_seconds: 300,
          client_request_id: expect.stringMatching(/^hive-/),
        }),
      })
    );
    expect(mockedRequest).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        data: expect.objectContaining({
          resource: 'ram',
          memory_mib: 32,
        }),
      })
    );
  });

  it('rejects invalid cancellation IDs before contacting the gateway', () => {
    expect(() =>
      cancelQsdmVirtualComputeJob({ jobId: '../other-job' })
    ).toThrow('job ID is invalid');
    expect(mockedRequest).not.toHaveBeenCalled();
  });

  it('surfaces the bounded gateway error instead of an Axios object', async () => {
    mockedRequest.mockRejectedValueOnce(
      Object.assign(new Error('Request failed with status code 409'), {
        isAxiosError: true,
        response: { data: { error: 'No compatible GPU Agent is online.' } },
      })
    );

    await expect(
      submitQsdmVirtualComputeJob({ resource: 'gpu', units: 1_000_000 })
    ).rejects.toThrow('No compatible GPU Agent is online.');
  });
});
