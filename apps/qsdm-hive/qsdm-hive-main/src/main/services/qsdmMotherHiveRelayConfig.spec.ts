import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  disconnectQsdmMotherHiveRelay,
  getDefaultEdgeRelayTokenFile,
  getDefaultEdgeRelayURL,
  getEdgeRelayConnectionConfig,
  getEdgeRelayConnectionConfigPath,
  getEdgeRelayMotherContext,
  pairQsdmMotherHiveRelay,
} from './qsdmMotherHiveRelayConfig';

const pairingCode = (overrides: Record<string, unknown> = {}) => {
  const payload = {
    version: 1,
    kind: 'mother',
    relay_url: 'http://192.168.50.10:7740',
    token: 'ab'.repeat(32),
    ...overrides,
  };
  return `QSDM-EDGE-1.${Buffer.from(JSON.stringify(payload)).toString(
    'base64url'
  )}`;
};

const federationCode = (overrides: Record<string, unknown> = {}) => {
  const payload = {
    version: 2,
    kind: 'mother-federation',
    relay_url: 'https://node.qsdm.tech',
    token: 'cd'.repeat(32),
    offer_id: 'offer-home-lab',
    provider_name: 'Home Lab',
    provider_wallet: 'a'.repeat(64),
    consumer_wallet: 'b'.repeat(64),
    expires_at: new Date(Date.now() + 3600_000).toISOString(),
    workload_ids: ['qsdm.cpu.hash-chain.v1', 'qsdm.ram.memory-scan.v1'],
    ...overrides,
  };
  const context = {
    version: 1,
    relay_url: payload.relay_url,
    offer_id: payload.offer_id,
    provider_name: payload.provider_name,
    provider_wallet: payload.provider_wallet,
    consumer_wallet: payload.consumer_wallet,
    expires_at: payload.expires_at,
    workload_ids: payload.workload_ids,
  };
  return `QSDM-EDGE-2.${Buffer.from(
    JSON.stringify({
      ...payload,
      federation_context: Buffer.from(JSON.stringify(context)).toString(
        'base64url'
      ),
    })
  ).toString('base64url')}`;
};

const localMotherCode = (overrides: Record<string, unknown> = {}) => {
  const context = {
    version: 1,
    mother_id: 'mother-' + 'a'.repeat(24),
    mother_name: 'Office Mother Hive',
    issued_at: new Date(
      Math.floor((Date.now() - 1000) / 1000) * 1000
    ).toISOString().replace('.000Z', 'Z'),
  };
  const payload = {
    version: 3,
    kind: 'mother-local',
    relay_url: 'http://192.168.50.10:7740',
    token: 'de'.repeat(32),
    mother_id: context.mother_id,
    mother_name: context.mother_name,
    mother_context: Buffer.from(JSON.stringify(context)).toString('base64url'),
    ...overrides,
  };
  return `QSDM-EDGE-3.${Buffer.from(JSON.stringify(payload)).toString(
    'base64url'
  )}`;
};

