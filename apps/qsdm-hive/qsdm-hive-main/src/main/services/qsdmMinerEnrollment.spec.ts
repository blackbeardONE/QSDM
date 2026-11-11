import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  QSDM_MINER_UNBOND_WINDOW_BLOCKS,
  buildQsdmMinerNodeId,
  decideQsdmGpuEnrollmentReuse,
  describeQsdmMinerUnbondWindow,
  findQsdmActiveEnrollmentForGpu,
  inferQsdmNvidiaArchitecture,
  parseQsdmNvidiaSmiCsv,
  readQsdmEnrollmentHmacKeyHex,
  resolveQsdmMinerEnrollmentSubmitApiUrl,
  writeQsdmAdoptedMinerIdentity,
} from './qsdmMinerEnrollment';

type EnrollmentPage = Awaited<
  ReturnType<Parameters<typeof findQsdmActiveEnrollmentForGpu>[1]>
>;

const OWNER = 'c'.repeat(64);

const enrollment = (
  nodeId: string,
  owner: string,
  gpuUuid: string,
  phase: 'active' | 'pending_unbond' | 'revoked' = 'active'
) => ({
  node_id: nodeId,
  owner,
  gpu_uuid: gpuUuid,
  stake_dust: 1_000_000_000,
  phase,
  slashable: true,
  enrolled_at_height: 196_873,
});

const txWithPayload = (payload: object | string) => ({
  Payload: Buffer.from(
    typeof payload === 'string' ? payload : JSON.stringify(payload)
  ).toString('base64'),
});

