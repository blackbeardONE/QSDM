import { fetchWithTimeout } from 'main/node/helpers';

import { retrieveFromIPFS } from './ipfs';

jest.mock('main/node/helpers', () => ({
  fetchWithTimeout: jest.fn(),
}));

const mockedFetchWithTimeout = fetchWithTimeout as jest.MockedFunction<
  typeof fetchWithTimeout
>;

const response = (body: string, status = 200): Response =>
  ({
    ok: status >= 200 && status < 300,
    status,
    text: jest.fn().mockResolvedValue(body),
  } as unknown as Response);

describe('retrieveFromIPFS', () => {
  beforeEach(() => {
    mockedFetchWithTimeout.mockReset();
  });

  it('skips transport failures, HTTP failures, and gateway HTML pages', async () => {
    mockedFetchWithTimeout
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(response('not found', 404))
      .mockResolvedValueOnce(response('  <html>gateway error</html>'))
      .mockResolvedValueOnce(response('{"name":"QSDM task"}'));

    await expect(retrieveFromIPFS('test-cid', 'task.json')).resolves.toBe(
      '{"name":"QSDM task"}'
    );
    expect(mockedFetchWithTimeout).toHaveBeenCalledTimes(4);
  });

  it('returns a serializable detailed error after every gateway fails', async () => {
    mockedFetchWithTimeout.mockRejectedValue(new Error('offline'));

    await expect(retrieveFromIPFS('test-cid', 'task.json')).rejects.toThrow(
      'Failed to get test-cid from IPFS'
    );
    expect(mockedFetchWithTimeout).toHaveBeenCalledTimes(6);
  });
});
