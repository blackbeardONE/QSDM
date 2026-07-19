import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const TEST_ADDRESS =
  '13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9';
const PROVIDER_VERSION = 'qsdm-hive-wallet-provider/v1';
const EXPECTED_EXTENSION_ID = 'habkkkednignfkoffhpbjahcjbikkahh';

const testsDirectory = path.dirname(fileURLToPath(import.meta.url));
const extensionDirectory = path.resolve(testsDirectory, '..');
const workspaceDirectory = path.resolve(extensionDirectory, '..', '..');
const hiveDirectory = path.join(
  workspaceDirectory,
  'apps',
  'qsdm-hive',
  'qsdm-hive-main'
);
const hiveRequire = createRequire(path.join(hiveDirectory, 'package.json'));
const puppeteer = hiveRequire('puppeteer-core');

const readArgument = (name, fallback = '') => {
  const index = process.argv.indexOf(name);
  return index >= 0 && process.argv[index + 1]
    ? process.argv[index + 1]
    : fallback;
};

const defaultBrowser = [
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
].find((candidate) => fs.existsSync(candidate));

const browserPath = path.resolve(
  readArgument('--browser', defaultBrowser || '')
);
const nativeHostPath = path.resolve(
  readArgument(
    '--host',
    path.join(
      hiveDirectory,
      'native',
      'windows',
      'x64',
      'qsdm-hive-wallet-host.exe'
    )
  )
);
const keepProfile = process.argv.includes('--keep-profile');
const headful = process.argv.includes('--headful');

const stage = (message) => console.log(`[qsdm-wallet-acceptance] ${message}`);

const withTimeout = (promise, milliseconds, label) => {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(
      () => reject(new Error(`${label} timed out after ${milliseconds}ms`)),
      milliseconds
    );
    timer.unref?.();
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
};

if (process.platform !== 'win32') {
  throw new Error('This acceptance runner currently installs the Windows host.');
}
if (!browserPath || !fs.existsSync(browserPath)) {
  throw new Error('Chrome or Edge was not found. Pass --browser <path>.');
}
if (!fs.existsSync(nativeHostPath)) {
  throw new Error(
    `The native host was not found at ${nativeHostPath}. Build Hive native tools first.`
  );
}

const temporaryDirectory = fs.mkdtempSync(
  path.join(os.tmpdir(), 'qsdm-wallet-acceptance-')
);
const profileDirectory = path.join(temporaryDirectory, 'browser-profile');
const brokerStatePath = path.join(temporaryDirectory, 'broker.json');
const brokerToken = Buffer.from(
  'qsdm-wallet-acceptance-token-that-never-leaves-the-test-process'
)
  .toString('hex')
  .slice(0, 64)
  .padEnd(64, '0');

const requests = [];
let connected = false;
let expectedOrigin = '';

const readBody = (request) =>
  new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    request.on('data', (chunk) => {
      size += chunk.length;
      if (size > 64 * 1024) {
        reject(new Error('acceptance request exceeded 64 KiB'));
        request.destroy();
        return;
      }
      chunks.push(chunk);
    });
    request.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    request.on('error', reject);
  });

const responseFor = (payload) => {
  assert.equal(payload.version, PROVIDER_VERSION);
  const popupMethods = new Set([
    'qsdm_ping',
    'qsdm_getWalletInfo',
    'qsdm_openWallet',
  ]);
  assert.equal(
    payload.origin,
    popupMethods.has(payload.method)
      ? 'qsdm-extension://wallet-popup'
      : expectedOrigin
  );
  requests.push({ method: payload.method, params: payload.params });

  switch (payload.method) {
    case 'qsdm_ping':
      return { version: PROVIDER_VERSION, hive: true, signerReady: true };
    case 'qsdm_getWalletInfo':
      return { address: TEST_ADDRESS, ready: true, connectedSites: 0 };
    case 'qsdm_openWallet':
      return { opened: true };
    case 'qsdm_requestAccounts':
      connected = true;
      return [TEST_ADDRESS];
    case 'qsdm_accounts':
      return connected ? [TEST_ADDRESS] : [];
    case 'qsdm_getBalance':
      assert.equal(connected, true);
      return {
        address: TEST_ADDRESS,
        balance: 42.5,
        token: 'CELL',
        reachable: true,
      };
    case 'qsdm_signMessage':
      assert.equal(connected, true);
      assert.deepEqual(payload.params, { message: 'QSDM acceptance challenge' });
      return {
        address: TEST_ADDRESS,
        signature: 'mock-ml-dsa-signature',
      };
    case 'qsdm_sendTransaction':
      assert.equal(connected, true);
      assert.deepEqual(payload.params, {
        recipient: TEST_ADDRESS,
        amount: 0.125,
      });
      return { transactionId: 'mock-qsdm-transaction' };
    case 'qsdm_disconnect':
      connected = false;
      return { disconnected: true };
    default:
      throw new Error(`Unexpected method reached mock broker: ${payload.method}`);
  }
};

