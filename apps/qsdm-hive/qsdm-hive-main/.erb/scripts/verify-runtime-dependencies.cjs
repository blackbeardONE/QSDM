const fs = require('fs');
const path = require('path');

function runtimeDependencies(appDir) {
  return Object.keys(
    JSON.parse(fs.readFileSync(path.join(appDir, 'package.json'), 'utf8'))
      .dependencies || {}
  );
}

function assertRuntimeDependencies(appDir) {
  const modules = path.join(appDir, 'node_modules');
  const required = runtimeDependencies(appDir);
  for (const name of required) {
    const folder = path.join(modules, name);
    const manifest = path.join(folder, 'package.json');
    if (!fs.existsSync(manifest)) {
      throw new Error(
        'Hive runtime dependency is missing from release/app/node_modules: ' +
          name +
          '. Run npm ci --omit=dev --ignore-scripts in release/app before packaging.'
      );
    }
    const resolved = require.resolve(folder);
    const realModules = fs.realpathSync(modules) + path.sep;
    if (!fs.realpathSync(resolved).startsWith(realModules)) {
      throw new Error(
        'Hive runtime dependency resolves outside release/app/node_modules: ' +
          name
      );
    }
  }
  return required;
}

function assertPackagedRuntimeDependencies(archive, required, archiveApi) {
  const asar = archiveApi || require('@electron/asar');
  for (const name of required) {
    const base = path.join('node_modules', name);
    let metadata;
    try {
      metadata = JSON.parse(
        asar
          .extractFile(archive, path.join(base, 'package.json'))
          .toString('utf8')
      );
      const entry = path.join(base, metadata.main || 'index.js');
      asar.statFile(archive, entry);
    } catch (error) {
      throw new Error(
        'Hive packaged runtime dependency is missing or incomplete: ' +
          name +
          ': ' +
          error.message
      );
    }
  }
}

module.exports = {
  runtimeDependencies,
  assertRuntimeDependencies,
  assertPackagedRuntimeDependencies,
};
if (require.main === module) {
  const required = assertRuntimeDependencies(
    path.resolve(__dirname, '../../release/app')
  );
  console.log('Verified Hive runtime dependencies: ' + required.join(', '));
}
