// Build-time guard for the Hive release trust root (public key only).
// Hive 1.4.21+ pins the v2 release-signing key. The repository ships a
// placeholder until the operator drops in release-signing-public.json; a
// production bundle must never be built from the placeholder.
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const RELEASE_TRUST_KEY_PATH = path.resolve(
  __dirname,
  '../../../../..',
  'QSDM',
  'deploy',
  'release-trust',
  'qsdm-hive-release-key-v2.json'
);

function describeReleaseTrustKey(value) {
  const key = value || {};
  if (key.placeholder === true) {
    throw new Error(
      'The Hive release trust root is still the placeholder. Copy release-signing-public.json from the new signing directory over QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json first.'
    );
  }
  if (
    key.schema !== 'qsdm.release-trust-key.v1' ||
    key.algorithm !== 'ML-DSA-87' ||
    typeof key.key_id !== 'string' ||
    !/^[0-9a-f]{64}$/.test(key.key_id) ||
    key.address !== key.key_id ||
    typeof key.public_key !== 'string' ||
    !/^[0-9a-f]{5184}$/.test(key.public_key) ||
    typeof key.created_at !== 'string' ||
    !Number.isFinite(Date.parse(key.created_at))
  ) {
    throw new Error('The Hive release trust root is malformed.');
  }
  const derived = crypto
    .createHash('sha256')
    .update(Buffer.from(key.public_key, 'hex'))
    .digest('hex');
  if (derived !== key.key_id) {
    throw new Error(
      'The Hive release trust root key_id is not the SHA-256 of its public key.'
    );
  }
  return { keyId: key.key_id, createdAt: key.created_at };
}

function assertReleaseTrustKeyFile(filePath = RELEASE_TRUST_KEY_PATH) {
  const parsed = JSON.parse(fs.readFileSync(filePath, 'utf8'));
  const result = describeReleaseTrustKey(parsed);
  const expected = (process.env.QSDM_EXPECTED_RELEASE_KEY_ID || '').trim();
  if (expected && expected !== result.keyId) {
    throw new Error(
      `The Hive release trust root key_id ${result.keyId} differs from QSDM_EXPECTED_RELEASE_KEY_ID ${expected}.`
    );
  }
  return { ...result, path: filePath };
}

module.exports = {
  RELEASE_TRUST_KEY_PATH,
  assertReleaseTrustKeyFile,
  describeReleaseTrustKey,
};

if (require.main === module) {
  try {
    const result = assertReleaseTrustKeyFile(process.argv[2]);
    console.log(`QSDM_RELEASE_TRUST_KEY_OK ${result.keyId} ${result.path}`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exit(1);
  }
}
