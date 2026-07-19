// cspell:ignore nosniff
import { randomBytes, timingSafeEqual } from 'crypto';
import { BrowserWindow, dialog } from 'electron';
import fs from 'fs';
import http from 'http';
import path from 'path';

import { getQsdmCellAccount } from 'main/controllers/qsdm/getQsdmCellAccount';
import { getAppDataPath } from 'main/node/helpers/getAppDataPath';

import { signQsdmMessageWithCli } from './qsdmTaskActions';
import {
  getQsdmTaskActionSender,
  getQsdmTaskActionSignerStatus,
} from './qsdmTaskActionSigner';
import { submitQsdmWalletTransferIntent } from './qsdmWalletTransfer';

import type { Event } from 'electron';
import type {
  QsdmWalletProviderPermission,
  QsdmWalletProviderPermissionsResponse,
  QsdmWalletProviderRevokeResponse,
} from 'models/api/qsdm';

const BROKER_VERSION = 'qsdm-hive-wallet-provider/v1';
const MAX_REQUEST_BYTES = 64 * 1024;
const MAX_MESSAGE_BYTES = 16 * 1024;
const INTERNAL_EXTENSION_ORIGIN = 'qsdm-extension://wallet-popup';
const SAFE_ADDRESS = /^[a-zA-Z0-9]{32,128}$/;

type WalletProviderMethod =
  | 'qsdm_ping'
  | 'qsdm_getWalletInfo'
  | 'qsdm_openWallet'
  | 'qsdm_requestAccounts'
  | 'qsdm_accounts'
  | 'qsdm_getBalance'
  | 'qsdm_signMessage'
  | 'qsdm_sendTransaction'
  | 'qsdm_disconnect';

interface WalletProviderRequest {
  version?: string;
  id?: string;
  origin?: string;
  method?: WalletProviderMethod | string;
  params?: unknown;
}

interface WalletProviderPermissionsFile {
  version: 1;
  permissions: QsdmWalletProviderPermission[];
}

interface WalletProviderBrokerOptions {
  openWallet: () => void;
  showHive: () => void;
}

interface WalletProviderBrokerState {
  version: typeof BROKER_VERSION;
  host: '127.0.0.1';
  port: number;
  token: string;
  pid: number;
  startedAt: string;
}

let brokerServer: http.Server | undefined;
let brokerStatePath = '';
let approvalQueue: Promise<unknown> = Promise.resolve();

const writePrivateJson = (filePath: string, value: unknown) => {
  fs.mkdirSync(path.dirname(filePath), { recursive: true, mode: 0o700 });
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, {
    mode: 0o600,
  });
  try {
    fs.chmodSync(filePath, 0o600);
  } catch {
    // Windows uses the current user's profile ACL.
  }
};

const getProviderDirectory = () =>
  path.join(getAppDataPath(), 'wallet-provider');

const getPermissionsPath = () =>
  path.join(getProviderDirectory(), 'permissions.json');

const readPermissions = (): WalletProviderPermissionsFile => {
  try {
    const parsed = JSON.parse(
      fs.readFileSync(getPermissionsPath(), 'utf-8')
    ) as WalletProviderPermissionsFile;
    if (parsed.version === 1 && Array.isArray(parsed.permissions)) {
      return {
        version: 1,
        permissions: parsed.permissions.filter(
          (permission) =>
            permission &&
            typeof permission.origin === 'string' &&
            typeof permission.address === 'string' &&
            typeof permission.connectedAt === 'string' &&
            typeof permission.lastUsedAt === 'string'
        ),
      };
    }
  } catch {
    // A missing or damaged permission file is equivalent to no permissions.
  }
  return { version: 1, permissions: [] };
};

const writePermissions = (permissions: QsdmWalletProviderPermission[]) => {
  writePrivateJson(getPermissionsPath(), { version: 1, permissions });
};

const normalizeOrigin = (value: string) => {
  if (value === INTERNAL_EXTENSION_ORIGIN) return value;
  const parsed = new URL(value);
  const localHttp =
    parsed.protocol === 'http:' &&
    ['127.0.0.1', 'localhost', '::1'].includes(parsed.hostname);
  if (parsed.protocol !== 'https:' && !localHttp) {
    throw new Error('QSDM wallet connections require HTTPS');
  }
  if (parsed.origin !== value) {
    throw new Error('QSDM wallet request origin must not contain a path');
  }
  return parsed.origin;
};

const getPermission = (origin: string, address: string) =>
  readPermissions().permissions.find(
    (permission) =>
      permission.origin === origin &&
      permission.address.toLowerCase() === address.toLowerCase()
  );

const requireConnectedPermission = (origin: string) => {
  const address = getQsdmTaskActionSender();
  if (!address || !getPermission(origin, address)) {
    throw new Error('This site is not connected to the active QSDM wallet');
  }
  return address;
};

