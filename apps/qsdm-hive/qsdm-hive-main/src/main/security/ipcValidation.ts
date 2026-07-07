import { Endpoints } from 'config/endpoints';

import { isAllowedExternalUrl } from './externalNavigation';

const MAX_TEXT = 4096;
const MAX_PAYLOAD = 1024 * 1024;
const MAX_KEYSTORE_JSON = 1024 * 1024;
const SAFE_ADDRESS = /^[a-zA-Z0-9]{32,128}$/;
const SAFE_TASK_ID = /^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$/;
const SAFE_CAPABILITY = /^[a-z0-9][a-z0-9._-]{0,127}$/;
const SAFE_SEMVER = /^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/;
const SAFE_TASK_ACTIONS = new Set([
  'start',
  'stop',
  'stake',
  'fund',
  'unstake',
  'submit',
  'claim',
  'withdraw',
  'migrate',
  'catalog-register',
  'catalog-update',
  'catalog-pause',
  'catalog-resume',
]);

type JsonObject = Record<string, unknown>;

const invalid = (endpoint: string, detail: string): never => {
  throw new Error(`Invalid IPC payload for ${endpoint}: ${detail}`);
};

const ensureObject = (endpoint: string, payload: unknown): JsonObject => {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    invalid(endpoint, 'expected an object');
  }

  return payload as JsonObject;
};

const ensureNoPayload = (endpoint: string, args: unknown[]) => {
  if (args.length > 1) {
    invalid(endpoint, 'expected no extra arguments');
  }

  if (args.length === 1 && args[0] !== undefined && args[0] !== null) {
    invalid(endpoint, 'expected no payload');
  }
};

const ensureString = (
  endpoint: string,
  object: JsonObject,
  key: string,
  options: {
    min?: number;
    max?: number;
    pattern?: RegExp;
  } = {}
): string => {
  const value = object[key];
  const min = options.min ?? 1;
  const max = options.max ?? MAX_TEXT;

  if (typeof value !== 'string') {
    invalid(endpoint, `${key} must be a string`);
  }

  const stringValue = value as string;

  if (stringValue.length < min || stringValue.length > max) {
    invalid(endpoint, `${key} length is outside the allowed range`);
  }

  if (options.pattern && !options.pattern.test(stringValue)) {
    invalid(endpoint, `${key} has an invalid format`);
  }

  return stringValue;
};

const ensureOptionalString = (
  endpoint: string,
  object: JsonObject,
  key: string,
  options: {
    min?: number;
    max?: number;
    pattern?: RegExp;
  } = {}
): string | undefined => {
  if (object[key] === undefined || object[key] === null) {
    return undefined;
  }

  return ensureString(endpoint, object, key, options);
};

const ensureNumber = (
  endpoint: string,
  object: JsonObject,
  key: string,
  options: {
    min?: number;
    max?: number;
    integer?: boolean;
  } = {}
): number => {
  const value = object[key];

  if (typeof value !== 'number' || !Number.isFinite(value)) {
    invalid(endpoint, `${key} must be a finite number`);
  }

  const numberValue = value as number;

  if (options.integer && !Number.isInteger(numberValue)) {
    invalid(endpoint, `${key} must be an integer`);
  }

  if (options.min !== undefined && numberValue < options.min) {
    invalid(endpoint, `${key} is below the allowed range`);
  }

  if (options.max !== undefined && numberValue > options.max) {
    invalid(endpoint, `${key} is above the allowed range`);
  }

  return numberValue;
};

const ensureOptionalNumber = (
  endpoint: string,
  object: JsonObject,
  key: string,
  options: {
    min?: number;
    max?: number;
    integer?: boolean;
  } = {}
): number | undefined => {
  if (object[key] === undefined || object[key] === null) {
    return undefined;
  }

  return ensureNumber(endpoint, object, key, options);
};

const ensureOptionalBoolean = (
  endpoint: string,
  object: JsonObject,
  key: string
): boolean | undefined => {
  if (object[key] === undefined || object[key] === null) {
    return undefined;
  }

  if (typeof object[key] !== 'boolean') {
    invalid(endpoint, `${key} must be a boolean`);
  }

  return object[key] as boolean;
};

const ensureBoolean = (
  endpoint: string,
  object: JsonObject,
  key: string
): boolean => {
  if (typeof object[key] !== 'boolean') {
    invalid(endpoint, `${key} must be a boolean`);
  }
  return object[key] as boolean;
};

