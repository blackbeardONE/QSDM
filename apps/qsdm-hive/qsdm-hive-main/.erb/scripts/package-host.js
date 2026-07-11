const { spawnSync } = require('child_process');
const path = require('path');

const hiveRoot = path.resolve(__dirname, '../..');
const npmCli = process.env.npm_execpath;

function run(scriptName) {
  if (!npmCli) {
    throw new Error('package-host must be started through npm run package.');
  }

  const result = spawnSync(process.execPath, [npmCli, 'run', scriptName], {
    cwd: hiveRoot,
    env: process.env,
    stdio: 'inherit',
    windowsHide: true,
  });

  if (result.error) throw result.error;
  process.exit(result.status ?? 1);
}

if (process.platform === 'win32') {
  run('package:windows');
} else if (process.platform === 'linux') {
  run('package:linux');
} else {
  console.error(
    `QSDM Hive release packaging is supported only on Windows and Linux; got ${process.platform}.`
  );
  process.exit(1);
}
