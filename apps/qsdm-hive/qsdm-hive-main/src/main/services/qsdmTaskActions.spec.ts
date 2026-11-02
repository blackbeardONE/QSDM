import axios from 'axios';
import { spawn } from 'child_process';
import { EventEmitter } from 'events';
import { PassThrough } from 'stream';

import { setQsdmRuntimeCoreApiUrl } from 'config/qsdm';
import { assertQsdmCanonicalChainSafety } from './qsdmCanonicalChain';

import {
  __resetQsdmTaskActionNonceReservationsForTests,
  resolveQsdmTaskActionApiUrl,
  submitQsdmTaskActionIntent,
} from './qsdmTaskActions';

jest.mock('axios', () => ({
  get: jest.fn(),
  post: jest.fn(),
  isAxiosError: (error: any) => Boolean(error?.isAxiosError),
}));

jest.mock('child_process', () => ({
  spawn: jest.fn(),
}));

jest.mock('./qsdmCanonicalChain', () => ({
  assertQsdmCanonicalChainSafety: jest.fn().mockResolvedValue({ safe: true }),
}));

const mockedAxiosGet = axios.get as jest.Mock;
const mockedAxiosPost = axios.post as jest.Mock;
const mockedSpawn = spawn as jest.Mock;
const mockedSafety = assertQsdmCanonicalChainSafety as jest.Mock;
const safeReport = {
  safe: true,
  effectiveApiUrl: 'http://127.0.0.1:8080/api/v1',
  canonicalApiUrl: 'https://api.qsdm.tech/api/v1',
};

const originalEnv = process.env;
const realSetTimeout = setTimeout;
const flushTaskSubmission = () =>
  new Promise((resolve) => realSetTimeout(resolve, 0));
const useTaskRetryClock = () => {
  jest.useFakeTimers({ doNotFake: ['nextTick', 'queueMicrotask'] });
  jest.setSystemTime(new Date('2026-09-20T00:00:00.000Z'));
};
const retryAfterError = (value: unknown, status = 503) => ({
  isAxiosError: true,
  message: `Request failed with status code ${status}`,
  response: { status, headers: { 'Retry-After': value } },
});

const createMockChild = () => {
  const child = new EventEmitter() as EventEmitter & {
    stdin: PassThrough;
    stdout: PassThrough;
    stderr: PassThrough;
    kill: jest.Mock;
  };
  child.stdin = new PassThrough();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = jest.fn();
  return child;
};

const autoSignTask = () => {
  mockedSpawn.mockImplementation(() => {
    const child = createMockChild();
    const input: Buffer[] = [];
    child.stdin.on('data', (chunk) => input.push(Buffer.from(chunk)));
    child.stdin.on('finish', () => {
      const envelope = JSON.parse(Buffer.concat(input).toString()) as Record<
        string,
        unknown
      >;
      queueMicrotask(() => {
        child.stdout.end(
          JSON.stringify({ ...envelope, signature: 'sig', public_key: 'pub' })
        );
        child.emit('close', 0);
      });
    });
    return child;
  });
};
const acceptedTask = () => ({
  data: {
    status: 'accepted',
    action_id: 'accepted-id',
    mempool_submitted: true,
    mempool_status: 'submitted',
  },
});

