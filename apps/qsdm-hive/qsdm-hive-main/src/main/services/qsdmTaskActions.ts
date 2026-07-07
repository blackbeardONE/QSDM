import axios from 'axios';
import { spawn } from 'child_process';
import { randomBytes } from 'crypto';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import {
  QsdmTaskAction,
  QsdmTaskActionEnvelope,
  QsdmTaskActionSubmitResponse,
  QsdmMiningAccountResponse,
} from 'models/api/qsdm';

import {
  getQsdmTaskActionCliPath,
  getQsdmTaskActionKeystorePath,
  getQsdmTaskActionPassphraseFile,
  getQsdmTaskActionSender,
  getQsdmTaskActionSignerMode,
} from './qsdmTaskActionSigner';
import { assertQsdmCanonicalChainSafety } from './qsdmCanonicalChain';

type UnsignedQsdmTaskActionEnvelope = Omit<
  QsdmTaskActionEnvelope,
  'signature' | 'public_key'
> & {
  signature?: string;
  public_key?: string;
};

export interface SubmitQsdmTaskActionIntentParams {
  taskId: string;
  action: QsdmTaskAction;
  amount?: number;
  payload?: string | Record<string, unknown>;
  waitForCommit?: boolean;
}

export interface QsdmMessageSignature {
  address: string;
  public_key: string;
  signature: string;
}

interface QsdmWalletMetadata {
  address?: string;
  public_key?: string;
}

const describeTaskActionRejection = (
  response: QsdmTaskActionSubmitResponse
) => {
  const details = [
    `status=${response.status || 'unknown'}`,
    `mempool=${response.mempool_status || 'unknown'}`,
  ];
  if (response.mempool_error) {
    details.push(response.mempool_error);
  }
  return details.join(', ');
};

const assertTaskActionSubmitted = (response: QsdmTaskActionSubmitResponse) => {
  // The validator only records an action after mempool admission. A duplicate
  // action ID therefore means a previous attempt succeeded but its response
  // was lost. Treat that response as idempotent success.
  if (response.status === 'duplicate') {
    return;
  }

  if (response.status !== 'accepted') {
    throw new Error(
      `QSDM task action was not accepted: ${describeTaskActionRejection(
        response
      )}`
    );
  }

  if (
    !response.mempool_submitted ||
    (response.mempool_status &&
      !['submitted', 'duplicate'].includes(response.mempool_status))
  ) {
    throw new Error(
      `QSDM task action was not submitted to the validator mempool: ${describeTaskActionRejection(
        response
      )}`
    );
  }
};

const readEnv = (key: string, fallback = '') => {
  const value = process.env[key];
  return value?.trim() || fallback;
};

const makeActionId = (action: QsdmTaskAction) =>
  `hive_${action}_${Date.now()}_${randomBytes(8).toString('hex')}`;

const LOCAL_NONCE_BURST_WINDOW_MS = 30_000;
const TASK_ACTION_NONCE_COMMIT_POLL_MS = 1000;
const TASK_ACTION_NONCE_COMMIT_WAIT_MS = 120_000;
const TASK_ACTION_SUBMIT_MAX_ATTEMPTS = 3;
const TASK_ACTION_SUBMIT_RETRY_BASE_MS = 750;

const getTaskActionSubmitMaxAttempts = () => {
  const parsed = Number.parseInt(
    readEnv(
      'QSDM_TASK_ACTION_SUBMIT_MAX_ATTEMPTS',
      String(TASK_ACTION_SUBMIT_MAX_ATTEMPTS)
    ),
    10
  );
  return Number.isFinite(parsed) && parsed >= 1 && parsed <= 5
    ? parsed
    : TASK_ACTION_SUBMIT_MAX_ATTEMPTS;
};

const getTaskActionSubmitRetryBaseMs = () => {
  const parsed = Number.parseInt(
    readEnv(
      'QSDM_TASK_ACTION_SUBMIT_RETRY_BASE_MS',
      String(TASK_ACTION_SUBMIT_RETRY_BASE_MS)
    ),
    10
  );
  return Number.isFinite(parsed) && parsed >= 0 && parsed <= 10_000
    ? parsed
    : TASK_ACTION_SUBMIT_RETRY_BASE_MS;
};

