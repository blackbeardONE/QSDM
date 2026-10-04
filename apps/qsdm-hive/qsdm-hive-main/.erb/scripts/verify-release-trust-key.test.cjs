const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const {
  RELEASE_TRUST_KEY_PATH,
  assertReleaseTrustKeyFile,
  describeReleaseTrustKey,
} = require('./verify-release-trust-key.cjs');

// Synthetic public key bytes: format checks only, never a real signing key.
function syntheticKey() {
  const publicKey = crypto
    .createHash('sha512')
    .update('qsdm-test-only')
    .digest()
    .toString('hex')
    .repeat(41)
    .slice(0, 5184);
  const keyId = crypto
    .createHash('sha256')
    .update(Buffer.from(publicKey, 'hex'))
    .digest('hex');
  return {
    schema: 'qsdm.release-trust-key.v1',
    key_id: keyId,
    algorithm: 'ML-DSA-87',
    public_key: publicKey,
    address: keyId,
    created_at: '2026-10-04T00:00:00.000Z',
    custody: 'test',
  };
}

test('pins the v2 trust root file', () => {
  assert.match(
    RELEASE_TRUST_KEY_PATH.replace(/\\/g, '/'),
    /QSDM\/deploy\/release-trust\/qsdm-hive-release-key-v2\.json$/
  );
  assert.ok(fs.existsSync(RELEASE_TRUST_KEY_PATH));
});

test('accepts release-signing-public.json shaped metadata', () => {
  const key = syntheticKey();
  assert.equal(describeReleaseTrustKey(key).keyId, key.key_id);
});

test('refuses the placeholder', () => {
  assert.throws(
    () =>
      describeReleaseTrustKey({
        schema: 'qsdm.release-trust-key.v1',
        placeholder: true,
      }),
    /placeholder/
  );
});

test('refuses a key_id that is not the SHA-256 of the public key', () => {
  const key = syntheticKey();
  key.key_id = 'a'.repeat(64);
  key.address = key.key_id;
  assert.throws(() => describeReleaseTrustKey(key), /SHA-256/);
});

test('refuses a short public key', () => {
  const key = syntheticKey();
  key.public_key = key.public_key.slice(0, 64);
  assert.throws(() => describeReleaseTrustKey(key), /malformed/);
});

test('honours QSDM_EXPECTED_RELEASE_KEY_ID', () => {
  const key = syntheticKey();
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-trust-test-'));
  const file = path.join(dir, 'key.json');
  fs.writeFileSync(file, JSON.stringify(key));
  const previous = process.env.QSDM_EXPECTED_RELEASE_KEY_ID;
  try {
    process.env.QSDM_EXPECTED_RELEASE_KEY_ID = key.key_id;
    assert.equal(assertReleaseTrustKeyFile(file).keyId, key.key_id);
    process.env.QSDM_EXPECTED_RELEASE_KEY_ID = 'b'.repeat(64);
    assert.throws(() => assertReleaseTrustKeyFile(file), /differs/);
  } finally {
    if (previous === undefined) delete process.env.QSDM_EXPECTED_RELEASE_KEY_ID;
    else process.env.QSDM_EXPECTED_RELEASE_KEY_ID = previous;
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