const savePermission = (origin: string, address: string) => {
  const now = new Date().toISOString();
  const current = readPermissions().permissions.filter(
    (permission) => permission.origin !== origin
  );
  current.push({ origin, address, connectedAt: now, lastUsedAt: now });
  writePermissions(current);
};

const touchPermission = (origin: string, address: string) => {
  const current = readPermissions().permissions;
  const updated = current.map((permission) =>
    permission.origin === origin &&
    permission.address.toLowerCase() === address.toLowerCase()
      ? { ...permission, lastUsedAt: new Date().toISOString() }
      : permission
  );
  writePermissions(updated);
};

const removePermission = (origin: string) => {
  const current = readPermissions().permissions;
  writePermissions(
    current.filter((permission) => permission.origin !== origin)
  );
};

export const getQsdmWalletProviderPermissions =
  (): QsdmWalletProviderPermissionsResponse => ({
    permissions: [...readPermissions().permissions].sort((left, right) =>
      right.lastUsedAt.localeCompare(left.lastUsedAt)
    ),
  });

export const revokeQsdmWalletProviderPermission = (
  requestedOrigin: string
): QsdmWalletProviderRevokeResponse => {
  const origin = normalizeOrigin(requestedOrigin);
  const revoked = readPermissions().permissions.some(
    (permission) => permission.origin === origin
  );
  removePermission(origin);
  return { origin, revoked };
};

const enqueueApproval = <T>(callback: () => Promise<T>): Promise<T> => {
  const previous = approvalQueue;
  const pending = (async () => {
    try {
      await previous;
    } catch {
      // A rejected request must not block the next approval prompt.
    }
    return callback();
  })();
  approvalQueue = pending.then(
    () => undefined,
    () => undefined
  );
  return pending;
};

const confirmRequest = async ({
  title,
  message,
  detail,
  approveLabel,
}: {
  title: string;
  message: string;
  detail: string;
  approveLabel: string;
}) => {
  const window =
    BrowserWindow.getFocusedWindow() || BrowserWindow.getAllWindows()[0];
  const options = {
    title,
    message,
    detail,
    type: 'question' as const,
    buttons: [approveLabel, 'Reject'],
    defaultId: 1,
    cancelId: 1,
    noLink: true,
  };
  const response = window
    ? await dialog.showMessageBox(window, options)
    : await dialog.showMessageBox(options);
  if (response.response !== 0) {
    throw new Error('The QSDM wallet request was rejected');
  }
};

const getStringParam = (params: unknown, key: string, required = true) => {
  const value =
    params && typeof params === 'object'
      ? (params as Record<string, unknown>)[key]
      : undefined;
  if (typeof value !== 'string' || (required && !value.trim())) {
    throw new Error(`${key} must be a non-empty string`);
  }
  return value.trim();
};

