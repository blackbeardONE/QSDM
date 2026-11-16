// PULSE, the non-custodial QSDM mining pool, from the member's side in Hive.
//
// Off by default: solo mining stays the default. When the member turns it on,
// the Miner task starts qsdmminer-console in pool mode (shares to the pool,
// network proofs still to QSDM Core) and the member joins the pool with one
// signed transaction on the chain. From then on the producer pays the member's
// mining rewards into a keyless escrow and the pool's signed settlement moves
// them to every member by share window through the chain. The pool never holds
// CELL. Leaving is one more transaction; an escrowed reward the pool never
// settled can be reclaimed by the member after the chain's reclaim timeout.
import fs from 'fs';
import path from 'path';

import axios from 'axios';

import { getAppDataPath } from 'main/node/helpers/getAppDataPath';
import { QsdmPulsePoolSettings, QsdmPulsePoolStatus } from 'models/api/qsdm';

import {
  QsdmPulseAction,
  resolveQsdmTaskActionApiUrl,
  submitQsdmPulseAction,
} from './qsdmTaskActions';
import { getQsdmTaskActionSender } from './qsdmTaskActionSigner';

export const QSDM_PULSE_DEFAULT_POOL_URL = 'https://qsdm.tech/pool/api';
// The miner mines solo after the pool has been unreachable this long, and
// tries the pool again every minute (PULSE design doc recommendation).
export const QSDM_PULSE_DEFAULT_FALLBACK_MINUTES = 5;
const MAX_FALLBACK_MINUTES = 24 * 60;
const RECEIPTS_SHOWN = 20;

const DEFAULT_SETTINGS: QsdmPulsePoolSettings = {
  enabled: false,
  poolUrl: QSDM_PULSE_DEFAULT_POOL_URL,
  backupUrl: '',
  fallbackMinutes: QSDM_PULSE_DEFAULT_FALLBACK_MINUTES,
  worker: '',
};

const pulseDir = () =>
  path.join(getAppDataPath(), 'namespace', 'qsdm-pulse-pool');

export const getQsdmPulseSettingsPath = () =>
  path.join(pulseDir(), 'settings.json');

export const getQsdmPulseReceiptsPath = () =>
  path.join(pulseDir(), 'receipts.jsonl');

// A pool URL must be HTTPS; plain HTTP only to this computer (a local pool).
export const isAcceptablePoolUrl = (value: string) => {
  try {
    const url = new URL(value);
    if (url.username || url.password) return false;
    if (url.protocol === 'https:') return true;
    return (
      url.protocol === 'http:' &&
      ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)
    );
  } catch {
    return false;
  }
};

export const normalizeQsdmPulseSettings = (
  input: Partial<QsdmPulsePoolSettings>
): QsdmPulsePoolSettings => {
  const merged = { ...DEFAULT_SETTINGS, ...input };
  const poolUrl = (merged.poolUrl || '').trim().replace(/\/+$/, '');
  const backupUrl = (merged.backupUrl || '').trim().replace(/\/+$/, '');
  if (!isAcceptablePoolUrl(poolUrl)) {
    throw new Error(
      'The pool URL must be https:// (or http:// to this computer)'
    );
  }
  if (backupUrl && !isAcceptablePoolUrl(backupUrl)) {
    throw new Error(
      'The backup pool URL must be https:// (or http:// to this computer)'
    );
  }
  const fallbackMinutes = Math.round(Number(merged.fallbackMinutes));
  if (
    !Number.isFinite(fallbackMinutes) ||
    fallbackMinutes < 1 ||
    fallbackMinutes > MAX_FALLBACK_MINUTES
  ) {
    throw new Error('Fallback minutes must be between 1 and 1440');
  }
  const worker = (merged.worker || '').trim();
  if (worker && !/^[A-Za-z0-9_-]{1,32}$/.test(worker)) {
    throw new Error('The worker name may use 1-32 letters, digits, _ and -');
  }
  return {
    enabled: Boolean(merged.enabled),
    poolUrl,
    backupUrl: backupUrl === poolUrl ? '' : backupUrl,
    fallbackMinutes,
    worker,
  };
};

