const { spawn } = require('child_process');

require('dotenv').config();

const electronmonCli = require.resolve('electronmon/bin/cli');

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE;
env.NODE_ENV = env.NODE_ENV || 'development';
env.TS_NODE_TRANSPILE_ONLY = 'true';

const child = spawn(
  process.execPath,
  [
    electronmonCli,
    '-r',
    'ts-node/register/transpile-only',
    '-r',
    'tsconfig-paths/register',
    '.',
  ],
  {
    env,
    stdio: 'inherit',
  }
);

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  process.exit(code ?? 0);
});

child.on('error', (error) => {
  console.error(error);
  process.exit(1);
});