describe('reusing the enrollment that already holds the GPU', () => {
  it('finds the active enrollment of the GPU across pages', async () => {
    const pages: Record<string, EnrollmentPage> = {
      '': {
        records: [
          enrollment('hive-a', OWNER, 'GPU-ABC', 'pending_unbond'),
          enrollment('hive-b', OWNER, 'GPU-other'),
        ],
        has_more: true,
        next_cursor: 'hive-b',
      },
      'hive-b': {
        records: [
          null,
          enrollment('hive-old-pc-0123456789ab', OWNER, 'GPU-ABC'),
        ],
        has_more: false,
      },
    };
    const cursors: string[] = [];
    const found = await findQsdmActiveEnrollmentForGpu(
      'gpu-abc',
      async (cursor) => {
        cursors.push(cursor);
        return pages[cursor];
      }
    );

    expect(found?.node_id).toBe('hive-old-pc-0123456789ab');
    expect(cursors).toEqual(['', 'hive-b']);
  });

  it('stops when the GPU is not enrolled or the cursor does not move', async () => {
    await expect(
      findQsdmActiveEnrollmentForGpu('GPU-x', async () => ({
        records: [],
        has_more: false,
      }))
    ).resolves.toBeUndefined();

    const cursors: string[] = [];
    await expect(
      findQsdmActiveEnrollmentForGpu('GPU-x', async (cursor) => {
        cursors.push(cursor);
        return { records: [], has_more: true, next_cursor: 'same' };
      })
    ).resolves.toBeUndefined();
    expect(cursors).toEqual(['', 'same']);
  });

  it('reuses an enrollment of the same wallet and names the other wallet', () => {
    const bound = enrollment('hive-old-pc-0123456789ab', OWNER, 'GPU-ABC');

    expect(
      decideQsdmGpuEnrollmentReuse(OWNER.toUpperCase(), 'hive-new-pc-01', bound)
    ).toEqual({ action: 'adopt', record: bound });
    expect(
      decideQsdmGpuEnrollmentReuse(OWNER, 'hive-new-pc-01', undefined)
    ).toEqual({ action: 'enroll' });

    const other = decideQsdmGpuEnrollmentReuse(
      'd'.repeat(64),
      'hive-new-pc-01',
      bound
    );
    expect(other.action).toBe('other-wallet');
    expect(other.action === 'other-wallet' ? other.message : '').toContain(
      `as hive-old-pc-0123456789ab by wallet ${OWNER}`
    );
  });

  it('reads the HMAC key from the enroll transaction only', () => {
    const key = Buffer.alloc(32, 7);
    const bound = enrollment('hive-old-pc-0123456789ab', OWNER, 'GPU-ABC');
    const page = {
      blocks: [
        {
          transactions: [
            { Payload: null },
            txWithPayload('not json'),
            txWithPayload({ kind: 'unenroll', node_id: bound.node_id }),
            txWithPayload({
              kind: 'enroll',
              node_id: 'hive-other-pc-0123456789ab',
              gpu_uuid: 'GPU-ZZZ',
              hmac_key: Buffer.alloc(32, 1).toString('base64'),
            }),
            txWithPayload({
              kind: 'enroll',
              node_id: bound.node_id,
              gpu_uuid: 'gpu-abc',
              hmac_key: key.toString('base64'),
            }),
          ],
        },
      ],
    };

    expect(readQsdmEnrollmentHmacKeyHex(bound, page)).toBe(key.toString('hex'));
    expect(
      readQsdmEnrollmentHmacKeyHex({ ...bound, node_id: 'hive-missing' }, page)
    ).toBeUndefined();
    expect(
      readQsdmEnrollmentHmacKeyHex(bound, {
        blocks: [
          {
            transactions: [
              txWithPayload({
                kind: 'enroll',
                node_id: bound.node_id,
                gpu_uuid: 'GPU-ABC',
                hmac_key: Buffer.alloc(16, 7).toString('base64'),
              }),
            ],
          },
        ],
      })
    ).toBeUndefined();
    expect(readQsdmEnrollmentHmacKeyHex(bound, {})).toBeUndefined();
  });

  it('points the miner settings at the reused enrollment and keeps copies', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-adopt-'));
    try {
      const configPath = path.join(dir, 'miner.toml');
      const hmacKeyPath = path.join(dir, 'miner-hmac.key');
      const oldKey = `${'ab'.repeat(32)}\n`;
      fs.writeFileSync(
        configPath,
        'protocol = "v2"\nnode_id = "hive-new-pc-01"\ngpu_uuid = "GPU-ABC"\n'
      );
      fs.writeFileSync(hmacKeyPath, oldKey);
      const identity = {
        uuid: 'GPU-ABC',
        name: 'NVIDIA GeForce RTX 3050',
        computeCapability: '8.6',
        driverVersion: '572.16',
        cudaVersion: '12.8',
        architecture: 'ampere',
        nodeId: 'hive-new-pc-01',
        hmacKeyPath,
        configPath,
      };
      const keyHex = '07'.repeat(32);

      const adopted = writeQsdmAdoptedMinerIdentity(
        identity,
        'hive-old-pc-0123456789ab',
        keyHex,
        1
      );

      expect(adopted.nodeId).toBe('hive-old-pc-0123456789ab');
      expect(fs.readFileSync(configPath, 'utf8')).toBe(
        'protocol = "v2"\nnode_id = "hive-old-pc-0123456789ab"\ngpu_uuid = "GPU-ABC"\n'
      );
      expect(fs.readFileSync(hmacKeyPath, 'utf8')).toBe(`${keyHex}\n`);
      expect(fs.readFileSync(`${configPath}.bak-1`, 'utf8')).toContain(
        'node_id = "hive-new-pc-01"'
      );
      expect(fs.readFileSync(`${hmacKeyPath}.bak-1`, 'utf8')).toBe(oldKey);

      writeQsdmAdoptedMinerIdentity(adopted, adopted.nodeId, keyHex, 2);
      expect(fs.existsSync(`${configPath}.bak-2`)).toBe(false);
      expect(fs.existsSync(`${hmacKeyPath}.bak-2`)).toBe(false);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe('qsdmMinerEnrollment', () => {
  it('describes the chain unbond window in blocks and approximate days', () => {
    expect(QSDM_MINER_UNBOND_WINDOW_BLOCKS).toBe(201_600);
    expect(describeQsdmMinerUnbondWindow()).toBe(
      '201,600 blocks (about 23 days)'
    );
    expect(describeQsdmMinerUnbondWindow(8_640, 10)).toBe(
      '8,640 blocks (about 1 day)'
    );
  });

  it.each([
    ['7.5', 'turing'],
    ['8.6', 'ampere'],
    ['8.9', 'ada-lovelace'],
    ['9.0', 'hopper'],
    ['10.0', 'blackwell'],
  ])('maps compute capability %s to %s', (computeCapability, expected) => {
    expect(inferQsdmNvidiaArchitecture(computeCapability)).toBe(expected);
  });

  it('parses the first NVIDIA GPU without inventing identity fields', () => {
    const gpu = parseQsdmNvidiaSmiCsv(
      'GPU-abc, NVIDIA GeForce RTX 3050, 8.6, 572.16\nGPU-def, NVIDIA RTX 4090, 8.9, 572.16',
      '12.8'
    );

    expect(gpu).toEqual({
      uuid: 'GPU-abc',
      name: 'NVIDIA GeForce RTX 3050',
      computeCapability: '8.6',
      driverVersion: '572.16',
      cudaVersion: '12.8',
      architecture: 'ampere',
    });
  });

  it('rejects pre-Turing GPUs', () => {
    expect(() =>
      parseQsdmNvidiaSmiCsv('GPU-old, NVIDIA GTX 1080, 6.1, 535.1')
    ).toThrow(/Turing or newer/);
  });

  it('binds generated NodeIDs to the active signer wallet', () => {
    const first = buildQsdmMinerNodeId(
      'DESKTOP-QSDM',
      'GPU-abc',
      'a'.repeat(64)
    );
    const second = buildQsdmMinerNodeId(
      'DESKTOP-QSDM',
      'GPU-abc',
      'b'.repeat(64)
    );

    expect(first).toMatch(/^hive-desktop-qsdm-[0-9a-f]{12}$/);
    expect(second).not.toBe(first);
    expect(first.length).toBeLessThanOrEqual(64);
  });

  it('submits official-gateway enrollments to canonical Core', () => {
    expect(
      resolveQsdmMinerEnrollmentSubmitApiUrl({
        runtimeApiUrl: 'https://api.qsdm.tech/attest/home-validator/api/v1',
        gatewayApiUrl: 'https://api.qsdm.tech/attest/home-validator/api/v1',
        canonicalApiUrl: 'https://api.qsdm.tech/api/v1/',
      })
    ).toBe('https://api.qsdm.tech/api/v1');
  });

  it.each([
    'http://127.0.0.1:8080/api/v1/',
    'https://validator.example/api/v1/',
  ])('keeps non-gateway enrollment on %s', (runtimeApiUrl) => {
    expect(
      resolveQsdmMinerEnrollmentSubmitApiUrl({
        runtimeApiUrl,
        gatewayApiUrl: 'https://api.qsdm.tech/attest/home-validator/api/v1',
        canonicalApiUrl: 'https://api.qsdm.tech/api/v1',
      })
    ).toBe(runtimeApiUrl.replace(/\/+$/, ''));
  });
});
