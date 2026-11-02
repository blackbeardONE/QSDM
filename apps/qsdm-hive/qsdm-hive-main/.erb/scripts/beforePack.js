const path = require('path');
const {
  assertRuntimeDependencies,
} = require('./verify-runtime-dependencies.cjs');
exports.default = async function beforePack() {
  assertRuntimeDependencies(path.resolve(__dirname, '../../release/app'));
};