const server = http.createServer(async (request, response) => {
  if (request.method === 'GET' && request.url === '/acceptance') {
    response.writeHead(200, {
      'Content-Type': 'text/html; charset=utf-8',
      'Cache-Control': 'no-store',
      'Content-Security-Policy': "default-src 'self'; script-src 'self'",
    });
    response.end(
      '<!doctype html><html><head><title>QSDM Wallet Acceptance</title></head><body><main id="result">ready</main></body></html>'
    );
    return;
  }

  if (request.method !== 'POST' || request.url !== '/v1/request') {
    response.writeHead(404).end();
    return;
  }
  if (request.headers.authorization !== `Bearer ${brokerToken}`) {
    response.writeHead(404).end();
    return;
  }

  try {
    const payload = JSON.parse(await readBody(request));
    const result = responseFor(payload);
    response.writeHead(200, {
      'Content-Type': 'application/json; charset=utf-8',
      'Cache-Control': 'no-store',
    });
    response.end(JSON.stringify({ id: payload.id, ok: true, result }));
  } catch (error) {
    response.writeHead(400, { 'Content-Type': 'application/json' });
    response.end(
      JSON.stringify({
        ok: false,
        error: error instanceof Error ? error.message : String(error),
      })
    );
  }
});

const listen = () =>
  new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(server.address()));
  });

const closeServer = () =>
  new Promise((resolve) => {
    server.closeAllConnections?.();
    server.close(() => resolve());
  });

const browserArguments = [
  '--no-first-run',
  '--no-default-browser-check',
  '--disable-component-update',
];

const launchBrowser = (environment = process.env) =>
  withTimeout(
    puppeteer.launch({
      executablePath: browserPath,
      headless: headful ? false : true,
      enableExtensions: true,
      pipe: true,
      userDataDir: profileDirectory,
      env: environment,
      args: browserArguments,
    }),
    30000,
    'Browser launch'
  );

const attachExtensionDiagnostics = (browserToInspect) => {
  const attached = new Set();
  const attach = async (target) => {
    if (
      attached.has(target) ||
      target.type() !== 'service_worker' ||
      !target.url().startsWith('chrome-extension://')
    ) {
      return;
    }
    attached.add(target);
    const session = await target.createCDPSession();
    await session.send('Runtime.enable');
    session.on('Runtime.consoleAPICalled', ({ type, args }) => {
      const values = args.map((arg) => arg.value ?? arg.description ?? '');
      stage(`extension worker ${type}: ${values.join(' ')}`);
    });
    session.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
      stage(
        `extension worker exception: ${
          exceptionDetails.exception?.description || exceptionDetails.text
        }`
      );
    });
    stage(`extension worker attached: ${target.url()}`);
  };
  browserToInspect.on('targetcreated', (target) => {
    attach(target).catch((error) =>
      stage(`extension diagnostic attach failed: ${error.message}`)
    );
  });
  for (const target of browserToInspect.targets()) {
    attach(target).catch(() => undefined);
  }
};

const delay = (milliseconds) =>
  new Promise((resolve) => setTimeout(resolve, milliseconds));

const closeBrowser = async (browserToClose) => {
  const browserProcess = browserToClose?.process();
  await Promise.race([browserToClose.close(), delay(5000)]).catch(
    () => undefined
  );
  if (browserProcess && browserProcess.exitCode === null) {
    browserProcess.kill();
  }
  if (browserProcess && browserProcess.exitCode === null) {
    await withTimeout(
      new Promise((resolve) => browserProcess.once('exit', resolve)),
      5000,
      'Browser process exit'
    ).catch(() => undefined);
  }
  await delay(250);
};

const removeTemporaryDirectory = () => {
  const resolvedTemporaryRoot = path.resolve(os.tmpdir());
  const resolvedTarget = path.resolve(temporaryDirectory);
  if (
    path.dirname(resolvedTarget).toLowerCase() !==
      resolvedTemporaryRoot.toLowerCase() ||
    !path.basename(resolvedTarget).startsWith('qsdm-wallet-acceptance-')
  ) {
    throw new Error(`Refusing to remove unexpected path: ${resolvedTarget}`);
  }
  try {
    fs.rmSync(resolvedTarget, {
      recursive: true,
      force: true,
      maxRetries: 12,
      retryDelay: 250,
    });
  } catch (error) {
    console.warn(
      `Acceptance profile cleanup will be retried by the next run: ${error.message}`
    );
  }
};

