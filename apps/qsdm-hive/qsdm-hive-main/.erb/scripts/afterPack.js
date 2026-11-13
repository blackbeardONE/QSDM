const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const {
  runtimeDependencies,
  assertPackagedRuntimeDependencies,
} = require('./verify-runtime-dependencies.cjs');

// cspell:ignore qsdmminer asar

// macOS: runs for the x64 and the arm64 app and again for the universal app
// merged from them. The runtime modules are in the app's asar archives,
// qsdmcli is packed and executable, and nothing of the miner or the edge
// helpers is (mining needs an NVIDIA GPU: Windows and Linux only). The app is
// not run here: it is signed only after this hook.
function afterPackMac(context) {
  const app = path.join(
    context.appOutDir,
    `${context.packager.appInfo.productFilename}.app`
  );
  const resources = path.join(app, 'Contents', 'Resources');
  // When the two architectures' app.asar differ (their native modules do),
  // the universal app keeps both, as app-x64.asar and app-arm64.asar, behind
  // a small app.asar that loads the right one.
  const perArchArchives = ['app-x64.asar', 'app-arm64.asar'].map((name) =>
    path.join(resources, name)
  );
  const archives = perArchArchives.every((archive) => fs.existsSync(archive))
    ? perArchArchives
    : [path.join(resources, 'app.asar')];
  const required = runtimeDependencies(
    path.resolve(__dirname, '../../release/app')
  );
  for (const archive of archives) {
    assertPackagedRuntimeDependencies(archive, required);
  }
  const qsdmcli = path.join(resources, 'native', 'qsdmcli');
  try {
    fs.accessSync(qsdmcli, fs.constants.X_OK);
  } catch {
    throw new Error(
      `Packaged macOS qsdmcli is missing or not executable: ${qsdmcli}`
    );
  }
  for (const folder of ['miner', 'edge']) {
    if (fs.existsSync(path.join(resources, folder))) {
      throw new Error(
        `The macOS app must not carry ${folder}/ (the miner and the Edge tools are Windows and Linux only)`
      );
    }
  }
}

exports.default = async function afterPack(context) {
  const platform = context.electronPlatformName;
  if (platform === 'darwin') {
    afterPackMac(context);
    return;
  }
  if (platform !== 'linux' && platform !== 'win32') return;
  const required = runtimeDependencies(
    path.resolve(__dirname, '../../release/app')
  );
  assertPackagedRuntimeDependencies(
    path.join(context.appOutDir, 'resources', 'app.asar'),
    required
  );
  const packagedRuntime = path.join(
    context.appOutDir,
    platform === 'win32'
      ? `${context.packager.appInfo.productFilename}.exe`
      : 'qsdm-hive'
  );
  const archive = path.join(context.appOutDir, 'resources', 'app.asar');
  const probe = `const path = require('path'); for (const name of ${JSON.stringify(
    required
  )}) { require(path.join(${JSON.stringify(
    archive
  )}, 'node_modules', name)); } console.log('QSDM_RUNTIME_DEPENDENCIES_OK');`;
  const probeOutput = execFileSync(packagedRuntime, ['-e', probe], {
    encoding: 'utf8',
    timeout: 30000,
    windowsHide: true,
    env: { ...process.env, ELECTRON_RUN_AS_NODE: '1' },
  });
  if (!probeOutput.includes('QSDM_RUNTIME_DEPENDENCIES_OK')) {
    throw new Error(
      'Packaged Hive runtime dependencies did not load successfully.'
    );
  }

  if (platform === 'win32') {
    const updateConfigPath = path.join(
      context.appOutDir,
      'resources',
      'app-update.yml'
    );
    const expectedUpdateConfig = [
      'provider: generic',
      'url: https://qsdm.tech/downloads/hive-v2',
      'updaterCacheDirName: qsdm-hive-runtime-updater',
      '',
    ].join('\n');
    if (!fs.existsSync(updateConfigPath)) {
      throw new Error(
        `QSDM Hive updater configuration was not packaged: ${updateConfigPath}`
      );
    }
    const updateConfig = fs
      .readFileSync(updateConfigPath, 'utf8')
      .replace(/\r\n/g, '\n');
    if (updateConfig !== expectedUpdateConfig) {
      throw new Error(
        `Packaged updater configuration is invalid: ${updateConfigPath}`
      );
    }
  }

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
      'native',
      `qsdm-hive-wallet-host${extension}`
    ),
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

  const extensionManifest = path.join(
    context.appOutDir,
    'resources',
    'wallet-extension',
    'manifest.json'
  );
  if (!fs.existsSync(extensionManifest)) {
    throw new Error(
      `QSDM Hive Wallet extension was not packaged: ${extensionManifest}`
    );
  }
  if (platform === 'linux') {
    const installer = path.join(
      context.appOutDir,
      'resources',
      'wallet-extension',
      'native-host',
      'install-linux.sh'
    );
    if (!fs.existsSync(installer)) {
      throw new Error(
        `QSDM native-host installer was not packaged: ${installer}`
      );
    }
    fs.chmodSync(installer, 0o755);
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
      executable: executables[5],
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
