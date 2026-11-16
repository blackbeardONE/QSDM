import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  buildQsdmPulseMinerArgs,
  isAcceptablePoolUrl,
  normalizeQsdmPulseSettings,
  QSDM_PULSE_DEFAULT_FALLBACK_MINUTES,
  QSDM_PULSE_DEFAULT_POOL_URL,
  readQsdmPulseReceipts,
  reclaimableQsdmPulseProofs,
} from './qsdmPulsePool';
import { buildQsdmMinerLaunchArgs } from './qsdmSystemTasks';
import { validateQsdmPulseAction } from './qsdmTaskActions';

jest.mock('main/node/helpers/getAppDataPath', () => ({
  getAppDataPath: () => '/tmp/qsdm-pulse-spec',
}));

const me = 'ab'.repeat(32);
const pool = 'cd'.repeat(32);

describe('PULSE pool settings', () => {
  it('is off by default and points at the hosted pool', () => {
    const settings = normalizeQsdmPulseSettings({});
    expect(settings.enabled).toBe(false);
    expect(settings.poolUrl).toBe(QSDM_PULSE_DEFAULT_POOL_URL);
    expect(settings.fallbackMinutes).toBe(QSDM_PULSE_DEFAULT_FALLBACK_MINUTES);
  });

  it('accepts HTTPS, and plain HTTP only to this computer', () => {
    expect(isAcceptablePoolUrl('https://qsdm.tech/pool/api')).toBe(true);
    expect(isAcceptablePoolUrl('http://127.0.0.1:17760/pool/api')).toBe(true);
    expect(isAcceptablePoolUrl('http://localhost:17760/pool/api')).toBe(true);
    expect(isAcceptablePoolUrl('http://pool.example.com/pool/api')).toBe(false);
    expect(isAcceptablePoolUrl('https://user:pass@qsdm.tech/pool/api')).toBe(
      false
    );
    expect(isAcceptablePoolUrl('file:///etc/passwd')).toBe(false);
    expect(isAcceptablePoolUrl('not a url')).toBe(false);
  });

  it('refuses bad values instead of saving them', () => {
    expect(() =>
      normalizeQsdmPulseSettings({ poolUrl: 'http://evil.example/' })
    ).toThrow();
    expect(() =>
      normalizeQsdmPulseSettings({ backupUrl: 'ftp://x' })
    ).toThrow();
    expect(() => normalizeQsdmPulseSettings({ fallbackMinutes: 0 })).toThrow();
    expect(() =>
      normalizeQsdmPulseSettings({ fallbackMinutes: 100000 })
    ).toThrow();
    expect(() => normalizeQsdmPulseSettings({ worker: 'bad name' })).toThrow();
  });

  it('drops a backup URL equal to the primary and trailing slashes', () => {
    const settings = normalizeQsdmPulseSettings({
      poolUrl: 'https://qsdm.tech/pool/api/',
      backupUrl: 'https://qsdm.tech/pool/api',
    });
    expect(settings.poolUrl).toBe('https://qsdm.tech/pool/api');
    expect(settings.backupUrl).toBe('');
  });
});

describe('PULSE miner flags', () => {
  it('adds nothing while the pool is off (solo mining)', () => {
    expect(buildQsdmPulseMinerArgs(normalizeQsdmPulseSettings({}))).toEqual([]);
  });

  it('passes the pool, backup, fallback, worker and receipts file when on', () => {
    expect(
      buildQsdmPulseMinerArgs(
        normalizeQsdmPulseSettings({
          enabled: true,
          backupUrl: 'https://backup.qsdm.tech/pool/api',
          fallbackMinutes: 7,
          worker: 'rig1',
        }),
        '/tmp/receipts.jsonl'
      )
    ).toEqual([
      '--pool-url=https://qsdm.tech/pool/api',
      '--pool-fallback-minutes=7',
      '--pool-receipts-file=/tmp/receipts.jsonl',
      '--pool-backup-url=https://backup.qsdm.tech/pool/api',
      '--pool-worker=rig1',
    ]);
  });

  it('reaches the miner command line after the signing flags', () => {
    const args = buildQsdmMinerLaunchArgs({
      configPath: 'miner.toml',
      logPath: 'miner.log',
      env: {},
      pulse: ['--pool-url=https://qsdm.tech/pool/api'],
    });
    expect(args[args.length - 1]).toBe('--pool-url=https://qsdm.tech/pool/api');
    expect(args).toContain('--compute-backend=cuda');
  });
});

describe('PULSE member actions', () => {
  it('validates what may be signed', () => {
    expect(() => validateQsdmPulseAction({ op: 'join', pool })).not.toThrow();
    expect(() => validateQsdmPulseAction({ op: 'leave' })).not.toThrow();
    expect(() =>
      validateQsdmPulseAction({ op: 'reclaim', proofs: ['p1'] })
    ).not.toThrow();
    expect(() =>
      validateQsdmPulseAction({ op: 'join', pool: 'XYZ' })
    ).toThrow();
    expect(() =>
      validateQsdmPulseAction({ op: 'reclaim', proofs: [] })
    ).toThrow();
    expect(() =>
      validateQsdmPulseAction({ op: 'reclaim', proofs: ['a', 'a'] })
    ).toThrow();
    expect(() =>
      validateQsdmPulseAction({ op: 'settle' } as unknown as { op: 'leave' })
    ).toThrow();
  });

  it('lists only my escrowed rewards that the next block may reclaim', () => {
    const state = {
      reclaim_after_blocks: 100,
      tip: 199,
      pool: {
        escrowed: [
          { proof_id: 'due', owner: me, dust: 1, height: 100 },
          { proof_id: 'early', owner: me, dust: 1, height: 101 },
          { proof_id: 'other', owner: pool, dust: 1, height: 1 },
        ],
      },
    };
    expect(
      reclaimableQsdmPulseProofs(state, me).map((e) => e.proof_id)
    ).toEqual(['due']);
    expect(
      reclaimableQsdmPulseProofs({ ...state, reclaim_after_blocks: 0 }, me)
    ).toEqual([]);
  });
});

describe('PULSE receipts', () => {
  it('reads the newest receipts the miner kept and skips broken lines', () => {
    const file = path.join(
      fs.mkdtempSync(path.join(os.tmpdir(), 'pulse-')),
      'r.jsonl'
    );
    const lines = [];
    for (let i = 1; i <= 25; i += 1) {
      lines.push(
        JSON.stringify({
          receipt: { index: i, difficulty: 65536, job_id: 'j' },
        })
      );
    }
    lines.splice(3, 0, '{not json');
    fs.writeFileSync(file, `${lines.join('\n')}\n`);
    const { total, recent } = readQsdmPulseReceipts(file);
    expect(total).toBe(26);
    expect(recent.length).toBe(20);
    expect(recent[recent.length - 1].index).toBe(25);
    expect(
      readQsdmPulseReceipts(path.join(os.tmpdir(), 'missing-pulse.jsonl'))
    ).toEqual({
      total: 0,
      recent: [],
    });
  });
});
