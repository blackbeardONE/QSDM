const fs = require('fs');
const path = require('path');

exports.default = async function afterPack(context) {
  if (context.electronPlatformName !== 'linux') return;

  const executables = [
    path.join(context.appOutDir, 'resources', 'edge', 'qsdm-edge-agent'),
    path.join(context.appOutDir, 'resources', 'edge', 'qsdm-edge-control'),
    path.join(context.appOutDir, 'resources', 'edge', 'qsdm-edge-gpu-helper'),
    path.join(context.appOutDir, 'resources', 'native', 'qsdmcli'),
    path.join(context.appOutDir, 'resources', 'miner', 'qsdmminer-console'),
  ];

  for (const executable of executables) {
    if (!fs.existsSync(executable)) {
      throw new Error(`Required Linux executable was not packaged: ${executable}`);
    }
    fs.chmodSync(executable, 0o755);
  }
};
