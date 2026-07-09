import { Endpoints } from 'config/endpoints';

import { isAllowedExternalUrl } from './externalNavigation';
import { validateIpcPayload } from './ipcValidation';

const validAddress =
  '13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9';

const validTaskAction = {
  id: 'action-1',
  sender: validAddress,
  task_id: 'qsdm-miner',
  action: 'start',
  amount: 1,
  payload: '{}',
  nonce: 1,
  timestamp: new Date().toISOString(),
  signature: 'signature',
  public_key: 'public-key',
};

describe('QSDM Hive IPC validation', () => {
  it('allows only HTTP and HTTPS external URLs', () => {
    expect(isAllowedExternalUrl('https://qsdm.tech')).toBe(true);
    expect(isAllowedExternalUrl('http://127.0.0.1:1212')).toBe(true);
    expect(isAllowedExternalUrl('file:///C:/Windows/System32/calc.exe')).toBe(
      false
    );
    // eslint-disable-next-line no-script-url
    expect(isAllowedExternalUrl('javascript:alert(1)')).toBe(false);
  });

  it('validates native clipboard writes', () => {
    expect(() =>
      validateIpcPayload(Endpoints.COPY_TEXT_TO_CLIPBOARD, [
        { text: 'qsdm-wallet-address' },
      ])
    ).not.toThrow();
    expect(() =>
      validateIpcPayload(Endpoints.COPY_TEXT_TO_CLIPBOARD, [{ text: '' }])
    ).toThrow(/outside the allowed range/);
  });

  it('rejects unsafe browser-open payloads', () => {
    expect(() =>
      validateIpcPayload(Endpoints.OPEN_BROWSER_WINDOW, [
        { URL: 'https://qsdm.tech/docs' },
      ])
    ).not.toThrow();

    expect(() =>
      validateIpcPayload(Endpoints.OPEN_BROWSER_WINDOW, [
        { URL: 'file:///C:/Users/Windows 10/secret.txt' },
      ])
    ).toThrow(/URL must be HTTP or HTTPS/);
  });

  it('rejects malformed CELL transfers before they reach wallet code', () => {
    expect(() =>
      validateIpcPayload(Endpoints.TRANSFER_CELL_FROM_MAIN_WALLET, [
        {
          accountName: 'Blackbeard',
          amount: 1,
          toWalletAddress: validAddress,
        },
      ])
    ).not.toThrow();

    expect(() =>
      validateIpcPayload(Endpoints.TRANSFER_CELL_FROM_MAIN_WALLET, [
        {
          accountName: 'Blackbeard',
          amount: -1,
          toWalletAddress: validAddress,
        },
      ])
    ).toThrow(/amount is below/);
  });

  it('rejects unsupported signed task actions', () => {
    expect(() =>
      validateIpcPayload(Endpoints.SUBMIT_QSDM_TASK_ACTION, [validTaskAction])
    ).not.toThrow();

    expect(() =>
      validateIpcPayload(Endpoints.SUBMIT_QSDM_TASK_ACTION, [
        {
          ...validTaskAction,
          action: 'format-disk',
        },
      ])
    ).toThrow(/action is not supported/);
  });

  it('validates bounded Task Studio catalog manifests', () => {
    const request = {
      operation: 'publish',
      taskId: 'shared-edge',
      draft: {
        task_id: 'shared-edge',
        name: 'Shared Edge',
        description: 'CPU work shared through QSDM.',
        active: true,
        runtime: {
          kind: 'capability',
          capability: 'generic-proof-v1',
          min_hive_version: '1.3.60',
        },
        minimum_stake_amount: 1,
        reward_per_round: 0.05,
        round_time: 60,
        submission_window: 30,
        audit_window: 15,
        source_url: 'https://qsdm.tech/docs/#/shared-edge',
        tags: ['qsdm', 'cell'],
        authorized_relay_ids: ['a'.repeat(64)],
      },
    };
    expect(() =>
      validateIpcPayload(Endpoints.MANAGE_QSDM_TASK_CATALOG, [request])
    ).not.toThrow();
    expect(() =>
      validateIpcPayload(Endpoints.MANAGE_QSDM_TASK_CATALOG, [
        {
          ...request,
          draft: {
            ...request.draft,
            runtime: {
              kind: 'javascript',
              capability: 'remote-code',
            },
          },
        },
      ])
    ).toThrow(/capability runtimes/);
    expect(() =>
      validateIpcPayload(Endpoints.MANAGE_QSDM_TASK_CATALOG, [
        {
          ...request,
          draft: {
            ...request.draft,
            source_url: 'http://insecure.example/task',
          },
        },
      ])
    ).toThrow(/absolute HTTPS URL/);
    expect(() =>
      validateIpcPayload(Endpoints.MANAGE_QSDM_TASK_CATALOG, [
        {
          ...request,
          draft: {
            ...request.draft,
            authorized_relay_ids: ['not-a-relay-id'],
          },
        },
      ])
    ).toThrow(/authorized_relay_ids/);
  });

  it('requires imported QSDM signer wallets to be JSON objects', () => {
    expect(() =>
      validateIpcPayload(Endpoints.IMPORT_QSDM_SIGNER_WALLET, [
        {
          keystoreJson: JSON.stringify({ address: validAddress }),
          passphrase: 'test-passphrase',
        },
      ])
    ).not.toThrow();

    expect(() =>
      validateIpcPayload(Endpoints.IMPORT_QSDM_SIGNER_WALLET, [
        {
          keystoreJson: 'not-json',
          passphrase: 'test-passphrase',
        },
      ])
    ).toThrow(/valid JSON/);
  });

  it('requires a strong-enough passphrase for new QSDM wallets', () => {
    expect(() =>
      validateIpcPayload(Endpoints.CREATE_QSDM_SIGNER_WALLET, [
        { passphrase: 'correct horse battery staple' },
      ])
    ).not.toThrow();

    expect(() =>
      validateIpcPayload(Endpoints.CREATE_QSDM_SIGNER_WALLET, [
        { passphrase: 'too-short' },
      ])
    ).toThrow(/outside the allowed range/);
  });

  it('allows only bounded Virtual Compute workloads', () => {
    expect(() =>
      validateIpcPayload(Endpoints.SUBMIT_QSDM_VIRTUAL_COMPUTE_JOB, [
        { resource: 'cpu', units: 100_000, deadlineSeconds: 300 },
      ])
    ).not.toThrow();
    expect(() =>
      validateIpcPayload(Endpoints.SUBMIT_QSDM_VIRTUAL_COMPUTE_JOB, [
        { resource: 'ram', memoryMiB: 16 },
      ])
    ).not.toThrow();
    expect(() =>
      validateIpcPayload(Endpoints.SUBMIT_QSDM_VIRTUAL_COMPUTE_JOB, [
        { resource: 'shell', units: 1 },
      ])
    ).toThrow(/resource/);
    expect(() =>
      validateIpcPayload(Endpoints.SUBMIT_QSDM_VIRTUAL_COMPUTE_JOB, [
        { resource: 'gpu', units: 100_000_001 },
      ])
    ).toThrow(/units is above/);
  });

  it('validates Virtual Compute reads and cancellation IDs', () => {
    expect(() =>
      validateIpcPayload(Endpoints.GET_QSDM_VIRTUAL_COMPUTE_RESOURCES, [
        undefined,
      ])
    ).not.toThrow();
    expect(() =>
      validateIpcPayload(Endpoints.CANCEL_QSDM_VIRTUAL_COMPUTE_JOB, [
        { jobId: 'a'.repeat(32) },
      ])
    ).not.toThrow();
    expect(() =>
      validateIpcPayload(Endpoints.CANCEL_QSDM_VIRTUAL_COMPUTE_JOB, [
        { jobId: '../other-job' },
      ])
    ).toThrow(/jobId/);
  });
});
