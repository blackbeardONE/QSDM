import axios from 'axios';
import { randomBytes } from 'crypto';
import { app } from 'electron';
import fs from 'fs';
import path from 'path';

import {
  QSDM_CANONICAL_API_URL,
  QSDM_CANONICAL_GENESIS_HASH,
  QSDM_CANONICAL_GENESIS_STATE_ROOT,
  QSDM_CORE_API_URL,
  QSDM_DEFAULT_LOCAL_CORE_API_URL,
} from 'config/qsdm';
import type { QsdmBackupReadStatus } from 'models/api/qsdm';

type BlockSummary = QsdmBackupReadStatus['blocks'][number];
type ReadCheckpoint = {
  version: 1;
  canonicalApiUrl: string;
  genesisHash: string;
  genesisStateRoot: string;
  confirmedAt: string;
  blocks: BlockSummary[];
};

const HISTORY_LIMIT = 20;
const REQUEST_TIMEOUT_MS = 4_000;
const MAX_RESPONSE_BYTES = 16 * 1024 * 1024;
const MAX_CHECKPOINT_BYTES = 16 * 1024;
const HASH = /^[a-f0-9]{64}$/;
const normalizeUrl = (value: string) => value.replace(/\/+$/, '');
const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const isHeight = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const isHash = (value: unknown): value is string =>
  typeof value === 'string' && HASH.test(value);
const isTimestamp = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length <= 40 &&
  /^\d{4}-\d{2}-\d{2}T/.test(value) &&
  Number.isFinite(Date.parse(value));

const checkpointPath = () =>
  path.join(app.getPath('userData'), 'qsdm-read-checkpoint.json');

const parseSummaries = (
  values: unknown,
  from: number,
  to: number,
  stored = false
): BlockSummary[] | undefined => {
  if (
    !Array.isArray(values) ||
    values.length !== to - from + 1 ||
    values.length < 1 ||
    values.length > HISTORY_LIMIT
  ) {
    return undefined;
  }
  const blocks: BlockSummary[] = [];
  for (let index = 0; index < values.length; index += 1) {
    const value: unknown = values[index];
    if (
      !isRecord(value) ||
      value.height !== from + index ||
      !isHash(value.hash)
    ) {
      return undefined;
    }
    const stateRoot = stored ? value.stateRoot : value.state_root;
    if (
      (stateRoot !== undefined && !isHash(stateRoot)) ||
      (value.timestamp !== undefined && !isTimestamp(value.timestamp))
    ) {
      return undefined;
    }
    blocks.push({
      height: from + index,
      hash: value.hash,
      ...(isHash(stateRoot) ? { stateRoot } : {}),
      ...(isTimestamp(value.timestamp) ? { timestamp: value.timestamp } : {}),
    });
  }
  return blocks;
};

const readCheckpoint = (): ReadCheckpoint | undefined => {
  try {
    const filePath = checkpointPath();
    if (fs.statSync(filePath).size > MAX_CHECKPOINT_BYTES) return undefined;
    const raw: unknown = JSON.parse(fs.readFileSync(filePath, 'utf8'));
    if (
      !isRecord(raw) ||
      raw.version !== 1 ||
      raw.canonicalApiUrl !== normalizeUrl(QSDM_CANONICAL_API_URL) ||
      raw.genesisHash !== QSDM_CANONICAL_GENESIS_HASH ||
      raw.genesisStateRoot !== QSDM_CANONICAL_GENESIS_STATE_ROOT ||
      !isTimestamp(raw.confirmedAt) ||
      Date.parse(raw.confirmedAt) > Date.now() ||
      !Array.isArray(raw.blocks) ||
      raw.blocks.length < 1 ||
      raw.blocks.length > HISTORY_LIMIT
    ) {
      return undefined;
    }
    const first: unknown = raw.blocks[0];
    const last: unknown = raw.blocks[raw.blocks.length - 1];
    if (
      !isRecord(first) ||
      !isHeight(first.height) ||
      !isRecord(last) ||
      !isHeight(last.height) ||
      first.height !== Math.max(0, last.height - HISTORY_LIMIT + 1)
    ) {
      return undefined;
    }
    const blocks = parseSummaries(raw.blocks, first.height, last.height, true);
    if (!blocks) return undefined;
    return {
      version: 1,
      canonicalApiUrl: raw.canonicalApiUrl,
      genesisHash: raw.genesisHash,
      genesisStateRoot: raw.genesisStateRoot,
      confirmedAt: raw.confirmedAt,
      blocks,
    };
  } catch {
    return undefined;
  }
};

