import { createHash, randomBytes } from 'crypto';
import fs from 'fs';
import os from 'os';
import path from 'path';

const PAIRING_CODE_PREFIX = 'QSDM-EDGE-1.';
const FEDERATION_PAIRING_CODE_PREFIX = 'QSDM-EDGE-2.';
const PAIRING_CODE_MAX_LENGTH = 4096;
const MAX_FEDERATION_INVITATION_MS = 25 * 60 * 60 * 1000;
const OWNED_TOKEN_PREFIX = 'hive-mother-';
const OWNED_FEDERATION_TOKEN_PREFIX = 'hive-federation-';
const FEDERATION_WORKLOAD_IDS = [
  'qsdm.cpu.hash-chain.v1',
  'qsdm.gpu.cuda-mix.v1',
  'qsdm.ram.memory-scan.v1',
] as const;

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
  federation_context?: string;
};

type MotherHivePairingPayload = {
  version: 1 | 2;
  kind: 'mother' | 'mother-federation';
  relay_url: string;
  token: string;
  offer_id?: string;
  provider_name?: string;
  provider_wallet?: string;
  consumer_wallet?: string;
  expires_at?: string;
  workload_ids?: string[];
  federation_context?: string;
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

const isFederationConnectionExpired = (
  value: Partial<EdgeRelayConnectionConfig>,
  now = Date.now()
) =>
  value.connection_mode === 'internet-federation' &&
  (typeof value.expires_at !== 'string' ||
    !Number.isFinite(Date.parse(value.expires_at)) ||
    Date.parse(value.expires_at) <= now);

const decodeFederationContext = (encoded: string) => {
  if (!encoded || encoded.length > PAIRING_CODE_MAX_LENGTH) {
    throw new Error('The federation invitation has no credential context.');
  }
  let parsed: unknown;
  try {
    const raw = Buffer.from(encoded, 'base64url');
    if (raw.toString('base64url') !== encoded) {
      throw new Error('non-canonical federation context');
    }
    parsed = JSON.parse(raw.toString('utf8'));
  } catch {
    throw new Error('The federation invitation credential context is damaged.');
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('The federation invitation credential context is damaged.');
  }
  const context = parsed as Record<string, unknown>;
  const allowedKeys = new Set([
    'version',
    'relay_url',
    'offer_id',
    'provider_name',
    'provider_wallet',
    'consumer_wallet',
    'expires_at',
    'workload_ids',
  ]);
  if (Object.keys(context).some((key) => !allowedKeys.has(key))) {
    throw new Error('The federation invitation context has unexpected data.');
  }
  if (
    context.version !== 1 ||
    typeof context.relay_url !== 'string' ||
    typeof context.offer_id !== 'string' ||
    !/^[a-z0-9][a-z0-9._:-]{2,95}$/i.test(context.offer_id) ||
    typeof context.provider_name !== 'string' ||
    context.provider_name.trim() !== context.provider_name ||
    context.provider_name.length < 1 ||
    context.provider_name.length > 80 ||
    typeof context.expires_at !== 'string' ||
    !Number.isFinite(Date.parse(context.expires_at)) ||
    !Array.isArray(context.workload_ids)
  ) {
    throw new Error('The federation invitation credential context is invalid.');
  }
  for (const walletKey of ['provider_wallet', 'consumer_wallet'] as const) {
    const wallet = context[walletKey];
    if (
      wallet !== undefined &&
      (typeof wallet !== 'string' || !/^[a-z0-9]{32,128}$/i.test(wallet))
    ) {
      throw new Error(
        'The federation invitation credential context is invalid.'
      );
    }
  }
  const workloads = context.workload_ids;
  if (
    workloads.length < 1 ||
    workloads.length > FEDERATION_WORKLOAD_IDS.length ||
    workloads.some(
      (workload) =>
        typeof workload !== 'string' ||
        !FEDERATION_WORKLOAD_IDS.includes(
          workload as (typeof FEDERATION_WORKLOAD_IDS)[number]
        )
    ) ||
    new Set(workloads).size !== workloads.length ||
    JSON.stringify([...workloads].sort()) !== JSON.stringify(workloads)
  ) {
    throw new Error('The federation invitation credential context is invalid.');
  }
  return context;
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
    if (value.connection_mode === 'internet-federation') {
      if (
        !normalizeRelayURL(value.relay_url).startsWith('https://') ||
        typeof value.federation_context !== 'string' ||
        typeof value.expires_at !== 'string' ||
        !Number.isFinite(Date.parse(value.expires_at))
      ) {
        return false;
      }
      const context = decodeFederationContext(value.federation_context);
      if (
        normalizeRelayURL(String(context.relay_url || '')) !==
          normalizeRelayURL(value.relay_url) ||
        context.offer_id !== value.offer_id ||
        context.provider_name !== value.provider_name ||
        context.provider_wallet !== value.provider_wallet ||
        context.consumer_wallet !== value.consumer_wallet ||
        context.expires_at !== value.expires_at ||
        JSON.stringify(context.workload_ids || []) !==
          JSON.stringify(value.workload_ids || [])
      ) {
        return false;
      }
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
  const connectionConfigExists = fs.existsSync(
    getEdgeRelayConnectionConfigPath()
  );
  const configuredConnection = getEdgeRelayConnectionConfig();
  if (connectionConfigExists && !configuredConnection) {
    return '';
  }
  if (configuredConnection) {
    if (isFederationConnectionExpired(configuredConnection)) {
      return '';
    }
    return fs.existsSync(configuredConnection.token_file)
      ? configuredConnection.token_file
      : '';
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

export const getEdgeRelayFederationContext = () => {
  const configuredConnection = getEdgeRelayConnectionConfig();
  if (
    !configuredConnection ||
    configuredConnection.connection_mode !== 'internet-federation' ||
    isFederationConnectionExpired(configuredConnection)
  ) {
    return '';
  }
  return configuredConnection.federation_context || '';
};

const decodeMotherHivePairingCode = (
  value: string
): MotherHivePairingPayload => {
  const pairingCode = String(value || '').trim();
  if (
    pairingCode.length > PAIRING_CODE_MAX_LENGTH ||
    (!pairingCode.startsWith(PAIRING_CODE_PREFIX) &&
      !pairingCode.startsWith(FEDERATION_PAIRING_CODE_PREFIX))
  ) {
    throw new Error(
      'Paste the Mother Hive pairing code shown by QSDM Edge Control.'
    );
  }

  let parsed: unknown;
  try {
    const prefix = pairingCode.startsWith(FEDERATION_PAIRING_CODE_PREFIX)
      ? FEDERATION_PAIRING_CODE_PREFIX
      : PAIRING_CODE_PREFIX;
    const encoded = pairingCode.slice(prefix.length);
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
    'federation_context',
  ]);
  if (Object.keys(payload).some((key) => !allowedKeys.has(key))) {
    throw new Error('The Mother Hive pairing code contains unexpected data.');
  }
  if (!['mother', 'mother-federation'].includes(String(payload.kind))) {
    throw new Error('This pairing code is not a Mother Hive code.');
  }
  const isFederation = payload.kind === 'mother-federation';
  if (isFederation && payload.version !== 2) {
    throw new Error(
      'This federation invitation uses a permanent legacy credential. Create a new invitation in the latest QSDM Edge Control.'
    );
  }
  if (!isFederation && payload.version !== 1) {
    throw new Error('This pairing code version is not supported.');
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
    if (isFederation && expiry > Date.now() + MAX_FEDERATION_INVITATION_MS) {
      throw new Error(
        'The Mother Hive federation invitation exceeds the maximum lifetime.'
      );
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

  const federationContext = isFederation
    ? typeof payload.federation_context === 'string'
      ? payload.federation_context
      : ''
    : undefined;
  if (isFederation) {
    const context = decodeFederationContext(federationContext || '');
    const contextWorkloads = Array.isArray(context.workload_ids)
      ? context.workload_ids.filter(
          (value): value is string => typeof value === 'string'
        )
      : [];
    if (
      context.version !== 1 ||
      normalizeRelayURL(String(context.relay_url || '')) !== relayURL ||
      context.offer_id !== payload.offer_id ||
      context.provider_name !== payload.provider_name ||
      context.provider_wallet !== payload.provider_wallet ||
      context.consumer_wallet !== payload.consumer_wallet ||
      context.expires_at !== expiresAt ||
      JSON.stringify(contextWorkloads) !== JSON.stringify(workloadIds || [])
    ) {
      throw new Error(
        'The federation invitation metadata does not match its credential context.'
      );
    }
  }

  return {
    version: payload.version as MotherHivePairingPayload['version'],
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
    federation_context: federationContext,
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
    federation_context: payload.federation_context,
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