const isTransientTaskActionSubmitError = (error: unknown) => {
  if (!axios.isAxiosError(error)) {
    return false;
  }
  const status = error.response?.status;
  return (
    status === undefined ||
    status === 408 ||
    status === 425 ||
    status === 429 ||
    status === 500 ||
    status === 502 ||
    status === 503 ||
    status === 504
  );
};

const issuedTaskActionNonceBySender: Record<
  string,
  { nonce: number; reservedAtMs: number }
> = {};
const taskActionQueueBySender: Record<string, Promise<void>> = {};

export const __resetQsdmTaskActionNonceReservationsForTests = () => {
  Object.keys(issuedTaskActionNonceBySender).forEach((sender) => {
    delete issuedTaskActionNonceBySender[sender];
  });
  Object.keys(taskActionQueueBySender).forEach((sender) => {
    delete taskActionQueueBySender[sender];
  });
};

const reserveTaskActionNonce = (
  sender: string | undefined,
  suggestedNonce: number | undefined
) => {
  if (!sender) {
    return suggestedNonce;
  }

  const previousReservation = issuedTaskActionNonceBySender[sender];
  if (suggestedNonce === undefined && previousReservation === undefined) {
    return undefined;
  }

  const now = Date.now();
  const previousNonce = previousReservation?.nonce;
  const previousIsFresh =
    previousReservation !== undefined &&
    now - previousReservation.reservedAtMs <= LOCAL_NONCE_BURST_WINDOW_MS;

  const nextNonce =
    previousNonce === undefined
      ? suggestedNonce
      : suggestedNonce !== undefined &&
        suggestedNonce <= previousNonce &&
        !previousIsFresh
      ? suggestedNonce
      : Math.max(suggestedNonce || 0, previousNonce + 1);

  if (nextNonce && nextNonce > 0) {
    issuedTaskActionNonceBySender[sender] = {
      nonce: nextNonce,
      reservedAtMs: now,
    };
  }

  return nextNonce;
};

const getTaskActionSignerTimeoutMs = () => {
  const raw = readEnv('QSDM_TASK_ACTION_SIGNER_TIMEOUT_MS', '30000');
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 30000;
};

const getTaskActionNonceCommitWaitMs = () => {
  const raw = readEnv(
    'QSDM_TASK_ACTION_SERIAL_COMMIT_WAIT_MS',
    String(TASK_ACTION_NONCE_COMMIT_WAIT_MS)
  );
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed >= 0
    ? parsed
    : TASK_ACTION_NONCE_COMMIT_WAIT_MS;
};

const delay = (ms: number) =>
  new Promise((resolve) => {
    setTimeout(resolve, ms);
  });

const buildPayload = (payload?: string | Record<string, unknown>) => {
  if (!payload) return undefined;
  return typeof payload === 'string' ? payload : JSON.stringify(payload);
};

const buildUrl = (path: string, params: Record<string, string>) => {
  const url = new URL(buildQsdmCoreApiUrl(path));
  Object.entries(params).forEach(([key, value]) => {
    url.searchParams.set(key, value);
  });
  return url.toString();
};

const getTaskActionAccountNonce = async (
  sender: string
): Promise<number | undefined> => {
  try {
    const response = await axios.get<QsdmMiningAccountResponse>(
      buildUrl('/mining/account', { address: sender }),
      { timeout: 10000 }
    );
    return Number.isFinite(response.data.nonce)
      ? response.data.nonce
      : undefined;
  } catch {
    return undefined;
  }
};

const getNextTaskActionNonce = async (
  sender: string
): Promise<number | undefined> => {
  // QSDM account nonces are "next expected nonce" values. The task-action
  // log is only an inbox of accepted submissions, not proof that a task action
  // has been applied to the chain. Using the log tail here can skip ahead after
  // a rejected or stale action and leave later task rewards waiting forever.
  return getTaskActionAccountNonce(sender);
};

const waitForTaskActionNonceCommit = async (
  sender: string | undefined,
  nonce: number | undefined
) => {
  const waitMs = getTaskActionNonceCommitWaitMs();
  if (!sender || nonce === undefined || waitMs <= 0) {
    return;
  }

  const targetNonce = nonce + 1;
  const deadline = Date.now() + waitMs;
  while (Date.now() <= deadline) {
    const accountNonce = await getTaskActionAccountNonce(sender);
    if (accountNonce !== undefined && accountNonce >= targetNonce) {
      return;
    }
    await delay(TASK_ACTION_NONCE_COMMIT_POLL_MS);
  }
};

