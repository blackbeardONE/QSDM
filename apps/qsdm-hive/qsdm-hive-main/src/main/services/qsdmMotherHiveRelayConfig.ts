import { createHash, randomBytes } from 'crypto';
import fs from 'fs';
import os from 'os';
import path from 'path';

const PAIRING_CODE_PREFIX = 'QSDM-EDGE-1.';
const PAIRING_CODE_MAX_LENGTH = 4096;
const OWNED_TOKEN_PREFIX = 'hive-mother-';
const OWNED_FEDERATION_TOKEN_PREFIX = 'hive-federation-';

export type EdgeRelayConnectionMode = 'private-lan' | 'internet-federation';

export type EdgeRelayConnectionConfig = {
  schema_version: 1;
  relay_url: string;
  token_file: string;
  connection_mode: EdgeRelayConnectionMode;
  offer_id?: string;
  provider_name?: string;
  provider_wallet?: string;
  consumer_wallet?: string;
  expires_at?: string;
  workload_ids?: string[];
};

type MotherHivePairingPayload = {
  version: 1;
  kind: 'mother' | 'mother-federation';
  relay_url: string;
  token: string;
  offer_id?: string;
  provider_name?: string;
  provider_wallet?: string;
  consumer_wallet?: string;
  expires_at?: string;
  workload_ids?: string[];
};

const getConfigRoot = () => {
  if (process.platform === 'win32') {
    return process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming');
  }
  return process.env.XDG_CONFIG_HOME || path.join(os.homedir(), '.config');
};

export const getEdgeRelayConfigDirectory = () =>
  path.join(getConfigRoot(), 'QSDM', 'edge-pool');

export const getEdgeRelayConnectionConfigPath = () =>
  path.join(getEdgeRelayConfigDirectory(), 'mother-hive.json');

const getDisconnectedMarkerPath = () =>
  path.join(getEdgeRelayConfigDirectory(), 'mother-hive.disconnected');

const normalizeRelayURL = (value: string) => {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error(
      'The Mother Hive pairing code contains an invalid Relay address.'
    );
  }
  if (
    !['http:', 'https:'].includes(parsed.protocol) ||
    !parsed.hostname ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash
  ) {
    throw new Error(
      'The Mother Hive pairing code contains an invalid Relay address.'
    );
  }
  parsed.pathname = '/';
  return parsed.toString();
};

const isConnectionConfig = (
  value: Partial<EdgeRelayConnectionConfig>
): value is EdgeRelayConnectionConfig => {
  if (
    value.schema_version !== 1 ||
    typeof value.relay_url !== 'string' ||
    typeof value.token_file !== 'string' ||
    !path.isAbsolute(value.token_file)
  ) {
    return false;
  }
  try {
    normalizeRelayURL(value.relay_url);
    if (
      value.connection_mode &&
      !['private-lan', 'internet-federation'].includes(value.connection_mode)
    ) {
      return false;
    }
    return true;
  } catch {
    return false;
  }
};

export const getEdgeRelayConnectionConfig =
  (): EdgeRelayConnectionConfig | null => {
    try {
      const parsed = JSON.parse(
        fs.readFileSync(getEdgeRelayConnectionConfigPath(), 'utf8')
      ) as Partial<EdgeRelayConnectionConfig>;
      return isConnectionConfig(parsed)
        ? {
            ...parsed,
            relay_url: normalizeRelayURL(parsed.relay_url),
            connection_mode: parsed.connection_mode || 'private-lan',
          }
        : null;
    } catch {
      return null;
    }
  };

export const getDefaultEdgeRelayURL = () =>
  process.env.QSDM_EDGE_RELAY_URL ||
  process.env.QSDM_EDGE_POOL_URL ||
  getEdgeRelayConnectionConfig()?.relay_url ||
  'http://127.0.0.1:7740';

