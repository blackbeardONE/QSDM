import axios from 'axios';
import { spawn } from 'child_process';
import { EventEmitter } from 'events';
import { PassThrough } from 'stream';

import { submitQsdmWalletTransferIntent } from './qsdmWalletTransfer';

jest.mock('axios', () => ({
  get: jest.fn(),
  post: jest.fn(),
  isAxiosError: jest.fn((error) => !!error?.isAxiosError),
}));

jest.mock('child_process', () => ({
  spawn: jest.fn(),
}));

const mockedAxiosGet = axios.get as jest.Mock;
const mockedAxiosPost = axios.post as jest.Mock;
const mockedSpawn = spawn as jest.Mock;

const originalEnv = process.env;

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

describe('qsdmWalletTransfer', () => {
  beforeEach(() => {
    process.env = {
      ...originalEnv,
      QSDM_TASK_ACTION_SIGNER: 'cli',
      QSDM_TASK_ACTION_SENDER:
        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      QSDM_TASK_ACTION_PASSPHRASE_FILE: 'pass.txt',
      QSDM_TASK_ACTION_CLI_PATH: 'qsdmcli',
    };
    mockedAxiosGet.mockReset();
    mockedAxiosPost.mockReset();
    mockedSpawn.mockReset();
  });

  afterAll(() => {
    process.env = originalEnv;
  });

  it('signs with qsdmcli and posts the signed wallet transfer envelope', async () => {
    const child = createMockChild();
    const stdinChunks: Buffer[] = [];
    child.stdin.on('data', (chunk) => stdinChunks.push(Buffer.from(chunk)));
    mockedSpawn.mockReturnValue(child);

    const signedEnvelope = {
      id: 'hive_wallet_1234567890_abcd',
      sender: process.env.QSDM_TASK_ACTION_SENDER,
      recipient:
        'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
      amount: 1.25,
      fee: 0,
      geotag: '',
      parent_cells: [],
      nonce: 8,
      timestamp: '2026-05-30T00:00:00.000Z',
      signature: 'sig',
      public_key: 'pub',
    };

    mockedAxiosGet.mockResolvedValue({
      data: {
        sender: process.env.QSDM_TASK_ACTION_SENDER,
        nonce: 7,
        next: 8,
      },
    });
    mockedAxiosPost.mockResolvedValue({
      data: {
        transaction_id: signedEnvelope.id,
        status: 'accepted',
        broadcast: 'local-only',
      },
    });

    const promise = submitQsdmWalletTransferIntent({
      recipient: signedEnvelope.recipient,
      amount: signedEnvelope.amount,
    });

    await new Promise((resolve) => setTimeout(resolve, 0));
    child.stdout.write(JSON.stringify(signedEnvelope));
    child.stdout.end();
    child.emit('close', 0);

    const response = await promise;

    expect(mockedSpawn).toHaveBeenCalledWith(
      'qsdmcli',
      ['wallet', 'sign-tx', '--envelope-file', '-', '--passphrase-file', 'pass.txt'],
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
      recipient: signedEnvelope.recipient,
      amount: 1.25,
      fee: 0,
      geotag: '',
      parent_cells: [],
      nonce: 8,
    });
    expect(unsignedEnvelope.id).toMatch(/^hive_wallet_\d+_[0-9a-f]{16}$/);
    expect(mockedAxiosGet).toHaveBeenCalledWith(
      `http://127.0.0.1:8080/api/v1/wallet/nonce?sender=${process.env.QSDM_TASK_ACTION_SENDER}`,
      { timeout: 10000 }
    );
    expect(mockedAxiosPost).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/wallet/submit-signed',
      signedEnvelope,
      { timeout: 10000 }
    );
    expect(response.status).toBe('accepted');
  });

  it('requires the CLI signer to be configured', async () => {
    process.env.QSDM_TASK_ACTION_SIGNER = '';

    await expect(
      submitQsdmWalletTransferIntent({
        recipient:
          'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        amount: 1,
      })
    ).rejects.toThrow('QSDM_TASK_ACTION_SIGNER=cli');
    expect(mockedSpawn).not.toHaveBeenCalled();
  });
});