const runQueuedTaskActionForSender = async <T>(
  sender: string | undefined,
  work: () => Promise<T>
) => {
  if (!sender) {
    return work();
  }

  const previous = taskActionQueueBySender[sender] || Promise.resolve();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const queued = previous.catch(() => undefined).then(() => gate);
  taskActionQueueBySender[sender] = queued;

  await previous.catch(() => undefined);
  try {
    return await work();
  } finally {
    release();
    if (taskActionQueueBySender[sender] === queued) {
      delete taskActionQueueBySender[sender];
    }
  }
};

const assertCliSignerConfigured = () => {
  if (getQsdmTaskActionSignerMode() !== 'cli') {
    throw new Error(
      'QSDM_TASK_ACTION_SIGNER=cli is required to sign native QSDM wallet actions'
    );
  }
  if (!getQsdmTaskActionSender()) {
    throw new Error(
      'QSDM_TASK_ACTION_SENDER or QSDM_WALLET_ADDRESS is required to sign native QSDM wallet actions'
    );
  }
  if (!getQsdmTaskActionPassphraseFile()) {
    throw new Error(
      'QSDM_TASK_ACTION_PASSPHRASE_FILE or QSDM_PASSPHRASE_FILE is required to sign native QSDM wallet actions'
    );
  }
};

const buildWalletArgs = (subcommand: string, extraArgs: string[] = []) => {
  const args = ['wallet', subcommand, ...extraArgs];
  const keystorePath = getQsdmTaskActionKeystorePath();

  if (keystorePath) {
    args.push('--in', keystorePath);
  }

  return args;
};

const runQsdmCli = ({
  args,
  stdin,
  timeoutMessage,
}: {
  args: string[];
  stdin?: string;
  timeoutMessage: string;
}): Promise<string> =>
  new Promise((resolve, reject) => {
    const child = spawn(getQsdmTaskActionCliPath(), args, {
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    let settled = false;

    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      child.kill();
      reject(new Error(timeoutMessage));
    }, getTaskActionSignerTimeoutMs());

    child.stdout?.on('data', (chunk) => {
      stdout += chunk.toString();
    });
    child.stderr?.on('data', (chunk) => {
      stderr += chunk.toString();
    });
    child.on('error', (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      reject(error);
    });
    child.on('close', (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);

      if (code !== 0) {
        reject(
          new Error(
            `qsdmcli ${args.join(
              ' '
            )} failed with exit code ${code}: ${stderr.trim()}`
          )
        );
        return;
      }

      resolve(stdout.trim());
    });
    child.stdin?.end(stdin || '');
  });

const readQsdmSignerWalletMetadata = async (): Promise<
  Required<QsdmWalletMetadata>
> => {
  const raw = await runQsdmCli({
    args: buildWalletArgs('show', ['--json']),
    timeoutMessage: 'qsdmcli wallet show timed out',
  });
  const metadata = JSON.parse(raw) as QsdmWalletMetadata;
  if (!metadata.address || !metadata.public_key) {
    throw new Error(
      'qsdmcli wallet show did not return address and public_key'
    );
  }
  return {
    address: metadata.address,
    public_key: metadata.public_key,
  };
};

export const signQsdmMessageWithCli = async (
  message: string
): Promise<QsdmMessageSignature> => {
  assertCliSignerConfigured();
  if (!message.trim()) {
    throw new Error('refusing to sign an empty QSDM message');
  }

  const passphraseFile = getQsdmTaskActionPassphraseFile();
  const [metadata, signature] = await Promise.all([
    readQsdmSignerWalletMetadata(),
    runQsdmCli({
      args: buildWalletArgs('sign', [
        '--message-file',
        '-',
        '--passphrase-file',
        passphraseFile,
      ]),
      stdin: message,
      timeoutMessage: 'qsdmcli wallet message signing timed out',
    }),
  ]);

  return {
    address: metadata.address,
    public_key: metadata.public_key,
    signature,
  };
};

