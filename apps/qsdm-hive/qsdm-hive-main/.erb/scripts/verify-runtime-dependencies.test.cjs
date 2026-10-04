const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const {
  assertRuntimeDependencies,
  assertPackagedRuntimeDependencies,
} = require('./verify-runtime-dependencies.cjs');

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-runtime-test-'));
  const app = path.join(root, 'release', 'app');
  fs.mkdirSync(app, { recursive: true });
  fs.writeFileSync(
    path.join(app, 'package.json'),
    JSON.stringify({ dependencies: { puppeteer: '*' } })
  );
  return { root, app };
}
function dependency(folder) {
  fs.mkdirSync(folder, { recursive: true });
  fs.writeFileSync(
    path.join(folder, 'package.json'),
    JSON.stringify({ name: 'puppeteer', main: 'index.js' })
  );
  fs.writeFileSync(path.join(folder, 'index.js'), 'module.exports = {};');
}
test('ancestor-installed dependency cannot hide missing runtime dependency', () => {
  const { root, app } = fixture();
  dependency(path.join(root, 'node_modules', 'puppeteer'));
  assert.throws(
    () => assertRuntimeDependencies(app),
    /missing from release\/app/
  );
});
test('runtime dependency must contain a resolvable entry point', () => {
  const { app } = fixture();
  const folder = path.join(app, 'node_modules', 'puppeteer');
  fs.mkdirSync(folder, { recursive: true });
  fs.writeFileSync(path.join(folder, 'package.json'), '{"main":"absent.js"}');
  assert.throws(() => assertRuntimeDependencies(app), /Cannot find module/);
});
test('complete runtime dependency passes preflight', () => {
  const { app } = fixture();
  dependency(path.join(app, 'node_modules', 'puppeteer'));
  assert.deepEqual(assertRuntimeDependencies(app), ['puppeteer']);
});
test('packaged missing runtime module blocks release', () => {
  const fake = {
    extractFile() {
      throw Error('missing');
    },
  };
  assert.throws(
    () =>
      assertPackagedRuntimeDependencies('fixture.asar', ['puppeteer'], fake),
    /packaged runtime dependency is missing/
  );
});
test('packaged dependency entry must exist, not only its metadata', () => {
  const fake = {
    extractFile() {
      return Buffer.from('{"main":"lib/start.js"}');
    },
    statFile() {
      throw Error('missing entry');
    },
  };
  assert.throws(
    () =>
      assertPackagedRuntimeDependencies('fixture.asar', ['puppeteer'], fake),
    /missing entry/
  );
});
test('complete packaged runtime module passes release gate', () => {
  const inspected = [];
  const fake = {
    extractFile() {
      return Buffer.from('{"main":"lib/start.js"}');
    },
    statFile(_archive, file) {
      inspected.push(file);
      return { size: 1 };
    },
  };
  assertPackagedRuntimeDependencies('fixture.asar', ['puppeteer'], fake);
  assert.deepEqual(inspected, [
    path.join('node_modules', 'puppeteer', 'lib', 'start.js'),
  ]);
});
