const fs = require('fs');
const path = require('path');

// cspell:ignore qsdmminer

exports.default = async function afterPack(context) {
  const platform = context.electronPlatformName;
  if (platform !== 'linux' && platform !== 'win32') return;

  const extension = platform === 'win32' ? '.exe' : '';

  const executables = [
    path.join(
      context.appOutDir,
      'resources',
      'edge',
      `qsdm-edge-agent${extension}`
    ),
    path.join(
      context.appOutDir,
      'resources',
      'edge',
      `qsdm-edge-control${extension}`
    ),
    path.join(
      context.appOutDir,
      'resources',
      'edge',
      `qsdm-edge-gpu-helper${extension}`
    ),
    path.join(context.appOutDir, 'resources', 'native', `qsdmcli${extension}`),
    path.join(
      context.appOutDir,
      'resources',
      'miner',
      `qsdmminer-console${extension}`
    ),
    path.join(
      context.appOutDir,
      'resources',
      'miner',
      `qsdm-miner-cuda-solver${extension}`
    ),
  ];

  for (const executable of executables) {
    if (!fs.existsSync(executable)) {
      throw new Error(
        `Required ${platform} executable was not packaged: ${executable}`
      );
    }
    if (platform === 'linux') {
      fs.chmodSync(executable, 0o755);
    }
  }
};