export const getDefaultEdgeRelayTokenFile = () => {
  if (process.env.QSDM_EDGE_RELAY_TOKEN_FILE) {
    return process.env.QSDM_EDGE_RELAY_TOKEN_FILE;
  }
  if (process.env.QSDM_EDGE_POOL_TOKEN_FILE) {
    return process.env.QSDM_EDGE_POOL_TOKEN_FILE;
  }
  const configuredToken = getEdgeRelayConnectionConfig()?.token_file;
  if (configuredToken && fs.existsSync(configuredToken)) {
    return configuredToken;
  }
  if (fs.existsSync(getDisconnectedMarkerPath())) {
    return '';
  }
  const motherCandidate = path.join(
    getEdgeRelayConfigDirectory(),
    'mother-hive.token'
  );
  if (fs.existsSync(motherCandidate)) {
    return motherCandidate;
  }
  const legacyCandidate = path.join(
    getEdgeRelayConfigDirectory(),
    'edge-pool.token'
  );
  return fs.existsSync(legacyCandidate) ? legacyCandidate : '';
};

const decodeMotherHivePairingCode = (
  value: string
): MotherHivePairingPayload => {
  const pairingCode = String(value || '').trim();
  if (
    pairingCode.length > PAIRING_CODE_MAX_LENGTH ||
    !pairingCode.startsWith(PAIRING_CODE_PREFIX)
  ) {
    throw new Error(
      'Paste the Mother Hive pairing code shown by QSDM Edge Control.'
    );
  }

  let parsed: unknown;
  try {
    const encoded = pairingCode.slice(PAIRING_CODE_PREFIX.length);
    parsed = JSON.parse(Buffer.from(encoded, 'base64url').toString('utf8'));
  } catch {
    throw new Error('The Mother Hive pairing code is damaged or incomplete.');
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('The Mother Hive pairing code is damaged or incomplete.');
  }
  const payload = parsed as Record<string, unknown>;
  const allowedKeys = new Set([
    'version',
    'kind',
    'relay_url',
    'token',
    'offer_id',
    'provider_name',
    'provider_wallet',
    'consumer_wallet',
    'expires_at',
    'workload_ids',
  ]);
  if (Object.keys(payload).some((key) => !allowedKeys.has(key))) {
    throw new Error('The Mother Hive pairing code contains unexpected data.');
  }
  if (
    payload.version !== 1 ||
    !['mother', 'mother-federation'].includes(String(payload.kind))
  ) {
    throw new Error('This pairing code is not a Mother Hive code.');
  }
  if (typeof payload.relay_url !== 'string') {
    throw new Error('The Mother Hive pairing code has no Relay address.');
  }
  if (
    typeof payload.token !== 'string' ||
    !/^[0-9a-fA-F]{64,}$/.test(payload.token) ||
    payload.token.length % 2 !== 0
  ) {
    throw new Error(
      'The Mother Hive pairing code has an invalid security key.'
    );
  }

  const relayURL = normalizeRelayURL(payload.relay_url);
  const isFederation = payload.kind === 'mother-federation';
  if (isFederation && !relayURL.startsWith('https://')) {
    throw new Error(
      'Internet federation invitations must use an HTTPS Relay address.'
    );
  }

  const expiresAt =
    typeof payload.expires_at === 'string' ? payload.expires_at : undefined;
  if (expiresAt) {
    const expiry = Date.parse(expiresAt);
    if (!Number.isFinite(expiry)) {
      throw new Error(
        'The Mother Hive federation invitation has an invalid expiry.'
      );
    }
    if (isFederation && expiry <= Date.now()) {
      throw new Error('The Mother Hive federation invitation has expired.');
    }
  } else if (isFederation) {
    throw new Error('The Mother Hive federation invitation has no expiry.');
  }

  const workloadIds = Array.isArray(payload.workload_ids)
    ? payload.workload_ids
        .filter((value): value is string => typeof value === 'string')
        .map((value) => value.trim())
        .filter((value) => /^[a-z0-9._-]{3,80}$/i.test(value))
        .slice(0, 16)
    : undefined;

  return {
    version: 1,
    kind: payload.kind as MotherHivePairingPayload['kind'],
    relay_url: relayURL,
    token: payload.token.toLowerCase(),
    offer_id:
      typeof payload.offer_id === 'string' &&
      /^[a-z0-9._:-]{3,96}$/i.test(payload.offer_id)
        ? payload.offer_id
        : undefined,
    provider_name:
      typeof payload.provider_name === 'string'
        ? payload.provider_name.trim().slice(0, 80)
        : undefined,
    provider_wallet:
      typeof payload.provider_wallet === 'string' &&
      /^[a-z0-9]{32,128}$/i.test(payload.provider_wallet)
        ? payload.provider_wallet
        : undefined,
    consumer_wallet:
      typeof payload.consumer_wallet === 'string' &&
      /^[a-z0-9]{32,128}$/i.test(payload.consumer_wallet)
        ? payload.consumer_wallet
        : undefined,
    expires_at: expiresAt,
    workload_ids: workloadIds,
  };
};

