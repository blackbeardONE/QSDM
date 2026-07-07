const { spawn } = require('child_process');

require('dotenv').config();

const electronmonCli = require.resolve('electronmon/bin/cli');
const electronPath = require('electron');

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE;
env.NODE_ENV = env.NODE_ENV || 'development';
env.TS_NODE_TRANSPILE_ONLY = 'true';

const electronArgs = [
  '-r',
  'ts-node/register/transpile-only',
  '-r',
  'tsconfig-paths/register',
  '.',
];

let fallbackStarted = false;

const startDirectElectron = () => {
  fallbackStarted = true;
  console.warn(
    'electronmon failed; starting Electron directly for this Hive session.'
  );

  const directChild = spawn(electronPath, electronArgs, {
    env,
    stdio: 'inherit',
  });

  directChild.on('exit', (code, signal) => {
    if (signal) {
      process.kill(process.pid, signal);
      return;
    }

    process.exit(code ?? 0);
  });

  directChild.on('error', (error) => {
    console.error(error);
    process.exit(1);
  });
};

const child = spawn(process.execPath, [electronmonCli, ...electronArgs], {
  env,
  stdio: 'inherit',
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  if (code && !fallbackStarted) {
    startDirectElectron();
    return;
  }

  process.exit(code ?? 0);
});

child.on('error', (error) => {
  console.error(error);
  process.exit(1);
});