export const getQsdmPulseSettings = (): QsdmPulsePoolSettings => {
  try {
    const raw = JSON.parse(
      fs.readFileSync(getQsdmPulseSettingsPath(), 'utf8')
    ) as Partial<QsdmPulsePoolSettings>;
    return normalizeQsdmPulseSettings(raw);
  } catch {
    return { ...DEFAULT_SETTINGS };
  }
};

export const setQsdmPulseSettings = (
  input: Partial<QsdmPulsePoolSettings>
): QsdmPulsePoolSettings => {
  const next = normalizeQsdmPulseSettings({
    ...getQsdmPulseSettings(),
    ...input,
  });
  fs.mkdirSync(pulseDir(), { recursive: true });
  const tmp = `${getQsdmPulseSettingsPath()}.tmp`;
  fs.writeFileSync(tmp, JSON.stringify(next, null, 2), { mode: 0o600 });
  fs.renameSync(tmp, getQsdmPulseSettingsPath());
  return next;
};

// The miner flags for pool mode; none when the pool is off (solo mining).
// Pool mode needs the operator signing flags too (the miner signs the pool
// login and the network proofs with the wallet key); the launcher checks that.
export const buildQsdmPulseMinerArgs = (
  settings: QsdmPulsePoolSettings = getQsdmPulseSettings(),
  receiptsPath: string = getQsdmPulseReceiptsPath()
) => {
  if (!settings.enabled) return [];
  const args = [
    `--pool-url=${settings.poolUrl}`,
    `--pool-fallback-minutes=${settings.fallbackMinutes}`,
    `--pool-receipts-file=${receiptsPath}`,
  ];
  if (settings.backupUrl) args.push(`--pool-backup-url=${settings.backupUrl}`);
  if (settings.worker) args.push(`--pool-worker=${settings.worker}`);
  return args;
};

type PoolInfo = {
  pool_id?: string;
  public_key?: string;
  mode?: string;
  note?: string;
  fee_bps?: number;
  fee_to?: string;
  fee_network_cap_bps?: number;
  payout_floor_dust?: number;
  window_factor?: number;
};

type PulseState = {
  activation_height?: number;
  reclaim_after_blocks?: number;
  active?: boolean;
  tip?: number;
  pool?: {
    escrowed?: {
      proof_id: string;
      owner: string;
      dust: number;
      height: number;
    }[];
    owed?: Record<string, number>;
  };
};