describe('Mother Hive Relay configuration', () => {
  const originalAppData = process.env.APPDATA;
  const originalXdgConfigHome = process.env.XDG_CONFIG_HOME;
  let configRoot = '';

  beforeEach(() => {
    configRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-hive-relay-'));
    process.env.APPDATA = configRoot;
    process.env.XDG_CONFIG_HOME = configRoot;
    delete process.env.QSDM_EDGE_RELAY_URL;
    delete process.env.QSDM_EDGE_POOL_URL;
    delete process.env.QSDM_EDGE_RELAY_TOKEN_FILE;
    delete process.env.QSDM_EDGE_POOL_TOKEN_FILE;
  });

  afterEach(() => {
    fs.rmSync(configRoot, { recursive: true, force: true });
  });

  afterAll(() => {
    if (originalAppData === undefined) delete process.env.APPDATA;
    else process.env.APPDATA = originalAppData;
    if (originalXdgConfigHome === undefined) delete process.env.XDG_CONFIG_HOME;
    else process.env.XDG_CONFIG_HOME = originalXdgConfigHome;
  });

  it('imports a Mother Hive pairing code into Hive-owned private files', () => {
    const result = pairQsdmMotherHiveRelay(pairingCode());

    expect(result.relay_url).toBe('http://192.168.50.10:7740/');
    expect(result.connection_mode).toBe('private-lan');
    expect(fs.existsSync(result.token_file)).toBe(true);
    expect(fs.readFileSync(result.token_file, 'utf8').trim()).toBe(
      'ab'.repeat(32)
    );
    expect(getEdgeRelayConnectionConfig()).toEqual(result);
    expect(getDefaultEdgeRelayURL()).toBe(result.relay_url);
    expect(getDefaultEdgeRelayTokenFile()).toBe(result.token_file);
  });

  it('imports an HTTPS internet federation invitation with provider metadata', () => {
    const result = pairQsdmMotherHiveRelay(federationCode());

    expect(result.relay_url).toBe('https://node.qsdm.tech/');
    expect(result.connection_mode).toBe('internet-federation');
    expect(result.offer_id).toBe('offer-home-lab');
    expect(result.provider_name).toBe('Home Lab');
    expect(result.workload_ids).toEqual([
      'qsdm.cpu.hash-chain.v1',
      'qsdm.ram.memory-scan.v1',
    ]);
    expect(path.basename(result.token_file)).toMatch(/^hive-federation-/);
    expect(getEdgeRelayConnectionConfig()).toEqual(result);
  });

  it('imports a private per-Hive Relay identity without storing the Relay master key', () => {
    const result = pairQsdmMotherHiveRelay(localMotherCode());

    expect(result.connection_mode).toBe('private-multi-hive');
    expect(result.mother_id).toBe('mother-' + 'a'.repeat(24));
    expect(result.mother_name).toBe('Office Mother Hive');
    expect(getEdgeRelayMotherContext()).toBe(result.mother_context);
    expect(fs.readFileSync(result.token_file, 'utf8').trim()).toBe(
      'de'.repeat(32)
    );
    expect(path.basename(result.token_file)).toMatch(/^hive-mother-/);
  });

  it('rejects a local Mother Hive payload under a legacy credential prefix', () => {
    expect(() =>
      pairQsdmMotherHiveRelay(
        localMotherCode().replace('QSDM-EDGE-3.', 'QSDM-EDGE-1.')
      )
    ).toThrow('wrong credential format');
  });

  it('rejects federation invitations without HTTPS or a valid expiry', () => {
    expect(() =>
      pairQsdmMotherHiveRelay(
        federationCode({ relay_url: 'http://node.qsdm.tech' })
      )
    ).toThrow('HTTPS Relay address');
    expect(() =>
      pairQsdmMotherHiveRelay(federationCode({ expires_at: undefined }))
    ).toThrow('no expiry');
    expect(() =>
      pairQsdmMotherHiveRelay(
        federationCode({
          expires_at: new Date(Date.now() - 1000).toISOString(),
        })
      )
    ).toThrow('expired');
    expect(() =>
      pairQsdmMotherHiveRelay(
        federationCode({
          expires_at: new Date(Date.now() + 26 * 3600_000).toISOString(),
        })
      )
    ).toThrow('maximum lifetime');
    expect(() =>
      pairQsdmMotherHiveRelay(federationCode({ workload_ids: [] }))
    ).toThrow('context is invalid');
  });

  it('rejects legacy federation invitations that expose a permanent token', () => {
    expect(() =>
      pairQsdmMotherHiveRelay(
        pairingCode({
          kind: 'mother-federation',
          expires_at: new Date(Date.now() + 3600_000).toISOString(),
        })
      )
    ).toThrow('permanent legacy credential');
  });

  it('never falls back to another token when a saved connection is invalid or expired', () => {
    const configDirectory = path.dirname(getEdgeRelayConnectionConfigPath());
    fs.mkdirSync(configDirectory, { recursive: true });
    fs.writeFileSync(
      path.join(configDirectory, 'mother-hive.token'),
      `${'ef'.repeat(32)}\n`
    );
    fs.writeFileSync(getEdgeRelayConnectionConfigPath(), '{"broken":true}\n');
    expect(getDefaultEdgeRelayTokenFile()).toBe('');

    const result = pairQsdmMotherHiveRelay(
      federationCode({
        expires_at: new Date(Date.now() + 60_000).toISOString(),
      })
    );
    const config = JSON.parse(
      fs.readFileSync(getEdgeRelayConnectionConfigPath(), 'utf8')
    ) as Record<string, unknown>;
    config.expires_at = new Date(Date.now() - 1000).toISOString();
    fs.writeFileSync(
      getEdgeRelayConnectionConfigPath(),
      `${JSON.stringify(config)}\n`
    );
    expect(fs.existsSync(result.token_file)).toBe(true);
    expect(getDefaultEdgeRelayTokenFile()).toBe('');
  });

  it('rejects Agent codes and unexpected pairing data', () => {
    expect(() =>
      pairQsdmMotherHiveRelay(pairingCode({ kind: 'agent' }))
    ).toThrow('not a Mother Hive code');
    expect(() =>
      pairQsdmMotherHiveRelay(pairingCode({ injected: true }))
    ).toThrow('unexpected data');
  });

  it('disconnects Hive without deleting Relay-owned keys', () => {
    const result = pairQsdmMotherHiveRelay(pairingCode());
    const relayOwnedToken = path.join(
      path.dirname(result.token_file),
      'mother-hive.token'
    );
    fs.writeFileSync(relayOwnedToken, `${'cd'.repeat(32)}\n`);

    expect(disconnectQsdmMotherHiveRelay()).toBe(true);
    expect(fs.existsSync(getEdgeRelayConnectionConfigPath())).toBe(false);
    expect(fs.existsSync(result.token_file)).toBe(false);
    expect(fs.existsSync(relayOwnedToken)).toBe(true);
    expect(getDefaultEdgeRelayTokenFile()).toBe('');
  });
});
