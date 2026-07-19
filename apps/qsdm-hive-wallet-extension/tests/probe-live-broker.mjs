import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const PROVIDER_VERSION = 'qsdm-hive-wallet-provider/v1';
const testDirectory = path.dirname(fileURLToPath(import.meta.url));
const extensionDirectory = path.dirname(testDirectory);
const repositoryDirectory = path.resolve(extensionDirectory, '..', '..');
const platformDirectory = process.platform === 'win32' ? 'windows' : 'linux';
const executableName =
  process.platform === 'win32'
    ? 'qsdm-hive-wallet-host.exe'
    : 'qsdm-hive-wallet-host';
const nativeHostPath = path.join(
  repositoryDirectory,
  'apps',
  'qsdm-hive',
  'qsdm-hive-main',
  'native',
  platformDirectory,
  'x64',
  executableName
);

if (!fs.existsSync(nativeHostPath)) {
  throw new Error(`Native host was not found at ${nativeHostPath}`);
}

const frame = (request) => {
  const payload = Buffer.from(JSON.stringify(request), 'utf8');
  const length = Buffer.alloc(4);
  length.writeUInt32LE(payload.length);
  return Buffer.concat([length, payload]);
};

const requests = [
  {
    version: PROVIDER_VERSION,
    id: 'live-ping',
    origin: 'qsdm-extension://wallet-popup',
    method: 'qsdm_ping',
  },
  {
    version: PROVIDER_VERSION,
    id: 'live-wallet-info',
    origin: 'qsdm-extension://wallet-popup',
    method: 'qsdm_getWalletInfo',
  },
];

const host = spawn(nativeHostPath, [], { windowsHide: true });
const stdout = [];
const stderr = [];
host.stdout.on('data', (chunk) => stdout.push(chunk));
host.stderr.on('data', (chunk) => stderr.push(chunk));
host.stdin.end(Buffer.concat(requests.map(frame)));

const exitCode = await new Promise((resolve, reject) => {
  const timeout = setTimeout(() => {
    host.kill();
    reject(new Error('Live QSDM Hive broker probe timed out'));
  }, 10000);
  host.once('error', (error) => {
    clearTimeout(timeout);
    reject(error);
  });
  host.once('close', (code) => {
    clearTimeout(timeout);
    resolve(code);
  });
});

if (exitCode !== 0) {
  throw new Error(
    Buffer.concat(stderr).toString('utf8') ||
      `Native host exited with code ${exitCode}`
  );
}

const output = Buffer.concat(stdout);
const responses = [];
let offset = 0;
while (offset < output.length) {
  assert.ok(output.length - offset >= 4, 'Incomplete native response header');
  const length = output.readUInt32LE(offset);
  offset += 4;
  assert.ok(output.length - offset >= length, 'Incomplete native response body');
  responses.push(
    JSON.parse(output.subarray(offset, offset + length).toString('utf8'))
  );
  offset += length;
}

assert.equal(responses.length, requests.length);
assert.equal(responses[0].id, 'live-ping');
assert.equal(responses[0].ok, true);
assert.equal(responses[0].result?.hive, true);
assert.equal(responses[0].result?.version, PROVIDER_VERSION);
assert.equal(responses[1].id, 'live-wallet-info');
assert.equal(responses[1].ok, true);
assert.equal(typeof responses[1].result?.ready, 'boolean');
assert.ok(
  responses[1].result?.address === null ||
    typeof responses[1].result?.address === 'string'
);

const address = responses[1].result.address;
const maskedAddress = address
  ? `${address.slice(0, 8)}...${address.slice(-8)}`
  : null;

console.log(
  JSON.stringify(
    {
      ok: true,
      providerVersion: responses[0].result.version,
      signerReady: responses[0].result.signerReady,
      wallet: {
        address: maskedAddress,
        ready: responses[1].result.ready,
        connectedSites: responses[1].result.connectedSites,
      },
      signed: false,
      transferred: false,
    },
    null,
    2
  )
);