const ensureOptionalHttpsUrl = (
  endpoint: string,
  object: JsonObject,
  key: string
) => {
  const value = ensureOptionalString(endpoint, object, key, {
    min: 0,
    max: 2048,
  });
  if (!value) return;

  try {
    const parsed = new URL(value);
    if (
      parsed.protocol !== 'https:' ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password
    ) {
      invalid(endpoint, `${key} must be an absolute HTTPS URL`);
    }
  } catch {
    invalid(endpoint, `${key} must be an absolute HTTPS URL`);
  }
};

const validateTransfer = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  ensureString(endpoint, object, 'accountName', { max: 128 });
  ensureNumber(endpoint, object, 'amount', { min: 0.000000001 });
  ensureString(endpoint, object, 'toWalletAddress', {
    pattern: SAFE_ADDRESS,
  });
};

const validateSignedTransaction = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  ensureString(endpoint, object, 'id', { max: 256 });
  ensureString(endpoint, object, 'sender', { pattern: SAFE_ADDRESS });
  ensureString(endpoint, object, 'recipient', { pattern: SAFE_ADDRESS });
  ensureNumber(endpoint, object, 'amount', { min: 0 });
  ensureNumber(endpoint, object, 'fee', { min: 0 });
  ensureString(endpoint, object, 'geotag', { min: 0, max: 512 });
  ensureString(endpoint, object, 'timestamp', { max: 128 });
  ensureString(endpoint, object, 'signature', { max: 16384 });
  ensureString(endpoint, object, 'public_key', { max: 16384 });
  ensureOptionalNumber(endpoint, object, 'nonce', { min: 0, integer: true });

  const parentCells = object.parent_cells;
  if (!Array.isArray(parentCells) || parentCells.length > 64) {
    invalid(endpoint, 'parent_cells must be a bounded array');
  }

  (parentCells as unknown[]).forEach((cell) => {
    if (typeof cell !== 'string' || cell.length > 256) {
      invalid(endpoint, 'parent_cells contains an invalid cell id');
    }
  });
};

const validateTaskAction = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  ensureString(endpoint, object, 'id', { max: 256 });
  ensureString(endpoint, object, 'sender', { pattern: SAFE_ADDRESS });
  ensureString(endpoint, object, 'task_id', { max: 256 });
  const action = ensureString(endpoint, object, 'action', { max: 64 });
  if (!SAFE_TASK_ACTIONS.has(action)) {
    invalid(endpoint, 'action is not supported');
  }
  ensureOptionalNumber(endpoint, object, 'amount', { min: 0 });
  ensureOptionalString(endpoint, object, 'payload', {
    min: 0,
    max: MAX_PAYLOAD,
  });
  ensureOptionalNumber(endpoint, object, 'nonce', { min: 0, integer: true });
  ensureString(endpoint, object, 'timestamp', { max: 128 });
  ensureString(endpoint, object, 'signature', { max: 16384 });
  ensureString(endpoint, object, 'public_key', { max: 16384 });
};