const writeCheckpoint = (checkpoint: ReadCheckpoint) => {
  const filePath = checkpointPath();
  const temporaryPath = `${filePath}.tmp-${process.pid}-${randomBytes(
    6
  ).toString('hex')}`;
  try {
    fs.mkdirSync(path.dirname(filePath), { recursive: true, mode: 0o700 });
    fs.writeFileSync(temporaryPath, `${JSON.stringify(checkpoint)}\n`, {
      encoding: 'utf8',
      mode: 0o600,
      flag: 'wx',
    });
    fs.renameSync(temporaryPath, filePath);
  } finally {
    if (fs.existsSync(temporaryPath)) fs.unlinkSync(temporaryPath);
  }
};

const readJson = async (url: string): Promise<unknown> => {
  const response = await axios.get<unknown>(url, {
    timeout: REQUEST_TIMEOUT_MS,
    maxContentLength: MAX_RESPONSE_BYTES,
    maxRedirects: 0,
  });
  return response.data;
};

const readBlockRange = async (apiUrl: string, from: number, to: number) => {
  const data = await readJson(
    `${apiUrl}/chain/blocks?from=${from}&to=${to}&limit=${to - from + 1}`
  );
  return isRecord(data) ? parseSummaries(data.blocks, from, to) : undefined;
};

// Call only after the canonical source has passed live genesis verification.
// These summaries are an observation of that source, never write authority.
export const recordQsdmReadCheckpoint = async (
  apiUrl: string,
  expectedTip: number
): Promise<void> => {
  if (
    normalizeUrl(apiUrl) !== normalizeUrl(QSDM_CANONICAL_API_URL) ||
    !isHeight(expectedTip)
  ) {
    return;
  }
  try {
    const previous = readCheckpoint();
    const previousTip = previous?.blocks[previous.blocks.length - 1];
    if (previousTip && previousTip.height > expectedTip) return;
    const blocks = await readBlockRange(
      normalizeUrl(apiUrl),
      Math.max(0, expectedTip - HISTORY_LIMIT + 1),
      expectedTip
    );
    if (!blocks) return;
    if (
      previousTip &&
      blocks.some(
        (block) =>
          block.height === previousTip.height && block.hash !== previousTip.hash
      )
    ) {
      return;
    }
    writeCheckpoint({
      version: 1,
      canonicalApiUrl: normalizeUrl(apiUrl),
      genesisHash: QSDM_CANONICAL_GENESIS_HASH,
      genesisStateRoot: QSDM_CANONICAL_GENESIS_STATE_ROOT,
      confirmedAt: new Date().toISOString(),
      blocks,
    });
  } catch {
    // An optional read cache must not break live safety checks or erase history.
  }
};

// The caller must establish a transport outage of the canonical source first.
// This never updates the shared Core URL or authorizes signing/submission.
export const getQsdmBackupRead = async (): Promise<
  QsdmBackupReadStatus | undefined
> => {
  const checkpoint = readCheckpoint();
  if (!checkpoint) return undefined;
  const firstBlock = checkpoint.blocks[0];
  const tipBlock = checkpoint.blocks[checkpoint.blocks.length - 1];
  const candidates = Array.from(
    new Set(
      [QSDM_CORE_API_URL, QSDM_DEFAULT_LOCAL_CORE_API_URL].map(normalizeUrl)
    )
  ).filter((apiUrl) => apiUrl !== normalizeUrl(QSDM_CANONICAL_API_URL));

  for (const apiUrl of candidates) {
    try {
      const status = await readJson(`${apiUrl}/status`);
      if (
        !isRecord(status) ||
        !isHeight(status.chain_tip) ||
        status.chain_tip < tipBlock.height ||
        !isHeight(status.peers)
      ) {
        continue;
      }
      const genesis = await readBlockRange(apiUrl, 0, 0);
      if (
        genesis?.[0].hash !== QSDM_CANONICAL_GENESIS_HASH ||
        genesis[0].stateRoot !== QSDM_CANONICAL_GENESIS_STATE_ROOT
      ) {
        continue;
      }
      const history = await readBlockRange(
        apiUrl,
        firstBlock.height,
        tipBlock.height
      );
      if (
        !history ||
        history.some(
          (block, index) => block.hash !== checkpoint.blocks[index].hash
        )
      ) {
        continue;
      }
      return {
        sourceApiUrl: apiUrl,
        checkpointHeight: tipBlock.height,
        checkpointHash: tipBlock.hash,
        confirmedAt: checkpoint.confirmedAt,
        checkedAt: new Date().toISOString(),
        // Use only canonical-cached metadata. The backup's later tip and
        // payloads have not been verified by this bounded read-only check.
        blocks: checkpoint.blocks,
        reportedTip: status.chain_tip,
        peers: status.peers,
      };
    } catch {
      // Try the next explicitly configured/local source without mutating it.
    }
  }
  return undefined;
};