describe('qsdmTaskActions', () => {
  beforeEach(() => {
    process.env = {
      ...originalEnv,
      QSDM_TASK_ACTION_SIGNER: 'cli',
      QSDM_TASK_ACTION_SENDER:
        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      QSDM_TASK_ACTION_PASSPHRASE_FILE: 'pass.txt',
      QSDM_TASK_ACTION_CLI_PATH: 'qsdmcli',
      QSDM_TASK_ACTION_SERIAL_COMMIT_WAIT_MS: '0',
      QSDM_TASK_ACTION_SUBMIT_RETRY_BASE_MS: '0',
    };
    mockedAxiosGet.mockReset();
    mockedAxiosPost.mockReset();
    mockedSpawn.mockReset();
    mockedSafety.mockReset().mockResolvedValue(safeReport);
    setQsdmRuntimeCoreApiUrl();
    __resetQsdmTaskActionNonceReservationsForTests();
    jest.useRealTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  afterAll(() => {
    process.env = originalEnv;
    jest.useRealTimers();
  });

  it.each([
    ['http://127.0.0.1:8080/api/v1', 'https://canonical.example/api/v1'],
    [
      'https://api.qsdm.tech/attest/home-validator/api/v1',
      'https://canonical.example/api/v1',
    ],
    ['https://canonical.example/api/v1', 'https://canonical.example/api/v1'],
    ['https://private.example/api/v1/', 'https://private.example/api/v1'],
  ])('routes signed task actions from %s to %s', (runtimeApiUrl, expected) => {
    expect(
      resolveQsdmTaskActionApiUrl({
        runtimeApiUrl,
        canonicalApiUrl: 'https://canonical.example/api/v1',
      })
    ).toBe(expected);
  });

  it('signs with qsdmcli and posts the signed task action envelope', async () => {
    const child = createMockChild();
    const stdinChunks: Buffer[] = [];
    child.stdin.on('data', (chunk) => stdinChunks.push(Buffer.from(chunk)));
    mockedSpawn.mockReturnValue(child);

    const signedEnvelope = {
      id: 'hive_start_1234567890_abcd',
      sender: process.env.QSDM_TASK_ACTION_SENDER,
      task_id: 'task-1',
      action: 'start',
      payload: '{"mode":"service"}',
      timestamp: '2026-05-28T00:00:00.000Z',
      nonce: 4,
      signature: 'sig',
      public_key: 'pub',
    };
    mockedAxiosGet.mockResolvedValue({
      data: {
        address: process.env.QSDM_TASK_ACTION_SENDER,
        balance: 10,
        nonce: 4,
        present: true,
      },
    });
    mockedAxiosPost.mockResolvedValue({
      data: {
        action_id: signedEnvelope.id,
        status: 'accepted',
        sender: signedEnvelope.sender,
        task_id: signedEnvelope.task_id,
        action: signedEnvelope.action,
        mempool_submitted: true,
        mempool_status: 'submitted',
      },
    });

    const promise = submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      payload: { mode: 'service' },
    });

    await new Promise((resolve) => setTimeout(resolve, 0));
    child.stdout.write(JSON.stringify(signedEnvelope));
    child.stdout.end();
    child.emit('close', 0);

    const response = await promise;

    expect(mockedSpawn).toHaveBeenCalledWith(
      'qsdmcli',
      [
        'wallet',
        'sign-task-action',
        '--envelope-file',
        '-',
        '--passphrase-file',
        'pass.txt',
      ],
      {
        windowsHide: true,
        stdio: ['pipe', 'pipe', 'pipe'],
      }
    );
    const unsignedEnvelope = JSON.parse(
      Buffer.concat(stdinChunks).toString()
    ) as Record<string, unknown>;
    expect(unsignedEnvelope).toMatchObject({
      sender: process.env.QSDM_TASK_ACTION_SENDER,
      task_id: 'task-1',
      action: 'start',
      payload: '{"mode":"service"}',
      nonce: 4,
    });
    expect(unsignedEnvelope.id).toMatch(/^hive_start_\d+_[0-9a-f]{16}$/);
    expect(mockedAxiosGet).toHaveBeenCalledWith(
      `https://api.qsdm.tech/api/v1/mining/account?address=${process.env.QSDM_TASK_ACTION_SENDER}`,
      { timeout: 10000 }
    );
    expect(mockedAxiosPost).toHaveBeenCalledWith(
      'https://api.qsdm.tech/api/v1/tasks/actions/submit-signed',
      signedEnvelope,
      { timeout: 10000 }
    );
    expect(response.status).toBe('accepted');
    expect(response.client_nonce).toBe(4);
  });

  it('retries a transient 503 idempotently and accepts a duplicate response', async () => {
    const child = createMockChild();
    mockedSpawn.mockReturnValue(child);
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 5 } });
    const signedEnvelope = {
      id: 'hive_stake_1234567890_retry',
      sender: process.env.QSDM_TASK_ACTION_SENDER,
      task_id: 'qsdm-edge-worker',
      action: 'stake',
      amount: 1,
      timestamp: '2026-07-05T00:00:00.000Z',
      nonce: 5,
      signature: 'sig',
      public_key: 'pub',
    };
    mockedAxiosPost
      .mockRejectedValueOnce({
        isAxiosError: true,
        message: 'Request failed with status code 503',
        response: { status: 503 },
      })
      .mockRejectedValueOnce({
        isAxiosError: true,
        message: 'Request failed with status code 409',
        response: {
          status: 409,
          data: {
            action_id: signedEnvelope.id,
            status: 'duplicate',
            sender: signedEnvelope.sender,
            task_id: signedEnvelope.task_id,
            action: signedEnvelope.action,
          },
        },
      });

    const promise = submitQsdmTaskActionIntent({
      taskId: signedEnvelope.task_id,
      action: 'stake',
      amount: 1,
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    child.stdout.write(JSON.stringify(signedEnvelope));
    child.stdout.end();
    child.emit('close', 0);

    await expect(promise).resolves.toMatchObject({
      status: 'duplicate',
      action_id: signedEnvelope.id,
    });
    expect(mockedSpawn).toHaveBeenCalledTimes(1);
    expect(mockedAxiosPost).toHaveBeenCalledTimes(2);
    expect(mockedAxiosPost.mock.calls[0][1]).toEqual(
      mockedAxiosPost.mock.calls[1][1]
    );
  });

  it.each([
    ['60', 503],
    ['60', 429],
    [60, 503],
    ['Sun, 20 Sep 2026 00:01:00 GMT', 503],
    ['Sunday, 20-Sep-26 00:01:00 GMT', 503],
    ['Sun Sep 20 00:01:00 2026', 503],
  ])(
    'releases a long server wait (%s, %s) without another POST',
    async (header, status) => {
      useTaskRetryClock();
      autoSignTask();
      mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
      mockedAxiosPost.mockRejectedValueOnce(retryAfterError(header, status));
      await expect(
        submitQsdmTaskActionIntent({
          taskId: 'task-1',
          action: 'start',
          waitForCommit: false,
        })
      ).rejects.toThrow(
        'not confirmed. The server requested a retry after 60 seconds'
      );
      expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
      expect(mockedSpawn).toHaveBeenCalledTimes(1);
      expect(mockedSafety).toHaveBeenCalledTimes(2);
      expect(jest.getTimerCount()).toBe(0);
    }
  );

  it('interprets an obsolete two-digit HTTP year relative to the current century', async () => {
    useTaskRetryClock();
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    // Date.parse interprets year 50 as 1950; HTTP dates mean 2050 here.
    mockedAxiosPost.mockRejectedValueOnce(
      retryAfterError('Saturday, 01-Jan-50 00:00:00 GMT')
    );
    await expect(
      submitQsdmTaskActionIntent({
        taskId: 'task-1',
        action: 'start',
        waitForCommit: false,
      })
    ).rejects.toThrow('not confirmed');
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
    expect(jest.getTimerCount()).toBe(0);
  });

  it('does not turn an enormous valid server wait into an immediate retry', async () => {
    useTaskRetryClock();
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedAxiosPost.mockRejectedValueOnce(retryAfterError('9'.repeat(400)));
    await expect(
      submitQsdmTaskActionIntent({
        taskId: 'task-1',
        action: 'start',
        waitForCommit: false,
      })
    ).rejects.toThrow('not confirmed');
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
    expect(jest.getTimerCount()).toBe(0);
  });

  it('releases the sender queue after a long wait without resetting its nonce reservation', async () => {
    useTaskRetryClock();
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedAxiosPost
      .mockRejectedValueOnce(retryAfterError('60'))
      .mockResolvedValueOnce(acceptedTask());
    const first = submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      waitForCommit: false,
    });
    const second = submitQsdmTaskActionIntent({
      taskId: 'task-2',
      action: 'start',
      waitForCommit: false,
    });
    const results = await Promise.allSettled([first, second]);
    expect(results.map((result) => result.status)).toEqual([
      'rejected',
      'fulfilled',
    ]);
    expect(mockedAxiosPost).toHaveBeenCalledTimes(2);
    expect(mockedSpawn).toHaveBeenCalledTimes(2);
    expect(mockedAxiosPost.mock.calls.map((call) => call[1].nonce)).toEqual([
      4, 5,
    ]);
    expect(mockedAxiosPost.mock.calls[0][1].id).not.toBe(
      mockedAxiosPost.mock.calls[1][1].id
    );
    expect(jest.getTimerCount()).toBe(0);
  });

  it.each(['2', 'Sun, 20 Sep 2026 00:00:02 GMT'])(
    'honors a short server wait (%s) before retrying the same signed envelope',
    async (header) => {
      useTaskRetryClock();
      autoSignTask();
      mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
      mockedAxiosPost
        .mockRejectedValueOnce(retryAfterError(header))
        .mockResolvedValueOnce(acceptedTask());
      const result = submitQsdmTaskActionIntent({
        taskId: 'task-1',
        action: 'start',
        waitForCommit: false,
      });
      await flushTaskSubmission();
      expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
      jest.advanceTimersByTime(1999);
      await flushTaskSubmission();
      expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
      jest.advanceTimersByTime(1);
      await expect(result).resolves.toMatchObject({ status: 'accepted' });
      expect(mockedSpawn).toHaveBeenCalledTimes(1);
      expect(mockedAxiosPost).toHaveBeenCalledTimes(2);
      expect(mockedAxiosPost.mock.calls[0][1]).toBe(
        mockedAxiosPost.mock.calls[1][1]
      );
      expect(mockedSafety.mock.calls).toEqual(
        Array(3).fill([{ forceRefresh: true }])
      );
    }
  );

  it('honors an HTTP leap second across midnight instead of retrying early', async () => {
    useTaskRetryClock();
    jest.setSystemTime(new Date('2026-09-20T23:59:59.000Z'));
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedAxiosPost
      .mockRejectedValueOnce(retryAfterError('Sun, 20 Sep 2026 23:59:60 GMT'))
      .mockResolvedValueOnce(acceptedTask());
    const result = submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      waitForCommit: false,
    });
    await flushTaskSubmission();
    jest.advanceTimersByTime(999);
    await flushTaskSubmission();
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
    jest.advanceTimersByTime(1);
    await expect(result).resolves.toMatchObject({ status: 'accepted' });
    expect(mockedAxiosPost).toHaveBeenCalledTimes(2);
  });

  it('keeps the larger existing backoff when the server wait is shorter', async () => {
    useTaskRetryClock();
    process.env.QSDM_TASK_ACTION_SUBMIT_RETRY_BASE_MS = '1500';
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedAxiosPost
      .mockRejectedValueOnce(retryAfterError('1'))
      .mockResolvedValueOnce(acceptedTask());
    const result = submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      waitForCommit: false,
    });
    await flushTaskSubmission();
    jest.advanceTimersByTime(1499);
    await flushTaskSubmission();
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
    jest.advanceTimersByTime(1);
    await expect(result).resolves.toMatchObject({ status: 'accepted' });
  });

  it('stops before repeated server waits would exceed the total inline budget', async () => {
    useTaskRetryClock();
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedAxiosPost.mockRejectedValue(retryAfterError('3'));
    const outcome = submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      waitForCommit: false,
    }).catch((error) => error);
    await flushTaskSubmission();
    jest.advanceTimersByTime(3000);
    const error = await outcome;
    expect(error.message).toContain('not confirmed');
    expect(error.message).toContain('3 seconds');
    expect(mockedAxiosPost).toHaveBeenCalledTimes(2);
    expect(mockedSpawn).toHaveBeenCalledTimes(1);
    expect(jest.getTimerCount()).toBe(0);
  });

  it('stops a delayed retry if fresh canonical verification fails', async () => {
    useTaskRetryClock();
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedSafety
      .mockResolvedValueOnce(safeReport)
      .mockResolvedValueOnce(safeReport)
      .mockRejectedValueOnce(new Error('Value-bearing actions are blocked'));
    mockedAxiosPost.mockRejectedValueOnce(retryAfterError('1'));
    const outcome = submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      waitForCommit: false,
    }).catch((error) => error);
    await flushTaskSubmission();
    jest.advanceTimersByTime(1000);
    const error = await outcome;
    expect(error.message).toBe('Value-bearing actions are blocked');
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
    expect(mockedSafety).toHaveBeenCalledTimes(3);
  });

  it.each([
    '1.5',
    '-1',
    '1e3',
    'Infinity',
    'not a date',
    'Sun, 31 Feb 2026 00:00:00 GMT',
    'Sun, 20 Sep 2026 24:00:00 GMT',
    'Sun, 20 Xxx 2026 00:00:00 GMT',
    '',
    undefined,
    [],
    false,
  ])(
    'retains bounded retry behavior for a malformed or missing hint (%s)',
    async (header) => {
      autoSignTask();
      mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
      const error = retryAfterError(header);
      mockedAxiosPost.mockRejectedValue(error);
      await expect(
        submitQsdmTaskActionIntent({
          taskId: 'task-1',
          action: 'start',
          waitForCommit: false,
        })
      ).rejects.toBe(error);
      expect(mockedAxiosPost).toHaveBeenCalledTimes(3);
      expect(mockedSpawn).toHaveBeenCalledTimes(1);
      expect(
        mockedAxiosPost.mock.calls.every(
          (call) => call[1] === mockedAxiosPost.mock.calls[0][1]
        )
      ).toBe(true);
    }
  );

  it('surfaces a validator mempool rejection instead of a generic Axios 422', async () => {
    const child = createMockChild();
    mockedSpawn.mockReturnValue(child);
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 2 } });
    const signedEnvelope = {
      id: 'hive_stake_1234567890_rejected',
      sender: process.env.QSDM_TASK_ACTION_SENDER,
      task_id: 'qsdm-mother-hive',
      action: 'stake',
      amount: 1,
      timestamp: '2026-07-09T00:00:00.000Z',
      nonce: 2,
      signature: 'sig',
      public_key: 'pub',
    };
    mockedAxiosPost.mockRejectedValue({
      isAxiosError: true,
      message: 'Request failed with status code 422',
      response: {
        status: 422,
        data: {
          action_id: signedEnvelope.id,
          status: 'rejected',
          sender: signedEnvelope.sender,
          task_id: signedEnvelope.task_id,
          action: signedEnvelope.action,
          mempool_submitted: false,
          mempool_status: 'rejected',
          mempool_error: 'sender nonce already pending in mempool',
        },
      },
    });

    const promise = submitQsdmTaskActionIntent({
      taskId: signedEnvelope.task_id,
      action: 'stake',
      amount: 1,
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    child.stdout.write(JSON.stringify(signedEnvelope));
    child.stdout.end();
    child.emit('close', 0);

    await expect(promise).rejects.toThrow(
      'sender nonce already pending in mempool'
    );
  });

  it('requires the CLI signer to be configured', async () => {
    process.env.QSDM_TASK_ACTION_SIGNER = '';

    await expect(
      submitQsdmTaskActionIntent({
        taskId: 'task-1',
        action: 'stop',
      })
    ).rejects.toThrow('QSDM_TASK_ACTION_SIGNER=cli');
    expect(mockedSpawn).not.toHaveBeenCalled();
  });

  it('uses the live QSDM account nonce instead of the task-action log tail', async () => {
    const child = createMockChild();
    const stdinChunks: Buffer[] = [];
    child.stdin.on('data', (chunk) => stdinChunks.push(Buffer.from(chunk)));
    mockedSpawn.mockReturnValue(child);
    mockedAxiosGet.mockResolvedValueOnce({
      data: {
        address: process.env.QSDM_TASK_ACTION_SENDER,
        balance: 10,
        nonce: 8,
        present: true,
      },
    });
    mockedAxiosGet.mockResolvedValueOnce({
      data: {
        actions: [
          {
            envelope: {
              sender: process.env.QSDM_TASK_ACTION_SENDER,
              nonce: 37,
            },
          },
        ],
      },
    });

    const signedEnvelope = {
      id: 'hive_stake_1234567890_abcd',
      sender: process.env.QSDM_TASK_ACTION_SENDER,
      task_id: 'qsdm-system-miner',
      action: 'stake',
      amount: 1,
      timestamp: '2026-05-28T00:00:00.000Z',
      nonce: 8,
      signature: 'sig',
      public_key: 'pub',
    };
    mockedAxiosPost.mockResolvedValue({
      data: {
        action_id: signedEnvelope.id,
        status: 'accepted',
        sender: signedEnvelope.sender,
        task_id: signedEnvelope.task_id,
        action: signedEnvelope.action,
        mempool_submitted: true,
        mempool_status: 'submitted',
      },
    });

    const promise = submitQsdmTaskActionIntent({
      taskId: 'qsdm-system-miner',
      action: 'stake',
      amount: 1,
    });

    await new Promise((resolve) => setTimeout(resolve, 0));
    child.stdout.write(JSON.stringify(signedEnvelope));
    child.stdout.end();
    child.emit('close', 0);

    await promise;

    const unsignedEnvelope = JSON.parse(
      Buffer.concat(stdinChunks).toString()
    ) as Record<string, unknown>;
    expect(unsignedEnvelope.nonce).toBe(8);
    expect(mockedAxiosGet).toHaveBeenCalledTimes(1);
  });

  it('increments locally reserved nonces for rapid action bursts', async () => {
    mockedAxiosGet.mockImplementation((url: string) => {
      if (url.includes('/mining/account')) {
        return Promise.resolve({
          data: {
            address: process.env.QSDM_TASK_ACTION_SENDER,
            balance: 10,
            nonce: 12,
            present: true,
          },
        });
      }
      return Promise.resolve({ data: { actions: [] } });
    });
    mockedAxiosPost.mockResolvedValue({
      data: {
        action_id: 'accepted-id',
        status: 'accepted',
        sender: process.env.QSDM_TASK_ACTION_SENDER,
        task_id: 'qsdm-edge-worker',
        action: 'submit',
        mempool_submitted: true,
        mempool_status: 'submitted',
      },
    });

    const submitOnce = async () => {
      const child = createMockChild();
      const stdinChunks: Buffer[] = [];
      child.stdin.on('data', (chunk) => stdinChunks.push(Buffer.from(chunk)));
      mockedSpawn.mockReturnValueOnce(child);

      const promise = submitQsdmTaskActionIntent({
        taskId: 'qsdm-edge-worker',
        action: 'submit',
        payload: { source: 'test' },
      });

      await new Promise((resolve) => setTimeout(resolve, 0));
      child.stdout.write(
        JSON.stringify({
          id: 'accepted-id',
          sender: process.env.QSDM_TASK_ACTION_SENDER,
          task_id: 'qsdm-edge-worker',
          action: 'submit',
          timestamp: '2026-05-28T00:00:00.000Z',
          nonce: 12,
          signature: 'sig',
          public_key: 'pub',
        })
      );
      child.stdout.end();
      child.emit('close', 0);
      await promise;

      return JSON.parse(Buffer.concat(stdinChunks).toString()) as Record<
        string,
        unknown
      >;
    };

    const firstEnvelope = await submitOnce();
    const secondEnvelope = await submitOnce();

    expect(firstEnvelope.nonce).toBe(12);
    expect(secondEnvelope.nonce).toBe(13);
  });

  it('resets stale local nonce reservations to the live validator nonce', async () => {
    const sender = process.env.QSDM_TASK_ACTION_SENDER;

    jest.useFakeTimers({
      doNotFake: ['nextTick', 'setImmediate', 'setInterval', 'setTimeout'],
    });
    jest.setSystemTime(new Date('2026-05-28T00:00:00.000Z'));

    mockedAxiosGet.mockResolvedValue({
      data: {
        address: sender,
        balance: 10,
        nonce: 69,
        present: true,
      },
    });
    mockedAxiosPost.mockResolvedValue({
      data: {
        action_id: 'accepted-id',
        status: 'accepted',
        sender,
        task_id: 'qsdm-edge-worker',
        action: 'submit',
        mempool_submitted: true,
        mempool_status: 'submitted',
      },
    });

    const submitOnce = async (signedNonce: number) => {
      const child = createMockChild();
      const stdinChunks: Buffer[] = [];
      child.stdin.on('data', (chunk) => stdinChunks.push(Buffer.from(chunk)));
      mockedSpawn.mockReturnValueOnce(child);

      const promise = submitQsdmTaskActionIntent({
        taskId: 'qsdm-edge-worker',
        action: 'submit',
        payload: { source: 'test' },
      });

      await new Promise((resolve) => setTimeout(resolve, 0));
      child.stdout.write(
        JSON.stringify({
          id: 'accepted-id',
          sender,
          task_id: 'qsdm-edge-worker',
          action: 'submit',
          timestamp: '2026-05-28T00:00:00.000Z',
          nonce: signedNonce,
          signature: 'sig',
          public_key: 'pub',
        })
      );
      child.stdout.end();
      child.emit('close', 0);
      await promise;

      return JSON.parse(Buffer.concat(stdinChunks).toString()) as Record<
        string,
        unknown
      >;
    };

    const firstEnvelope = await submitOnce(69);
    const secondEnvelope = await submitOnce(70);

    jest.setSystemTime(new Date('2026-05-28T00:01:00.000Z'));
    const staleResetEnvelope = await submitOnce(69);

    expect(firstEnvelope.nonce).toBe(69);
    expect(secondEnvelope.nonce).toBe(70);
    expect(staleResetEnvelope.nonce).toBe(69);
  });

  it('blocks a task when the canonical source fails while signing', async () => {
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedSafety
      .mockResolvedValueOnce(safeReport)
      .mockRejectedValueOnce(new Error('Value-bearing actions are blocked'));
    await expect(
      submitQsdmTaskActionIntent({
        taskId: 'task-1',
        action: 'start',
        waitForCommit: false,
      })
    ).rejects.toThrow('Value-bearing actions are blocked');
    expect(mockedAxiosPost).not.toHaveBeenCalled();
    expect(mockedSafety.mock.calls).toEqual([
      [{ forceRefresh: true }],
      [{ forceRefresh: true }],
    ]);
  });

  it('rechecks a queued task after an earlier submission completes', async () => {
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    let releaseFirst: ((value: unknown) => void) | undefined;
    mockedAxiosPost.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          releaseFirst = resolve;
        })
    );
    const first = submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      waitForCommit: false,
    });
    const second = submitQsdmTaskActionIntent({
      taskId: 'task-2',
      action: 'start',
      waitForCommit: false,
    });
    const outcomes = Promise.allSettled([first, second]);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
    mockedSafety.mockRejectedValue(
      new Error('Value-bearing actions are blocked')
    );
    releaseFirst?.(acceptedTask());
    const results = await outcomes;
    expect(results[0].status).toBe('fulfilled');
    expect(results[1].status).toBe('rejected');
    expect(mockedSpawn).toHaveBeenCalledTimes(1);
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
  });

  it('stops a transient submission retry when fresh canonical verification fails', async () => {
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedSafety
      .mockResolvedValueOnce(safeReport)
      .mockResolvedValueOnce(safeReport)
      .mockRejectedValueOnce(new Error('Value-bearing actions are blocked'));
    mockedAxiosPost.mockRejectedValueOnce({
      isAxiosError: true,
      response: { status: 503 },
    });
    await expect(
      submitQsdmTaskActionIntent({
        taskId: 'task-1',
        action: 'start',
        waitForCommit: false,
      })
    ).rejects.toThrow('Value-bearing actions are blocked');
    expect(mockedAxiosPost).toHaveBeenCalledTimes(1);
    expect(mockedSafety).toHaveBeenCalledTimes(3);
  });

  it('uses the verified task endpoint even if a status poll changes global routing', async () => {
    autoSignTask();
    mockedAxiosGet.mockResolvedValue({ data: { nonce: 4 } });
    mockedSafety.mockImplementation(async () => {
      setQsdmRuntimeCoreApiUrl('https://unverified.example/api/v1');
      return safeReport;
    });
    mockedAxiosPost.mockResolvedValue(acceptedTask());
    await submitQsdmTaskActionIntent({
      taskId: 'task-1',
      action: 'start',
      waitForCommit: false,
    });
    expect(mockedAxiosPost.mock.calls[0][0]).toBe(
      'https://api.qsdm.tech/api/v1/tasks/actions/submit-signed'
    );
    setQsdmRuntimeCoreApiUrl();
  });
});
