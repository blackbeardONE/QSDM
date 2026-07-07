import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  disconnectQsdmMotherHiveRelay,
  getDefaultEdgeRelayTokenFile,
  getDefaultEdgeRelayURL,
  getEdgeRelayConnectionConfig,
  getEdgeRelayConnectionConfigPath,
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
    expect(fs.existsSync(result.token_file)).toBe(true);
    expect(fs.readFileSync(result.token_file, 'utf8').trim()).toBe(
      'ab'.repeat(32)
    );
    expect(getEdgeRelayConnectionConfig()).toEqual(result);
    expect(getDefaultEdgeRelayURL()).toBe(result.relay_url);
    expect(getDefaultEdgeRelayTokenFile()).toBe(result.token_file);
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