export const handleWalletProviderRequest = async (
  request: WalletProviderRequest,
  options: WalletProviderBrokerOptions
) => {
  if (request.version !== BROKER_VERSION) {
    throw new Error('Unsupported QSDM wallet provider version');
  }
  if (
    request.id !== undefined &&
    (typeof request.id !== 'string' || request.id.length > 128)
  ) {
    throw new Error('Invalid QSDM wallet provider request id');
  }
  const origin = normalizeOrigin(request.origin || '');
  const method = request.method || '';
  const signer = getQsdmTaskActionSignerStatus();

  if (method === 'qsdm_ping') {
    return { version: BROKER_VERSION, hive: true, signerReady: signer.ready };
  }

  if (method === 'qsdm_openWallet') {
    options.openWallet();
    return { opened: true };
  }

  if (method === 'qsdm_getWalletInfo') {
    return {
      address: signer.sender || null,
      ready: signer.ready,
      connectedSites: readPermissions().permissions.length,
    };
  }

  if (!signer.ready || !signer.sender) {
    throw new Error('QSDM Hive does not have an unlocked signer wallet');
  }

  if (origin === INTERNAL_EXTENSION_ORIGIN) {
    throw new Error('The extension popup cannot request wallet signatures');
  }

  if (method === 'qsdm_requestAccounts') {
    const existing = getPermission(origin, signer.sender);
    if (!existing) {
      options.showHive();
      await enqueueApproval(() =>
        confirmRequest({
          title: 'Connect QSDM wallet',
          message: `${origin} wants to connect to QSDM Hive.`,
          detail: `The site will see this public address:\n${signer.sender}\n\nIt will not receive your private key or passphrase.`,
          approveLabel: 'Connect',
        })
      );
      savePermission(origin, signer.sender);
    } else {
      touchPermission(origin, signer.sender);
    }
    return [signer.sender];
  }

  if (method === 'qsdm_accounts') {
    return getPermission(origin, signer.sender) ? [signer.sender] : [];
  }

  const address = requireConnectedPermission(origin);
  touchPermission(origin, address);

  if (method === 'qsdm_getBalance') {
    const account = await getQsdmCellAccount({} as Event, { address });
    return {
      address,
      balance: account.balance ?? null,
      token: account.tokenSymbol,
      reachable: account.reachable,
    };
  }

  if (method === 'qsdm_disconnect') {
    removePermission(origin);
    return { disconnected: true };
  }

  if (method === 'qsdm_signMessage') {
    const message = getStringParam(request.params, 'message');
    if (Buffer.byteLength(message, 'utf-8') > MAX_MESSAGE_BYTES) {
      throw new Error('QSDM messages are limited to 16 KiB');
    }
    options.showHive();
    await enqueueApproval(() =>
      confirmRequest({
        title: 'Sign QSDM message',
        message: `${origin} is requesting a wallet signature.`,
        detail: `Address: ${address}\n\nMessage:\n${message}`,
        approveLabel: 'Sign',
      })
    );
    return signQsdmMessageWithCli(message);
  }

  if (method === 'qsdm_sendTransaction') {
    const recipient = getStringParam(request.params, 'recipient');
    if (!SAFE_ADDRESS.test(recipient)) {
      throw new Error('recipient is not a valid QSDM wallet address');
    }
    const rawAmount =
      request.params && typeof request.params === 'object'
        ? (request.params as Record<string, unknown>).amount
        : undefined;
    const amount = Number(rawAmount);
    if (
      !Number.isFinite(amount) ||
      amount <= 0 ||
      !Number.isSafeInteger(amount * 1e9)
    ) {
      throw new Error('amount must be greater than zero');
    }
    options.showHive();
    await enqueueApproval(() =>
      confirmRequest({
        title: 'Send CELL',
        message: `${origin} wants to send ${amount} CELL.`,
        detail: `From: ${address}\nTo: ${recipient}\nAmount: ${amount} CELL`,
        approveLabel: 'Send CELL',
      })
    );
    return submitQsdmWalletTransferIntent({ recipient, amount });
  }

  throw new Error(`Unsupported QSDM wallet method: ${method}`);
};

const readRequestBody = (request: http.IncomingMessage) =>
  new Promise<string>((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    request.on('data', (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_REQUEST_BYTES) {
        reject(new Error('QSDM wallet provider request is too large'));
        request.destroy();
        return;
      }
      chunks.push(chunk);
    });
    request.on('end', () => resolve(Buffer.concat(chunks).toString('utf-8')));
    request.on('error', reject);
  });

const tokenMatches = (authorization: string | undefined, token: string) => {
  const candidate = authorization?.replace(/^Bearer\s+/i, '') || '';
  const left = Buffer.from(candidate);
  const right = Buffer.from(token);
  return left.length === right.length && timingSafeEqual(left, right);
};

const sendJson = (
  response: http.ServerResponse,
  status: number,
  payload: unknown
) => {
  response.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
  });
  response.end(JSON.stringify(payload));
};

export const startQsdmWalletProviderBroker = async (
  options: WalletProviderBrokerOptions
) => {
  if (brokerServer) return;

  const token = randomBytes(32).toString('hex');
  const server = http.createServer(async (request, response) => {
    if (
      request.method !== 'POST' ||
      request.url !== '/v1/request' ||
      !tokenMatches(request.headers.authorization, token)
    ) {
      sendJson(response, 404, { error: 'not found' });
      return;
    }

    try {
      const payload = JSON.parse(
        await readRequestBody(request)
      ) as WalletProviderRequest;
      const result = await handleWalletProviderRequest(payload, options);
      sendJson(response, 200, { id: payload.id, ok: true, result });
    } catch (error) {
      sendJson(response, 400, {
        ok: false,
        error: error instanceof Error ? error.message : String(error),
      });
    }
  });

  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolve());
  });

  const address = server.address();
  if (!address || typeof address === 'string') {
    server.close();
    throw new Error('QSDM wallet provider could not bind its local broker');
  }

  brokerServer = server;
  brokerStatePath = path.join(getProviderDirectory(), 'broker.json');
  const state: WalletProviderBrokerState = {
    version: BROKER_VERSION,
    host: '127.0.0.1',
    port: address.port,
    token,
    pid: process.pid,
    startedAt: new Date().toISOString(),
  };
  writePrivateJson(brokerStatePath, state);
  console.log('QSDM Hive wallet provider is ready', {
    host: state.host,
    port: state.port,
    version: state.version,
  });
};

export const stopQsdmWalletProviderBroker = () => {
  if (brokerServer) {
    brokerServer.close();
    brokerServer = undefined;
  }
  if (brokerStatePath) {
    fs.rmSync(brokerStatePath, { force: true });
    brokerStatePath = '';
  }
};

export const QSDM_WALLET_PROVIDER_VERSION = BROKER_VERSION;
