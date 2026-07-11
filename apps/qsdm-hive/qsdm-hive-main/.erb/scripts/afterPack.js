const { execFileSync } = require('child_process');
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

  const appVersion = String(context.packager.appInfo.version).trim();
  const edgeVersionPath = path.resolve(
    __dirname,
    '../../../..',
    'qsdm-edge-agent',
    'VERSION'
  );
  const edgeVersion = fs.readFileSync(edgeVersionPath, 'utf8').trim();
  const versionProbes = [
    {
      executable: executables[0],
      expectedPrefix: `qsdm-edge-agent ${edgeVersion} (`,
    },
    {
      executable: executables[1],
      expectedPrefix: `qsdm-edge-control ${edgeVersion} (`,
    },
    {
      executable: executables[4],
      expectedPrefix: `qsdmminer-console hive-v${appVersion} (`,
    },
  ];

  for (const probe of versionProbes) {
    let output;
    try {
      output = execFileSync(probe.executable, ['--version'], {
        encoding: 'utf8',
        timeout: 15000,
        windowsHide: true,
      }).trim();
    } catch (error) {
      throw new Error(
        `Could not verify packaged executable version: ${probe.executable}: ${error.message}`
      );
    }

    if (!output.startsWith(probe.expectedPrefix)) {
      throw new Error(
        `Packaged executable version mismatch: ${probe.executable}; expected prefix "${probe.expectedPrefix}", got "${output}"`
      );
    }
  }
};