const writePrivateFileAtomic = (filePath: string, data: string) => {
  const directory = path.dirname(filePath);
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  const temporaryPath = path.join(
    directory,
    `.${path.basename(filePath)}.${randomBytes(8).toString('hex')}.tmp`
  );
  try {
    fs.writeFileSync(temporaryPath, data, { encoding: 'utf8', mode: 0o600 });
    fs.renameSync(temporaryPath, filePath);
    if (process.platform !== 'win32') {
      fs.chmodSync(filePath, 0o600);
    }
  } finally {
    try {
      fs.rmSync(temporaryPath, { force: true });
    } catch {
      // Best-effort cleanup after a failed atomic replace.
    }
  }
};

const removeOwnedMotherHiveTokens = (keepPath?: string) => {
  let entries: string[] = [];
  try {
    entries = fs.readdirSync(getEdgeRelayConfigDirectory());
  } catch {
    return;
  }
  for (const entry of entries) {
    if (
      (entry.startsWith(OWNED_TOKEN_PREFIX) ||
        entry.startsWith(OWNED_FEDERATION_TOKEN_PREFIX)) &&
      entry.endsWith('.token')
    ) {
      const candidate = path.join(getEdgeRelayConfigDirectory(), entry);
      if (!keepPath || path.resolve(candidate) !== path.resolve(keepPath)) {
        fs.rmSync(candidate, { force: true });
      }
    }
  }
};

export const pairQsdmMotherHiveRelay = (
  pairingCode: string
): EdgeRelayConnectionConfig => {
  const payload = decodeMotherHivePairingCode(pairingCode);
  const connectionMode: EdgeRelayConnectionMode =
    payload.kind === 'mother-federation'
      ? 'internet-federation'
      : 'private-lan';
  const tokenFingerprint = createHash('sha256')
    .update(payload.token, 'hex')
    .digest('hex')
    .slice(0, 16);
  const tokenPath = path.join(
    getEdgeRelayConfigDirectory(),
    `${
      connectionMode === 'internet-federation'
        ? OWNED_FEDERATION_TOKEN_PREFIX
        : OWNED_TOKEN_PREFIX
    }${tokenFingerprint}.token`
  );
  const config: EdgeRelayConnectionConfig = {
    schema_version: 1,
    relay_url: payload.relay_url,
    token_file: tokenPath,
    connection_mode: connectionMode,
    offer_id: payload.offer_id,
    provider_name: payload.provider_name,
    provider_wallet: payload.provider_wallet,
    consumer_wallet: payload.consumer_wallet,
    expires_at: payload.expires_at,
    workload_ids: payload.workload_ids,
  };

  writePrivateFileAtomic(tokenPath, `${payload.token}\n`);
  writePrivateFileAtomic(
    getEdgeRelayConnectionConfigPath(),
    `${JSON.stringify(config, null, 2)}\n`
  );
  fs.rmSync(getDisconnectedMarkerPath(), { force: true });
  removeOwnedMotherHiveTokens(tokenPath);
  return config;
};

export const disconnectQsdmMotherHiveRelay = () => {
  const configPath = getEdgeRelayConnectionConfigPath();
  const existed = fs.existsSync(configPath);
  fs.rmSync(configPath, { force: true });
  removeOwnedMotherHiveTokens();
  writePrivateFileAtomic(
    getDisconnectedMarkerPath(),
    `${new Date().toISOString()}\n`
  );
  return existed;
};