const installNativeHost = (extensionId) => {
  assert.match(extensionId, /^[a-p]{32}$/);
  const installerPath = path.join(
    extensionDirectory,
    'native-host',
    'install-windows.ps1'
  );
  const installation = spawnSync(
    'powershell.exe',
    [
      '-NoProfile',
      '-ExecutionPolicy',
      'Bypass',
      '-File',
      installerPath,
      '-ExtensionId',
      extensionId,
      '-HostPath',
      nativeHostPath,
    ],
    { encoding: 'utf8', windowsHide: true }
  );
  if (installation.status !== 0) {
    throw new Error(
      installation.stderr || installation.stdout || 'Native host install failed'
    );
  }
  stage(`native host registered for ${extensionId}`);
};

const probeNativeHost = async () => {
  const payload = Buffer.from(
    JSON.stringify({
      version: PROVIDER_VERSION,
      id: 'direct-native-host-probe',
      origin: 'qsdm-extension://wallet-popup',
      method: 'qsdm_ping',
    }),
    'utf8'
  );
  const length = Buffer.alloc(4);
  length.writeUInt32LE(payload.length);
  const probe = spawn(nativeHostPath, [], {
    env: {
      ...process.env,
      QSDM_HIVE_BROKER_STATE: brokerStatePath,
    },
    windowsHide: true,
  });
  const outputChunks = [];
  const errorChunks = [];
  probe.stdout.on('data', (chunk) => outputChunks.push(chunk));
  probe.stderr.on('data', (chunk) => errorChunks.push(chunk));
  probe.stdin.end(Buffer.concat([length, payload]));
  const status = await withTimeout(
    new Promise((resolve, reject) => {
      probe.once('error', reject);
      probe.once('close', resolve);
    }),
    10000,
    'Direct native host probe'
  ).catch((error) => {
    probe.kill();
    throw error;
  });
  if (status !== 0) {
    throw new Error(
      Buffer.concat(errorChunks).toString('utf8') ||
        `Native host probe exited with ${status}`
    );
  }
  const output = Buffer.concat(outputChunks);
  if (output.length < 4) {
    throw new Error('Native host probe returned no framed response');
  }
  const responseLength = output.readUInt32LE(0);
  const response = JSON.parse(
    output.subarray(4, 4 + responseLength).toString('utf8')
  );
  assert.equal(response.ok, true);
  assert.equal(response.result?.hive, true);
  requests.length = 0;
  stage('direct native host framing and broker response passed');
};

