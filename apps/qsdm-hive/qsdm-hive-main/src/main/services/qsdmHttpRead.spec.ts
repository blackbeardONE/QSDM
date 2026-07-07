/**
 * @jest-environment node
 */

import axios from 'axios';

import {
  clearQsdmReadCircuitState,
  qsdmGetFirstJson,
  qsdmGetJson,
} from './qsdmHttpRead';

jest.mock('axios', () => ({
  get: jest.fn(),
  isAxiosError: jest.fn((error) => Boolean(error?.isAxiosError)),
}));

const mockedGet = axios.get as jest.Mock;

describe('qsdmHttpRead', () => {
  beforeEach(() => {
    mockedGet.mockReset();
    clearQsdmReadCircuitState();
  });

  it('shares an in-flight request for the same URL', async () => {
    let resolveRequest: (value: unknown) => void = () => undefined;
    mockedGet.mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve;
      })
    );

    const first = qsdmGetJson<{ ok: boolean }>(
      'https://api.qsdm.tech/api/v1/status'
    );
    const second = qsdmGetJson<{ ok: boolean }>(
      'https://api.qsdm.tech/api/v1/status'
    );
    resolveRequest({ data: { ok: true } });

    await expect(first).resolves.toEqual({ ok: true });
    await expect(second).resolves.toEqual({ ok: true });
    expect(mockedGet).toHaveBeenCalledTimes(1);
  });

  it('opens a circuit only after consecutive transient gateway failures', async () => {
    mockedGet.mockRejectedValue(
      Object.assign(new Error('timeout of 4000ms exceeded'), {
        isAxiosError: true,
      })
    );
    const url = 'https://api.qsdm.tech/attest/home-validator/api/v1/tasks';

    await expect(qsdmGetJson(url)).rejects.toThrow('timeout');
    await expect(qsdmGetJson(url)).rejects.toThrow('timeout');
    await expect(qsdmGetJson(url)).rejects.toThrow('temporarily unavailable');
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  it('gates concurrent gateway reads behind one endpoint probe', async () => {
    let rejectProbe: (reason: unknown) => void = () => undefined;
    mockedGet
      .mockReturnValueOnce(
        new Promise((_resolve, reject) => {
          rejectProbe = reject;
        })
      )
      .mockRejectedValueOnce(
        Object.assign(new Error('gateway timeout'), { isAxiosError: true })
      );

    const first = qsdmGetJson(
      'https://api.qsdm.tech/attest/home-validator/api/v1/tasks'
    );
    const second = qsdmGetJson(
      'https://api.qsdm.tech/attest/home-validator/api/v1/status'
    );
    rejectProbe(
      Object.assign(new Error('gateway timeout'), { isAxiosError: true })
    );

    await expect(first).rejects.toThrow('gateway timeout');
    await expect(second).rejects.toThrow('gateway timeout');
    await expect(
      qsdmGetJson('https://api.qsdm.tech/attest/home-validator/api/v1/health')
    ).rejects.toThrow('temporarily unavailable');
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  it('recovers from one isolated timeout without quarantining the endpoint', async () => {
    mockedGet
      .mockRejectedValueOnce(
        Object.assign(new Error('temporary timeout'), { isAxiosError: true })
      )
      .mockResolvedValueOnce({ data: { chain_tip: 42 } });
    const url = 'https://api.qsdm.tech/api/v1/status';

    await expect(qsdmGetJson(url)).rejects.toThrow('temporary timeout');
    await expect(qsdmGetJson(url)).resolves.toEqual({ chain_tip: 42 });
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  it('does not quarantine a recently healthy API when one sibling route is slow', async () => {
    mockedGet
      .mockResolvedValueOnce({ data: { ok: true } })
      .mockRejectedValueOnce(
        Object.assign(new Error('status timeout'), { isAxiosError: true })
      )
      .mockResolvedValueOnce({ data: { chain_tip: 43 } });

    await expect(
      qsdmGetJson('https://api.qsdm.tech/api/v1/health')
    ).resolves.toEqual({ ok: true });
    await expect(
      qsdmGetJson('https://api.qsdm.tech/api/v1/status')
    ).rejects.toThrow('status timeout');
    await expect(
      qsdmGetJson('https://api.qsdm.tech/api/v1/status?fresh=1')
    ).resolves.toEqual({ chain_tip: 43 });
    expect(mockedGet).toHaveBeenCalledTimes(3);
  });

  it('keeps canonical and home-gateway circuits separate', async () => {
    mockedGet
      .mockRejectedValueOnce(
        Object.assign(new Error('gateway timeout'), { isAxiosError: true })
      )
      .mockResolvedValueOnce({ data: { chain_tip: 10 } });

    await expect(
      qsdmGetFirstJson([
        'https://api.qsdm.tech/attest/home-validator/api/v1/status',
        'https://api.qsdm.tech/api/v1/status',
      ])
    ).resolves.toEqual({ chain_tip: 10 });
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  it('does not open the circuit for a reachable 404 endpoint', async () => {
    const notFound = Object.assign(new Error('not found'), {
      isAxiosError: true,
      response: { status: 404, data: { error: 'task not found' } },
    });
    mockedGet.mockRejectedValue(notFound);
    const url = 'https://api.qsdm.tech/api/v1/tasks/missing';

    await expect(qsdmGetJson(url)).rejects.toThrow('not found');
    await expect(qsdmGetJson(url)).rejects.toThrow('not found');
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });
});