const validateTaskCatalogManage = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  const operation = ensureString(endpoint, object, 'operation', { max: 16 });
  if (!['publish', 'pause', 'resume'].includes(operation)) {
    invalid(endpoint, 'operation is not supported');
  }
  const taskId = ensureString(endpoint, object, 'taskId', {
    max: 128,
    pattern: SAFE_TASK_ID,
  });

  if (operation !== 'publish') return;
  const draft = ensureObject(endpoint, object.draft);
  const draftTaskId = ensureString(endpoint, draft, 'task_id', {
    max: 128,
    pattern: SAFE_TASK_ID,
  });
  if (draftTaskId !== taskId) {
    invalid(endpoint, 'draft.task_id must match taskId');
  }
  ensureString(endpoint, draft, 'name', { max: 120 });
  ensureOptionalString(endpoint, draft, 'description', {
    min: 0,
    max: 2000,
  });
  ensureBoolean(endpoint, draft, 'active');
  ensureOptionalNumber(endpoint, draft, 'minimum_stake_amount', { min: 0 });
  ensureOptionalNumber(endpoint, draft, 'reward_per_round', { min: 0 });
  const roundTime = ensureNumber(endpoint, draft, 'round_time', {
    min: 1,
    max: 10_000_000,
    integer: true,
  });
  const submissionWindow = ensureOptionalNumber(
    endpoint,
    draft,
    'submission_window',
    { min: 0, max: roundTime, integer: true }
  );
  const auditWindow = ensureOptionalNumber(endpoint, draft, 'audit_window', {
    min: 0,
    max: roundTime,
    integer: true,
  });
  if ((submissionWindow || 0) > roundTime || (auditWindow || 0) > roundTime) {
    invalid(endpoint, 'task windows cannot exceed round_time');
  }
  ensureOptionalHttpsUrl(endpoint, draft, 'metadata_url');
  ensureOptionalHttpsUrl(endpoint, draft, 'source_url');
  ensureOptionalHttpsUrl(endpoint, draft, 'icon_url');

  const runtime = ensureObject(endpoint, draft.runtime);
  const kind = ensureString(endpoint, runtime, 'kind', { max: 32 });
  if (kind !== 'capability') {
    invalid(endpoint, 'Task Studio currently publishes capability runtimes');
  }
  ensureString(endpoint, runtime, 'capability', {
    max: 128,
    pattern: SAFE_CAPABILITY,
  });
  ensureOptionalString(endpoint, runtime, 'min_hive_version', {
    max: 64,
    pattern: SAFE_SEMVER,
  });
  ensureOptionalNumber(endpoint, runtime, 'max_memory_mb', {
    min: 0,
    max: 4096,
    integer: true,
  });
  ensureOptionalNumber(endpoint, runtime, 'max_runtime_seconds', {
    min: 0,
    max: 86400,
    integer: true,
  });

  const { tags } = draft;
  if (tags !== undefined && tags !== null) {
    if (!Array.isArray(tags) || tags.length > 16) {
      invalid(endpoint, 'tags must be an array with at most 16 values');
    }
    (tags as unknown[]).forEach((tag) => {
      if (
        typeof tag !== 'string' ||
        tag.length < 1 ||
        tag.length > 32 ||
        !SAFE_CAPABILITY.test(tag)
      ) {
        invalid(endpoint, 'tags contains an invalid value');
      }
    });
  }

  const relayIds = draft.authorized_relay_ids;
  if (relayIds !== undefined && relayIds !== null) {
    if (!Array.isArray(relayIds) || relayIds.length > 16) {
      invalid(
        endpoint,
        'authorized_relay_ids must be an array with at most 16 values'
      );
    }
    (relayIds as unknown[]).forEach((relayId) => {
      if (
        typeof relayId !== 'string' ||
        relayId.length !== 64 ||
        !/^[0-9a-fA-F]+$/.test(relayId)
      ) {
        invalid(endpoint, 'authorized_relay_ids contains an invalid value');
      }
    });
  }
};

const validateSignerImport = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  const keystoreJson = ensureString(endpoint, object, 'keystoreJson', {
    max: MAX_KEYSTORE_JSON,
  });
  ensureString(endpoint, object, 'passphrase', { max: 4096 });

  try {
    const parsed = JSON.parse(keystoreJson);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      invalid(endpoint, 'keystoreJson must parse to a JSON object');
    }
  } catch {
    invalid(endpoint, 'keystoreJson must be valid JSON');
  }
};

const validateSignerCreate = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  ensureString(endpoint, object, 'passphrase', { min: 12, max: 4096 });
};

const validateSkyFangLink = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  ensureString(endpoint, object, 'code', { min: 4, max: 256 });

  const baseUrl = ensureOptionalString(endpoint, object, 'baseUrl', {
    max: 2048,
  });
  if (baseUrl && !isAllowedExternalUrl(baseUrl)) {
    invalid(endpoint, 'baseUrl must be an HTTP or HTTPS URL');
  }
};

const validateFaucetClaim = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload ?? {});
  ensureOptionalString(endpoint, object, 'address', { pattern: SAFE_ADDRESS });
  ensureOptionalNumber(endpoint, object, 'amount', {
    min: 0.000000001,
    max: 1000000,
  });
};

const validateReferralRegistration = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  ensureString(endpoint, object, 'referrer', { pattern: SAFE_ADDRESS });
  ensureString(endpoint, object, 'referralCode', {
    min: 6,
    max: 64,
    pattern: /^[0-9A-Za-z_-]+$/,
  });
  ensureOptionalString(endpoint, object, 'installId', {
    min: 1,
    max: 128,
    pattern: /^[0-9A-Za-z._:-]+$/,
  });
};

const validateReferralStatus = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload);
  ensureString(endpoint, object, 'referred', { pattern: SAFE_ADDRESS });
};

const validateReferralClaim = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload ?? {});
  ensureOptionalString(endpoint, object, 'referrer', { pattern: SAFE_ADDRESS });
  ensureOptionalString(endpoint, object, 'referred', { pattern: SAFE_ADDRESS });
};