const runProviderChecks = async (browser, testUrl) => {
  const page = await browser.newPage();
  page.on('console', (message) =>
    stage(`browser console ${message.type()}: ${message.text()}`)
  );
  page.on('pageerror', (error) => stage(`browser page error: ${error.message}`));
  await page.goto(testUrl, { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => Boolean(window.qsdm?.isQsdmHive), {
    timeout: 15000,
  });

  return page.evaluate(async () => {
    const accountEvents = [];
    window.qsdm.on('accountsChanged', (accounts) =>
      accountEvents.push(accounts)
    );
    let unsupportedError = '';
    try {
      await window.qsdm.request({ method: 'qsdm_unsupported' });
    } catch (error) {
      unsupportedError = error.message;
    }
    const initialAccounts = await window.qsdm.request({
      method: 'qsdm_accounts',
    });
    const connectedAccounts = await window.qsdm.request({
      method: 'qsdm_requestAccounts',
    });
    const balance = await window.qsdm.request({
      method: 'qsdm_getBalance',
    });
    const signature = await window.qsdm.request({
      method: 'qsdm_signMessage',
      params: { message: 'QSDM acceptance challenge' },
    });
    const transaction = await window.qsdm.request({
      method: 'qsdm_sendTransaction',
      params: {
        recipient:
          '13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9',
        amount: 0.125,
      },
    });
    const disconnected = await window.qsdm.request({
      method: 'qsdm_disconnect',
    });
    const finalAccounts = await window.qsdm.request({
      method: 'qsdm_accounts',
    });
    return {
      initialAccounts,
      connectedAccounts,
      balance,
      signature,
      transaction,
      unsupportedError,
      disconnected,
      finalAccounts,
      accountEvents,
    };
  });
};

let browser;
try {
  stage('starting isolated mock broker');
  const address = await listen();
  assert.equal(typeof address, 'object');
  expectedOrigin = `http://127.0.0.1:${address.port}`;
  fs.writeFileSync(
    brokerStatePath,
    `${JSON.stringify(
      {
        version: PROVIDER_VERSION,
        host: '127.0.0.1',
        port: address.port,
        token: brokerToken,
      },
      null,
      2
    )}\n`,
    { mode: 0o600 }
  );

  await probeNativeHost();

  stage('launching provider test browser');
  browser = await launchBrowser({
    ...process.env,
    QSDM_HIVE_BROKER_STATE: brokerStatePath,
  });
  attachExtensionDiagnostics(browser);
  stage('loading unpacked extension through Chromium debugging API');
  const extensionId = await withTimeout(
    browser.installExtension(extensionDirectory),
    15000,
    'Provider extension install'
  );
  if (!extensionId) {
    throw new Error('Chromium did not return an extension ID.');
  }
  assert.equal(
    extensionId,
    EXPECTED_EXTENSION_ID,
    'The unpacked extension must retain its pinned production identity.'
  );
  installNativeHost(extensionId);
  stage('testing website provider methods');
  const result = await withTimeout(
    runProviderChecks(browser, `${expectedOrigin}/acceptance`),
    30000,
    'Website provider checks'
  ).catch((error) => {
    stage(
      `mock broker methods before failure: ${
        requests.map((request) => request.method).join(', ') || '(none)'
      }`
    );
    throw error;
  });

  assert.deepEqual(result.initialAccounts, []);
  assert.deepEqual(result.connectedAccounts, [TEST_ADDRESS]);
  assert.equal(result.balance.balance, 42.5);
  assert.equal(result.balance.token, 'CELL');
  assert.equal(result.signature.signature, 'mock-ml-dsa-signature');
  assert.equal(result.transaction.transactionId, 'mock-qsdm-transaction');
  assert.match(result.unsupportedError, /Unsupported QSDM wallet method/);
  assert.deepEqual(result.disconnected, { disconnected: true });
  assert.deepEqual(result.finalAccounts, []);
  assert.deepEqual(result.accountEvents, [[TEST_ADDRESS], []]);

  stage('testing extension popup');
  const popup = await withTimeout(browser.newPage(), 10000, 'Popup creation');
  popup.on('console', (message) =>
    stage(`popup console ${message.type()}: ${message.text()}`)
  );
  popup.on('pageerror', (error) => stage(`popup page error: ${error.message}`));
  await withTimeout(
    popup.goto(`chrome-extension://${extensionId}/popup.html`),
    10000,
    'Popup navigation'
  );
  await withTimeout(
    popup.waitForFunction(
      () =>
        document.querySelector('#hive-status')?.textContent ===
        'Wallet ready',
      { timeout: 10000 }
    ),
    12000,
    'Popup connection check'
  ).catch(async (error) => {
    const popupState = await popup.evaluate(() => ({
      status: document.querySelector('#hive-status')?.textContent,
      address: document.querySelector('#wallet-address')?.textContent,
      notice: document.querySelector('#notice')?.textContent,
    }));
    stage(`popup state before failure: ${JSON.stringify(popupState)}`);
    throw error;
  });
  const popupAddress = await popup.$eval(
    '#wallet-address',
    (element) => element.textContent
  );
  assert.equal(
    popupAddress,
    `${TEST_ADDRESS.slice(0, 10)}...${TEST_ADDRESS.slice(-8)}`
  );
  assert.equal(
    await popup.$eval('#site-name', (element) => element.textContent),
    'Unavailable on this page'
  );

  const methods = requests.map((request) => request.method);
  assert.deepEqual(methods, [
    'qsdm_accounts',
    'qsdm_requestAccounts',
    'qsdm_getBalance',
    'qsdm_signMessage',
    'qsdm_sendTransaction',
    'qsdm_disconnect',
    'qsdm_accounts',
    'qsdm_ping',
    'qsdm_getWalletInfo',
  ]);

  console.log(
    JSON.stringify(
      {
        ok: true,
        browser: browserPath,
        extensionId,
        nativeHost: nativeHostPath,
        testedMethods: methods,
        realCellBroadcast: false,
      },
      null,
      2
    )
  );
} finally {
  stage('cleaning up acceptance processes');
  if (browser) await closeBrowser(browser);
  await withTimeout(closeServer(), 5000, 'Mock broker shutdown').catch(
    () => undefined
  );
  if (!keepProfile) {
    removeTemporaryDirectory();
  } else {
    console.log(`Acceptance profile retained at ${temporaryDirectory}`);
  }
}