export const signQsdmTaskActionWithCli = async (
  envelope: UnsignedQsdmTaskActionEnvelope
): Promise<QsdmTaskActionEnvelope> => {
  assertCliSignerConfigured();

  const args = buildWalletArgs('sign-task-action', ['--envelope-file', '-']);
  const passphraseFile = getQsdmTaskActionPassphraseFile();

  args.push('--passphrase-file', passphraseFile);

  return new Promise((resolve, reject) => {
    const child = spawn(getQsdmTaskActionCliPath(), args, {
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    let settled = false;

    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      child.kill();
      reject(new Error('qsdmcli task action signing timed out'));
    }, getTaskActionSignerTimeoutMs());

    child.stdout?.on('data', (chunk) => {
      stdout += chunk.toString();
    });
    child.stderr?.on('data', (chunk) => {
      stderr += chunk.toString();
    });
    child.on('error', (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      reject(error);
    });
    child.on('close', (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);

      if (code !== 0) {
        reject(
          new Error(
            `qsdmcli wallet sign-task-action failed with exit code ${code}: ${stderr.trim()}`
          )
        );
        return;
      }

      try {
        resolve(JSON.parse(stdout.trim()) as QsdmTaskActionEnvelope);
      } catch (error) {
        reject(error);
      }
    });
    child.stdin?.end(JSON.stringify(envelope));
  });
};

export const submitQsdmTaskActionIntent = async ({
  taskId,
  action,
  amount,
  payload,
  waitForCommit = true,
}: SubmitQsdmTaskActionIntentParams): Promise<QsdmTaskActionSubmitResponse> => {
  await assertQsdmCanonicalChainSafety();
  const sender = getQsdmTaskActionSender();

  return runQueuedTaskActionForSender(sender, async () => {
    const initialNonce = reserveTaskActionNonce(
      sender,
      sender ? await getNextTaskActionNonce(sender) : undefined
    );

    const signAndSubmit = async (nonce?: number) => {
      const envelope: UnsignedQsdmTaskActionEnvelope = {
        id: makeActionId(action),
        sender,
        task_id: taskId,
        action,
        amount,
        payload: buildPayload(payload),
        nonce,
        timestamp: new Date().toISOString(),
      };

      const signedEnvelope = await signQsdmTaskActionWithCli(envelope);
      const maxAttempts = getTaskActionSubmitMaxAttempts();
      const retryBaseMs = getTaskActionSubmitRetryBaseMs();

      for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
        try {
          const response = await axios.post<QsdmTaskActionSubmitResponse>(
            buildQsdmCoreApiUrl('/tasks/actions/submit-signed'),
            signedEnvelope,
            { timeout: 10000 }
          );

          return {
            ...response.data,
            client_nonce: signedEnvelope.nonce,
          };
        } catch (error) {
          if (axios.isAxiosError(error) && error.response?.status === 409) {
            return {
              ...(error.response.data as QsdmTaskActionSubmitResponse),
              client_nonce: signedEnvelope.nonce,
            };
          }
          if (
            !isTransientTaskActionSubmitError(error) ||
            attempt === maxAttempts
          ) {
            throw error;
          }

          const status = axios.isAxiosError(error)
            ? error.response?.status || error.code || 'network'
            : 'unknown';
          console.warn(
            `QSDM signed ${action} submission attempt ${attempt}/${maxAttempts} failed (${status}); retrying the same action ID.`
          );
          await delay(retryBaseMs * 2 ** (attempt - 1));
        }
      }

      throw new Error('QSDM signed task action submission exhausted retries');
    };

    const response = await signAndSubmit(initialNonce);
    if (
      response.status === 'nonce_replay' &&
      typeof response.last_nonce === 'number'
    ) {
      const replayNonce = reserveTaskActionNonce(
        sender,
        response.last_nonce + 1
      );
      const replayResponse = await signAndSubmit(replayNonce);
      assertTaskActionSubmitted(replayResponse);
      if (waitForCommit) {
        await waitForTaskActionNonceCommit(sender, replayResponse.client_nonce);
      }
      return replayResponse;
    }

    assertTaskActionSubmitted(response);
    if (waitForCommit) {
      await waitForTaskActionNonceCommit(sender, response.client_nonce);
    }
    return response;
  });
};