const getFirstAnsweringPool = async (settings: QsdmPulsePoolSettings) => {
  const urls = [settings.poolUrl, settings.backupUrl].filter(Boolean);
  let lastError: unknown;
  for (const base of urls) {
    try {
      const { data } = await axios.get<PoolInfo>(`${base}/v1/info`, {
        timeout: 8000,
      });
      return { base, info: data };
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError instanceof Error
    ? lastError
    : new Error('No pool endpoint answered');
};

// The receipts the miner kept (newest last), at most RECEIPTS_SHOWN.
export const readQsdmPulseReceipts = (
  receiptsPath: string = getQsdmPulseReceiptsPath()
) => {
  try {
    const lines = fs
      .readFileSync(receiptsPath, 'utf8')
      .split('\n')
      .filter((line) => line.trim());
    const recent = lines.slice(-RECEIPTS_SHOWN).flatMap((line) => {
      try {
        const parsed = JSON.parse(line) as {
          receipt?: {
            index?: number;
            difficulty?: number;
            at_unix_ms?: number;
            job_id?: string;
          };
        };
        return parsed.receipt ? [parsed.receipt] : [];
      } catch {
        return [];
      }
    });
    return { total: lines.length, recent };
  } catch {
    return { total: 0, recent: [] };
  }
};

// Reclaimable = escrowed for this owner and old enough for the next block.
export const reclaimableQsdmPulseProofs = (
  state: PulseState | undefined,
  owner: string
) => {
  const after = state?.reclaim_after_blocks || 0;
  if (!state?.pool?.escrowed || !after || state.tip === undefined) return [];
  const next = state.tip + 1;
  return state.pool.escrowed.filter(
    (entry) =>
      entry.owner === owner &&
      next >= entry.height &&
      next - entry.height >= after
  );
};

const DUST_PER_CELL = 100_000_000;

export const getQsdmPulsePoolStatus =
  async (): Promise<QsdmPulsePoolStatus> => {
    const settings = getQsdmPulseSettings();
    const address = getQsdmTaskActionSender() || '';
    const receipts = readQsdmPulseReceipts();
    const status: QsdmPulsePoolStatus = {
      settings,
      address,
      receiptsTotal: receipts.total,
      recentReceipts: receipts.recent,
      checkedAt: new Date().toISOString(),
    };

    try {
      const { base, info } = await getFirstAnsweringPool(settings);
      status.poolAnswering = base;
      status.poolId = info.pool_id;
      status.poolMode = info.mode;
      status.poolNote = info.note;
      status.feeBps = info.fee_bps;
      status.feeTo = info.fee_to;
      status.networkFeeCapBps = info.fee_network_cap_bps;
      status.payoutFloorCell =
        typeof info.payout_floor_dust === 'number'
          ? info.payout_floor_dust / DUST_PER_CELL
          : undefined;
      if (address) {
        try {
          const { data } = await axios.get<Record<string, unknown>>(
            `${base}/v1/address/${address}`,
            { timeout: 8000 }
          );
          status.poolStats = data;
        } catch {
          // No shares from this address yet.
        }
      }
    } catch (error) {
      status.poolError = (error as Error).message;
    }

    try {
      const api = resolveQsdmTaskActionApiUrl();
      if (address) {
        const { data: member } = await axios.get<{
          member: boolean;
          pool?: string;
        }>(`${api}/pulse/member`, {
          params: { owner: address },
          timeout: 8000,
        });
        status.member = member.member;
        status.memberPool = member.pool || undefined;
      }
      const poolForState = status.memberPool || status.poolId;
      const { data: state } = await axios.get<PulseState>(
        `${api}/pulse/state`,
        {
          params: poolForState ? { pool: poolForState } : undefined,
          timeout: 8000,
        }
      );
      status.ruleActive = Boolean(state.active);
      status.activationHeight = state.activation_height || undefined;
      status.reclaimAfterBlocks = state.reclaim_after_blocks || undefined;
      status.tip = state.tip;
      if (address && state.pool) {
        const mine = (state.pool.escrowed || []).filter(
          (e) => e.owner === address
        );
        status.escrowedCell =
          mine.reduce((sum, e) => sum + e.dust, 0) / DUST_PER_CELL;
        status.owedCell = (state.pool.owed?.[address] || 0) / DUST_PER_CELL;
        status.reclaimableProofs = reclaimableQsdmPulseProofs(
          state,
          address
        ).map((e) => e.proof_id);
      }
    } catch (error) {
      status.chainError = (error as Error).message;
    }
    return status;
  };

export const performQsdmPulseAction = async (
  request: { op: 'join' } | { op: 'leave' } | { op: 'reclaim' }
) => {
  let action: QsdmPulseAction;
  if (request.op === 'join') {
    const status = await getQsdmPulsePoolStatus();
    if (!status.poolId) {
      throw new Error(
        status.poolError || 'The pool did not answer; try again shortly'
      );
    }
    if (status.member) {
      throw new Error('This wallet is already in a pool');
    }
    action = { op: 'join', pool: status.poolId };
  } else if (request.op === 'leave') {
    action = { op: 'leave' };
  } else {
    const status = await getQsdmPulsePoolStatus();
    const proofs = (status.reclaimableProofs || []).slice(0, 256);
    if (proofs.length === 0) {
      throw new Error('Nothing is reclaimable yet');
    }
    action = { op: 'reclaim', proofs };
  }
  return submitQsdmPulseAction(action);
};