const validateSignedCellLoop = (endpoint: string, payload: unknown) => {
  const object = ensureObject(endpoint, payload ?? {});
  ensureOptionalString(endpoint, object, 'taskId', { max: 256 });
  ensureOptionalNumber(endpoint, object, 'fundAmount', { min: 0 });
  ensureOptionalNumber(endpoint, object, 'stakeAmount', { min: 0 });
  ensureOptionalNumber(endpoint, object, 'rewardAmount', { min: 0 });
  ensureOptionalNumber(endpoint, object, 'waitSeconds', {
    min: 0,
    max: 3600,
  });
  ensureOptionalBoolean(endpoint, object, 'skipFund');
};

const validateHiveVersionPolicyOptions = (
  endpoint: string,
  payload: unknown
) => {
  if (payload === undefined || payload === null) {
    return;
  }

  const object = ensureObject(endpoint, payload);
  ensureOptionalBoolean(endpoint, object, 'forceRefresh');
};

export const validateIpcPayload = (
  endpoint: Endpoints | string,
  args: unknown[]
): void => {
  const payload = args[0];

  switch (endpoint) {
    case Endpoints.OPEN_BROWSER_WINDOW: {
      const object = ensureObject(endpoint, payload);
      const url = ensureString(endpoint, object, 'URL', { max: 2048 });
      if (!isAllowedExternalUrl(url)) {
        invalid(endpoint, 'URL must be HTTP or HTTPS');
      }
      break;
    }
    case Endpoints.COPY_TEXT_TO_CLIPBOARD: {
      const object = ensureObject(endpoint, payload);
      ensureString(endpoint, object, 'text', { max: 65536 });
      break;
    }
    case Endpoints.TRANSFER_CELL_FROM_MAIN_WALLET:
    case Endpoints.TRANSFER_CELL_FROM_STAKING_WALLET:
      validateTransfer(endpoint, payload);
      break;
    case Endpoints.SUBMIT_QSDM_SIGNED_TRANSACTION:
      validateSignedTransaction(endpoint, payload);
      break;
    case Endpoints.SUBMIT_QSDM_TASK_ACTION:
      validateTaskAction(endpoint, payload);
      break;
    case Endpoints.MANAGE_QSDM_TASK_CATALOG:
      validateTaskCatalogManage(endpoint, payload);
      break;
    case Endpoints.CREATE_QSDM_SIGNER_WALLET:
      validateSignerCreate(endpoint, payload);
      break;
    case Endpoints.IMPORT_QSDM_SIGNER_WALLET:
      validateSignerImport(endpoint, payload);
      break;
    case Endpoints.LINK_QSDM_SKYFANG_ACCOUNT:
      validateSkyFangLink(endpoint, payload);
      break;
    case Endpoints.PAIR_QSDM_MOTHER_HIVE: {
      const object = ensureObject(endpoint, payload);
      ensureString(endpoint, object, 'pairingCode', {
        min: 1,
        max: 4096,
        pattern: /^QSDM-EDGE-1\.[0-9A-Za-z_-]+$/,
      });
      break;
    }
    case Endpoints.CLAIM_QSDM_CELL_FAUCET:
      validateFaucetClaim(endpoint, payload);
      break;
    case Endpoints.REGISTER_QSDM_REFERRAL:
      validateReferralRegistration(endpoint, payload);
      break;
    case Endpoints.GET_QSDM_REFERRAL_STATUS:
      validateReferralStatus(endpoint, payload);
      break;
    case Endpoints.CLAIM_QSDM_REFERRAL_REWARD:
      validateReferralClaim(endpoint, payload);
      break;
    case Endpoints.RUN_QSDM_SIGNED_CELL_LOOP:
      validateSignedCellLoop(endpoint, payload);
      break;
    case Endpoints.GET_HIVE_VERSION_POLICY:
      validateHiveVersionPolicyOptions(endpoint, payload);
      break;
    case Endpoints.EXPORT_QSDM_SIGNER_WALLET_BACKUP:
    case Endpoints.SET_QSDM_MINER_REWARD_ADDRESS_TO_SIGNER:
    case Endpoints.GET_QSDM_REFERRAL_REWARD_POOL_STATUS:
    case Endpoints.DISCONNECT_QSDM_MOTHER_HIVE:
    case Endpoints.QUIT_APP:
      ensureNoPayload(endpoint, args);
      break;
    default:
      break;
  }
};
