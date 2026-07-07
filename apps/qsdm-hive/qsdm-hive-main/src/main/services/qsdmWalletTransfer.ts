import axios from 'axios';
import { spawn } from 'child_process';
import { randomBytes } from 'crypto';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import {
  QsdmSignedTransactionEnvelope,
  QsdmSubmitSignedTransactionResponse,
  QsdmWalletNonceResponse,
} from 'models/api/qsdm';

import {
  getQsdmTaskActionCliPath,
  getQsdmTaskActionKeystorePath,
  getQsdmTaskActionPassphraseFile,
  getQsdmTaskActionSender,
  getQsdmTaskActionSignerMode,
} from './qsdmTaskActionSigner';
import { assertQsdmCanonicalChainSafety } from './qsdmCanonicalChain';

type UnsignedQsdmWalletEnvelope = Omit<
  QsdmSignedTransactionEnvelope,
  'signature' | 'public_key'
> & {
  signature?: string;
  public_key?: string;
};

type QsdmWalletTransferParams = {
  recipient: string;
  amount: number;
  fee?: number;
};

const readEnv = (key: string, fallback = '') => {
  const value = process.env[key];
  return value?.trim() || fallback;
};

const makeTransferId = () =>
  `hive_wallet_${Date.now()}_${randomBytes(8).toString('hex')}`;

const getSignerTimeoutMs = () => {
  const raw = readEnv('QSDM_WALLET_SIGNER_TIMEOUT_MS', '30000');
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 30000;
};

const assertWalletSignerConfigured = () => {
  if (getQsdmTaskActionSignerMode() !== 'cli') {
    throw new Error('QSDM_TASK_ACTION_SIGNER=cli is required to send CELL');
  }
  if (!getQsdmTaskActionSender()) {
    throw new Error(
      'QSDM_TASK_ACTION_SENDER or QSDM_WALLET_ADDRESS is required to send CELL'
    );
  }
  if (!getQsdmTaskActionPassphraseFile()) {
    throw new Error(
      'QSDM_TASK_ACTION_PASSPHRASE_FILE or QSDM_PASSPHRASE_FILE is required to send CELL'
    );
  }
};

const getNextWalletNonce = async (sender: string) => {
  const url = new URL(buildQsdmCoreApiUrl('/wallet/nonce'));
  url.searchParams.set('sender', sender);
  const response = await axios.get<QsdmWalletNonceResponse>(url.toString(), {
    timeout: 10000,
  });
  return response.data.next;
};

const signQsdmWalletTransferWithCli = async (
  envelope: UnsignedQsdmWalletEnvelope
): Promise<QsdmSignedTransactionEnvelope> => {
  assertWalletSignerConfigured();

  const args = ['wallet', 'sign-tx', '--envelope-file', '-'];
  const keystorePath = getQsdmTaskActionKeystorePath();
  const passphraseFile = getQsdmTaskActionPassphraseFile();

  if (keystorePath) {
    args.push('--in', keystorePath);
  }
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
      reject(new Error('qsdmcli wallet transfer signing timed out'));
    }, getSignerTimeoutMs());

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
            `qsdmcli wallet sign-tx failed with exit code ${code}: ${stderr.trim()}`
          )
        );
        return;
      }

      try {
        resolve(JSON.parse(stdout.trim()) as QsdmSignedTransactionEnvelope);
      } catch (error) {
        reject(error);
      }
    });
    child.stdin?.end(JSON.stringify(envelope));
  });
};

const isNonceConflict = (error: unknown) => {
  if (!axios.isAxiosError(error) || error.response?.status !== 409) {
    return false;
  }

  const body =
    typeof error.response.data === 'string'
      ? error.response.data
      : JSON.stringify(error.response.data);
  return body.toLowerCase().includes('nonce');
};

export const submitQsdmWalletTransferIntent = async ({
  recipient,
  amount,
  fee = 0,
}: QsdmWalletTransferParams): Promise<QsdmSubmitSignedTransactionResponse> => {
  await assertQsdmCanonicalChainSafety();
  assertWalletSignerConfigured();
  const sender = getQsdmTaskActionSender();
  if (!sender) {
    throw new Error('QSDM signer address is not configured');
  }

  const signAndSubmit = async (nonce: number) => {
    const envelope: UnsignedQsdmWalletEnvelope = {
      id: makeTransferId(),
      sender,
      recipient,
      amount,
      fee,
      geotag: '',
      parent_cells: [],
      nonce,
      timestamp: new Date().toISOString(),
    };

    const signedEnvelope = await signQsdmWalletTransferWithCli(envelope);
    const response = await axios.post<QsdmSubmitSignedTransactionResponse>(
      buildQsdmCoreApiUrl('/wallet/submit-signed'),
      signedEnvelope,
      { timeout: 10000 }
    );

    return response.data;
  };

  try {
    return await signAndSubmit(await getNextWalletNonce(sender));
  } catch (error) {
    if (isNonceConflict(error)) {
      return signAndSubmit(await getNextWalletNonce(sender));
    }
    throw error;
  }
};
