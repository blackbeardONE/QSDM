import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const TEST_ADDRESS =
  "13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9";
const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";
const EXPECTED_EXTENSION_ID = "habkkkednignfkoffhpbjahcjbikkahh";

const testsDirectory = path.dirname(fileURLToPath(import.meta.url));
const extensionDirectory = path.resolve(testsDirectory, "..");
const workspaceDirectory = path.resolve(extensionDirectory, "..", "..");
const hiveDirectory = path.join(
  workspaceDirectory,
  "apps",
  "qsdm-hive",
  "qsdm-hive-main"
);
const hiveRequire = createRequire(path.join(hiveDirectory, "package.json"));
const puppeteer = hiveRequire("puppeteer-core");
const walletProviderScriptPath = path.join(
  workspaceDirectory,
  "QSDM",
  "deploy",
  "landing",
  "wallet-provider.js"
);
const walletStartScriptPath = path.join(
  workspaceDirectory,
  "QSDM",
  "deploy",
  "landing",
  "wallet-start.js"
);
const landingDirectory = path.join(
  workspaceDirectory,
  "QSDM",
  "deploy",
  "landing"
);
const accountPagePath = path.join(landingDirectory, "account", "index.html");
const accountScriptPath = path.join(landingDirectory, "account", "account.js");
const accountStylePath = path.join(landingDirectory, "account", "account.css");
const siteStylePath = path.join(landingDirectory, "assets", "site.css");
const siteIconPath = path.join(
  landingDirectory,
  "assets",
  "qsdm-hive-icon.png"
);

const readArgument = (name, fallback = "") => {
  const index = process.argv.indexOf(name);
  return index >= 0 && process.argv[index + 1]
    ? process.argv[index + 1]
    : fallback;
};

const defaultBrowser = [
  "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
  "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
].find((candidate) => fs.existsSync(candidate));

const browserPath = path.resolve(
  readArgument("--browser", defaultBrowser || "")
);
const nativeHostPath = path.resolve(
  readArgument(
    "--host",
    path.join(
      hiveDirectory,
      "native",
      "windows",
      "x64",
      "qsdm-hive-wallet-host.exe"
    )
  )
);
const keepProfile = process.argv.includes("--keep-profile");
const headful = process.argv.includes("--headful");
const screenshotDirectory = readArgument("--screenshot-directory");
const nativeHostRegistryKeys = [
  "HKCU\\Software\\Google\\Chrome\\NativeMessagingHosts\\tech.qsdm.hive_wallet",
  "HKCU\\Software\\Microsoft\\Edge\\NativeMessagingHosts\\tech.qsdm.hive_wallet",
  "HKCU\\Software\\Mozilla\\NativeMessagingHosts\\tech.qsdm.hive_wallet",
];

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

if (process.platform !== "win32") {
  throw new Error(
    "This acceptance runner currently installs the Windows host."
  );
}
if (!browserPath || !fs.existsSync(browserPath)) {
  throw new Error("Chrome or Edge was not found. Pass --browser <path>.");
}
if (!fs.existsSync(nativeHostPath)) {
  throw new Error(
    `The native host was not found at ${nativeHostPath}. Build Hive native tools first.`
  );
}

const temporaryDirectory = fs.mkdtempSync(
  path.join(os.tmpdir(), "qsdm-wallet-acceptance-")
);
const profileDirectory = path.join(temporaryDirectory, "browser-profile");
const brokerStatePath = path.join(temporaryDirectory, "broker.json");
const brokerToken = Buffer.from(
  "qsdm-wallet-acceptance-token-that-never-leaves-the-test-process"
)
  .toString("hex")
  .slice(0, 64)
  .padEnd(64, "0");

const requests = [];
let connected = false;
let expectedOrigin = "";
let accountWallets = [];
let accountTelegram = "";
let accountSessionCount = 2;
let accountDeleted = false;

const readBody = (request) =>
  new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    request.on("data", (chunk) => {
      size += chunk.length;
      if (size > 64 * 1024) {
        reject(new Error("acceptance request exceeded 64 KiB"));
        request.destroy();
        return;
      }
      chunks.push(chunk);
    });
    request.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    request.on("error", reject);
  });

const responseFor = (payload) => {
  assert.equal(payload.version, PROVIDER_VERSION);
  const internal = payload.origin === "qsdm-extension://wallet-popup";
  assert.ok(
    internal || payload.origin === expectedOrigin,
    `Unexpected wallet request origin: ${payload.origin}`
  );
  requests.push({
    method: payload.method,
    params: payload.params,
    origin: payload.origin,
  });

  switch (payload.method) {
    case "qsdm_ping":
      return { version: PROVIDER_VERSION, hive: true, signerReady: true };
    case "qsdm_getWalletInfo":
      return { address: TEST_ADDRESS, ready: true, connectedSites: 0 };
    case "qsdm_openWallet":
      return { opened: true };
    case "qsdm_requestAccounts":
      connected = true;
      return [TEST_ADDRESS];
    case "qsdm_accounts":
      return connected ? [TEST_ADDRESS] : [];
    case "qsdm_getBalance":
      if (!internal) assert.equal(connected, true);
      return {
        address: TEST_ADDRESS,
        balance: 42.5,
        token: "CELL",
        reachable: true,
      };
    case "qsdm_signMessage":
      assert.equal(connected, true);
      assert.equal(typeof payload.params?.message, "string");
      assert.ok(
        payload.params.message === "QSDM acceptance challenge" ||
          payload.params.message === "QSDM Account acceptance wallet challenge"
      );
      return {
        address: TEST_ADDRESS,
        public_key: "mock-ml-dsa-public-key",
        signature: "mock-ml-dsa-signature",
      };
    case "qsdm_sendTransaction":
      if (!internal) assert.equal(connected, true);
      assert.equal(payload.params?.recipient, TEST_ADDRESS);
      assert.ok([0.125, 0.25, 0.5].includes(payload.params?.amount));
      return { transactionId: "mock-qsdm-transaction" };
    case "qsdm_disconnect":
      connected = false;
      return { disconnected: true };
    default:
      throw new Error(
        `Unexpected method reached mock broker: ${payload.method}`
      );
  }
};

const server = http.createServer(async (request, response) => {
  const requestUrl = new URL(request.url || "/", "http://127.0.0.1");
  if (request.method === "GET" && request.url === "/favicon.ico") {
    response.writeHead(204).end();
    return;
  }

  if (request.method === "GET" && request.url === "/acceptance") {
    response.writeHead(200, {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Content-Security-Policy": "default-src 'self'; script-src 'self'",
    });
    response.end(
      '<!doctype html><html><head><title>QSDM Wallet Acceptance</title></head><body><main id="result">ready</main></body></html>'
    );
    return;
  }

  if (request.method === "GET" && request.url === "/wallet-provider.js") {
    response.writeHead(200, {
      "Content-Type": "text/javascript; charset=utf-8",
      "Cache-Control": "no-store",
    });
    response.end(fs.readFileSync(walletProviderScriptPath));
    return;
  }

  if (request.method === "GET" && request.url === "/wallet-start.js") {
    response.writeHead(200, {
      "Content-Type": "text/javascript; charset=utf-8",
      "Cache-Control": "no-store",
    });
    response.end(fs.readFileSync(walletStartScriptPath));
    return;
  }

  if (request.method === "GET" && request.url === "/wallet-start-acceptance") {
    response.writeHead(200, {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Content-Security-Policy": "default-src 'self'; script-src 'self'",
    });
    response.end(
      '<!doctype html><html><head><title>QSDM Wallet Handoff Acceptance</title></head><body><p id="wallet-handoff-status"></p><button id="wallet-handoff-open">Open</button><script src="/wallet-start.js"></script></body></html>'
    );
    return;
  }

  if (request.method === "GET" && request.url === "/download.html") {
    response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    response.end("<!doctype html><title>QSDM Download</title>");
    return;
  }

  const staticFiles = new Map([
    ["/account/", [accountPagePath, "text/html; charset=utf-8"]],
    [
      "/account/account.js",
      [accountScriptPath, "text/javascript; charset=utf-8"],
    ],
    ["/account/account.css", [accountStylePath, "text/css; charset=utf-8"]],
    ["/assets/site.css", [siteStylePath, "text/css; charset=utf-8"]],
    ["/assets/qsdm-hive-icon.png", [siteIconPath, "image/png"]],
  ]);
  if (request.method === "GET" && staticFiles.has(requestUrl.pathname)) {
    const [filePath, contentType] = staticFiles.get(requestUrl.pathname);
    response.writeHead(200, {
      "Content-Type": contentType,
      "Cache-Control": "no-store",
    });
    response.end(fs.readFileSync(filePath));
    return;
  }

  if (
    request.method === "GET" &&
    requestUrl.pathname === "/api/account/config"
  ) {
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({
        ok: true,
        login: { email: true, telegram: true },
      })
    );
    return;
  }

  if (request.method === "GET" && requestUrl.pathname === "/api/account/me") {
    if (accountDeleted) {
      response.writeHead(401, { "Content-Type": "application/json" });
      response.end(
        JSON.stringify({
          error: {
            code: "not_authenticated",
            message: "Sign in to QSDM Account.",
          },
        })
      );
      return;
    }
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({
        ok: true,
        csrf_token: "acceptance-csrf",
        account: {
          id: "acct_acceptance",
          email: "t***@example.com",
          telegram: accountTelegram,
          wallets: accountWallets,
        },
      })
    );
    return;
  }

  if (
    request.method === "GET" &&
    requestUrl.pathname === "/api/account/sessions"
  ) {
    const now = Date.now();
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({
        ok: true,
        sessions: Array.from({ length: accountSessionCount }, (_, index) => ({
          created_at: new Date(now - (index + 1) * 60000).toISOString(),
          expires_at: new Date(now + 3600000).toISOString(),
          current: index === 0,
        })),
      })
    );
    return;
  }

  if (
    request.method === "POST" &&
    requestUrl.pathname === "/api/account/sessions/revoke-others"
  ) {
    assert.equal(request.headers["x-qsdm-csrf"], "acceptance-csrf");
    const revoked = Math.max(0, accountSessionCount - 1);
    accountSessionCount = 1;
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ ok: true, revoked }));
    return;
  }

  if (
    request.method === "DELETE" &&
    requestUrl.pathname === "/api/account/profile"
  ) {
    assert.equal(request.headers["x-qsdm-csrf"], "acceptance-csrf");
    assert.deepEqual(JSON.parse(await readBody(request)), {
      confirmation: "DELETE",
    });
    accountDeleted = true;
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({
        ok: true,
        message:
          "QSDM Account data was deleted. Hive, wallet keys, and CELL were not changed.",
      })
    );
    return;
  }

  if (
    request.method === "POST" &&
    requestUrl.pathname === "/api/account/identities/telegram/start"
  ) {
    assert.equal(request.headers["x-qsdm-csrf"], "acceptance-csrf");
    accountTelegram = "@acceptance";
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({ ok: true, url: "/account/?linked=telegram" })
    );
    return;
  }

  if (
    request.method === "POST" &&
    requestUrl.pathname === "/api/account/wallets/challenge"
  ) {
    const payload = JSON.parse(await readBody(request));
    assert.equal(request.headers["x-qsdm-csrf"], "acceptance-csrf");
    assert.equal(payload.address, TEST_ADDRESS);
    response.writeHead(201, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({
        ok: true,
        challenge: {
          id: "acceptance-wallet-challenge",
          message: "QSDM Account acceptance wallet challenge",
          expires_at: new Date(Date.now() + 300000).toISOString(),
        },
      })
    );
    return;
  }

  if (
    request.method === "POST" &&
    requestUrl.pathname === "/api/account/wallets/confirm"
  ) {
    const payload = JSON.parse(await readBody(request));
    assert.equal(request.headers["x-qsdm-csrf"], "acceptance-csrf");
    assert.deepEqual(payload, {
      challenge_id: "acceptance-wallet-challenge",
      address: TEST_ADDRESS,
      public_key: "mock-ml-dsa-public-key",
      signature: "mock-ml-dsa-signature",
    });
    accountWallets = [
      { address: TEST_ADDRESS, linked_at: new Date().toISOString() },
    ];
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ ok: true, address: TEST_ADDRESS }));
    return;
  }

  if (
    request.method === "POST" &&
    requestUrl.pathname === "/api/account/wallets/unlink"
  ) {
    const payload = JSON.parse(await readBody(request));
    assert.equal(request.headers["x-qsdm-csrf"], "acceptance-csrf");
    assert.deepEqual(payload, { address: TEST_ADDRESS });
    accountWallets = [];
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({ ok: true, address: TEST_ADDRESS, unlinked: true })
    );
    return;
  }

  if (
    request.method === "GET" &&
    requestUrl.pathname === "/api/v1/wallet/balance"
  ) {
    assert.equal(requestUrl.searchParams.get("address"), TEST_ADDRESS);
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ balance: 42.5 }));
    return;
  }

  if (
    request.method === "GET" &&
    request.url === "/wallet-provider-acceptance"
  ) {
    response.writeHead(200, {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Content-Security-Policy":
        "default-src 'self'; script-src 'self'; style-src 'unsafe-inline'",
    });
    response.end(
      '<!doctype html><html><head><title>QSDM Web Wallet Acceptance</title></head><body><main><section class="hero"></section></main><script src="/wallet-provider.js"></script></body></html>'
    );
    return;
  }

  if (request.method !== "POST" || request.url !== "/v1/request") {
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
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "no-store",
    });
    response.end(JSON.stringify({ id: payload.id, ok: true, result }));
  } catch (error) {
    response.writeHead(400, { "Content-Type": "application/json" });
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
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve(server.address()));
  });

const closeServer = () =>
  new Promise((resolve) => {
    server.closeAllConnections?.();
    server.close(() => resolve());
  });

const browserArguments = [
  "--no-first-run",
  "--no-default-browser-check",
  "--disable-component-update",
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
    "Browser launch"
  );

const attachExtensionDiagnostics = (browserToInspect) => {
  const attached = new Set();
  const attach = async (target) => {
    if (
      attached.has(target) ||
      target.type() !== "service_worker" ||
      !target.url().startsWith("chrome-extension://")
    ) {
      return;
    }
    attached.add(target);
    const session = await target.createCDPSession();
    await session.send("Runtime.enable");
    session.on("Runtime.consoleAPICalled", ({ type, args }) => {
      const values = args.map((arg) => arg.value ?? arg.description ?? "");
      stage(`extension worker ${type}: ${values.join(" ")}`);
    });
    session.on("Runtime.exceptionThrown", ({ exceptionDetails }) => {
      stage(
        `extension worker exception: ${
          exceptionDetails.exception?.description || exceptionDetails.text
        }`
      );
    });
    stage(`extension worker attached: ${target.url()}`);
  };
  browserToInspect.on("targetcreated", (target) => {
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
      new Promise((resolve) => browserProcess.once("exit", resolve)),
      5000,
      "Browser process exit"
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
    !path.basename(resolvedTarget).startsWith("qsdm-wallet-acceptance-")
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
    "native-host",
    "install-windows.ps1"
  );
  const installation = spawnSync(
    "powershell.exe",
    [
      "-NoProfile",
      "-ExecutionPolicy",
      "Bypass",
      "-File",
      installerPath,
      "-ExtensionId",
      extensionId,
      "-HostPath",
      nativeHostPath,
    ],
    { encoding: "utf8", windowsHide: true }
  );
  if (installation.status !== 0) {
    throw new Error(
      installation.stderr || installation.stdout || "Native host install failed"
    );
  }
  stage(`native host registered for ${extensionId}`);
};

const snapshotNativeHostRegistrations = () =>
  nativeHostRegistryKeys.map((registryKey) => {
    const query = spawnSync("reg.exe", ["QUERY", registryKey, "/ve"], {
      encoding: "utf8",
      windowsHide: true,
    });
    const match =
      query.status === 0 ? query.stdout.match(/REG_SZ\s+(.+)\r?$/m) : null;
    return { registryKey, manifestPath: match ? match[1].trim() : "" };
  });

const restoreNativeHostRegistrations = (snapshot) => {
  for (const { registryKey, manifestPath } of snapshot || []) {
    const args = manifestPath
      ? ["ADD", registryKey, "/ve", "/t", "REG_SZ", "/d", manifestPath, "/f"]
      : ["DELETE", registryKey, "/f"];
    const restored = spawnSync("reg.exe", args, {
      encoding: "utf8",
      windowsHide: true,
    });
    if (manifestPath && restored.status !== 0) {
      throw new Error(
        `Could not restore native host registration ${registryKey}`
      );
    }
  }
};

const probeNativeHost = async () => {
  const payload = Buffer.from(
    JSON.stringify({
      version: PROVIDER_VERSION,
      id: "direct-native-host-probe",
      origin: "qsdm-extension://wallet-popup",
      method: "qsdm_ping",
    }),
    "utf8"
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
  probe.stdout.on("data", (chunk) => outputChunks.push(chunk));
  probe.stderr.on("data", (chunk) => errorChunks.push(chunk));
  probe.stdin.end(Buffer.concat([length, payload]));
  const status = await withTimeout(
    new Promise((resolve, reject) => {
      probe.once("error", reject);
      probe.once("close", resolve);
    }),
    10000,
    "Direct native host probe"
  ).catch((error) => {
    probe.kill();
    throw error;
  });
  if (status !== 0) {
    throw new Error(
      Buffer.concat(errorChunks).toString("utf8") ||
        `Native host probe exited with ${status}`
    );
  }
  const output = Buffer.concat(outputChunks);
  if (output.length < 4) {
    throw new Error("Native host probe returned no framed response");
  }
  const responseLength = output.readUInt32LE(0);
  const response = JSON.parse(
    output.subarray(4, 4 + responseLength).toString("utf8")
  );
  assert.equal(response.ok, true);
  assert.equal(response.result?.hive, true);
  requests.length = 0;
  stage("direct native host framing and broker response passed");
};

const runProviderChecks = async (browser, testUrl) => {
  const page = await browser.newPage();
  page.on("console", (message) =>
    stage(`browser console ${message.type()}: ${message.text()}`)
  );
  page.on("pageerror", (error) =>
    stage(`browser page error: ${error.message}`)
  );
  await page.goto(testUrl, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => Boolean(window.qsdm?.isQsdmHive), {
    timeout: 15000,
  });

  return page.evaluate(async () => {
    const accountEvents = [];
    window.qsdm.on("accountsChanged", (accounts) =>
      accountEvents.push(accounts)
    );
    let unsupportedError = "";
    try {
      await window.qsdm.request({ method: "qsdm_unsupported" });
    } catch (error) {
      unsupportedError = error.message;
    }
    const initialAccounts = await window.qsdm.request({
      method: "qsdm_accounts",
    });
    const connectedAccounts = await window.qsdm.request({
      method: "qsdm_requestAccounts",
    });
    const balance = await window.qsdm.request({
      method: "qsdm_getBalance",
    });
    const signature = await window.qsdm.request({
      method: "qsdm_signMessage",
      params: { message: "QSDM acceptance challenge" },
    });
    const transaction = await window.qsdm.request({
      method: "qsdm_sendTransaction",
      params: {
        recipient:
          "13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9",
        amount: 0.125,
      },
    });
    const disconnected = await window.qsdm.request({
      method: "qsdm_disconnect",
    });
    const finalAccounts = await window.qsdm.request({
      method: "qsdm_accounts",
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

const runWebWalletChecks = async (browser, testUrl) => {
  const page = await browser.newPage();
  page.on("pageerror", (error) =>
    stage(`web wallet page error: ${error.message}`)
  );
  await page.goto(testUrl, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("#qsdm-hive-provider-panel", { timeout: 15000 });
  await page.waitForFunction(
    () =>
      document.querySelector("#hive-provider-state")?.textContent ===
      "Ready to connect",
    { timeout: 15000 }
  );
  await page.click("#hive-provider-connect");
  await page.waitForFunction(
    () =>
      document.querySelector("#hive-provider-state")?.textContent ===
        "Connected through QSDM Hive" &&
      document.querySelector("#hive-provider-balance")?.textContent ===
        "42.5 CELL",
    { timeout: 15000 }
  );
  await page.type("#hive-provider-recipient", TEST_ADDRESS);
  await page.type("#hive-provider-amount", "0.125");
  await page.click("#hive-provider-send");
  await page.waitForFunction(
    () =>
      document
        .querySelector("#hive-provider-notice")
        ?.textContent?.includes("mock-qsdm-transaction"),
    { timeout: 15000 }
  );
  await page.click("#hive-provider-disconnect");
  await page.waitForFunction(
    () =>
      document.querySelector("#hive-provider-address")?.textContent ===
      "Not connected",
    { timeout: 15000 }
  );
  return page.evaluate(() => ({
    state: document.querySelector("#hive-provider-state")?.textContent,
    address: document.querySelector("#hive-provider-address")?.textContent,
    notice: document.querySelector("#hive-provider-notice")?.textContent,
  }));
};

const runAccountDashboardChecks = async (browser, testUrl) => {
  const page = await browser.newPage();
  const consoleProblems = [];
  page.on("pageerror", (error) =>
    stage(`account dashboard page error: ${error.message}`)
  );
  page.on("console", (message) => {
    if (["error", "warning"].includes(message.type())) {
      consoleProblems.push(`${message.type()}: ${message.text()}`);
    }
  });
  await page.goto(testUrl, { waitUntil: "domcontentloaded" });
  assert.equal(await page.title(), "QSDM Account");
  assert.equal(new URL(page.url()).pathname, "/account/");
  await page.waitForFunction(
    () =>
      !document.querySelector("#dashboard-view")?.hidden &&
      document
        .querySelector("#identity-summary")
        ?.textContent?.includes("t***@example.com"),
    { timeout: 15000 }
  );
  await page.waitForFunction(
    () =>
      document.querySelector("#email-identity-value")?.textContent ===
        "t***@example.com" &&
      document.querySelector("#add-telegram-identity")?.hidden === false &&
      document.querySelector("#session-summary")?.textContent ===
        "2 active browser sessions",
    { timeout: 15000 }
  );
  await page.click("#revoke-other-sessions");
  await page.waitForFunction(
    () =>
      document.querySelector("#session-summary")?.textContent ===
        "1 active browser session" &&
      document
        .querySelector("#dashboard-status")
        ?.textContent?.includes("Signed out 1 other browser session"),
    { timeout: 15000 }
  );
  await page.click("#add-telegram-identity");
  await page.waitForFunction(
    () =>
      document.querySelector("#telegram-identity-value")?.textContent ===
        "@acceptance" &&
      document
        .querySelector("#dashboard-status")
        ?.textContent?.includes("Telegram now opens this QSDM Account"),
    { timeout: 15000 }
  );
  await page.click("#link-wallet");
  await page.waitForFunction(
    () =>
      document.querySelector("#dashboard-status")?.textContent ===
        "Wallet linked successfully." &&
      document.querySelector("#wallet-count")?.textContent === "1" &&
      document.querySelector("#total-balance")?.textContent === "42.5 CELL" &&
      document.querySelector("#active-wallet-balance")?.textContent ===
        "42.5 CELL",
    { timeout: 15000 }
  );
  await page.type("#active-wallet-recipient", TEST_ADDRESS);
  await page.type("#active-wallet-amount", "0.25");
  await page.click("#active-wallet-send");
  await page.waitForFunction(
    () =>
      document
        .querySelector("#active-wallet-status")
        ?.textContent?.includes("mock-qsdm-transaction"),
    { timeout: 15000 }
  );
  const transferStatus = await page.$eval(
    "#active-wallet-status",
    (element) => element.textContent
  );
  if (screenshotDirectory) {
    fs.mkdirSync(screenshotDirectory, { recursive: true });
    await page.setViewport({ width: 1280, height: 800 });
    await page.screenshot({
      path: path.join(
        screenshotDirectory,
        "qsdm-account-dashboard-desktop.png"
      ),
      fullPage: true,
    });
    await page.setViewport({ width: 390, height: 844 });
    await page.screenshot({
      path: path.join(screenshotDirectory, "qsdm-account-dashboard-mobile.png"),
      fullPage: true,
    });
  }
  page.once("dialog", (dialog) => dialog.accept());
  await page.click(".wallet-unlink");
  await page.waitForFunction(
    () =>
      document.querySelector("#wallet-count")?.textContent === "0" &&
      document.querySelector("#wallet-empty")?.hidden === false,
    { timeout: 15000 }
  );
  const walletStatus = await page.$eval(
    "#dashboard-status",
    (element) => element.textContent
  );
  await page.click("#open-delete-account");
  await page.waitForFunction(
    () => document.querySelector("#delete-account-dialog")?.open === true,
    { timeout: 15000 }
  );
  if (screenshotDirectory) {
    await page.setViewport({ width: 1280, height: 800 });
    await page.screenshot({
      path: path.join(
        screenshotDirectory,
        "qsdm-account-delete-dialog-desktop.png"
      ),
      fullPage: true,
    });
    await page.setViewport({ width: 390, height: 844 });
    await page.screenshot({
      path: path.join(
        screenshotDirectory,
        "qsdm-account-delete-dialog-mobile.png"
      ),
      fullPage: true,
    });
  }
  await page.type("#delete-account-confirmation", "delete");
  assert.equal(
    await page.$eval("#confirm-delete-account", (element) => element.disabled),
    true
  );
  await page.click("#delete-account-confirmation", { clickCount: 3 });
  await page.type("#delete-account-confirmation", "DELETE");
  await page.click("#confirm-delete-account");
  await page.waitForFunction(
    () =>
      !document.querySelector("#login-view")?.hidden &&
      document
        .querySelector("#login-status")
        ?.textContent?.includes("QSDM Account data was deleted"),
    { timeout: 15000 }
  );
  assert.deepEqual(consoleProblems, []);
  return page
    .evaluate(() => ({
      identity: document.querySelector("#identity-summary")?.textContent,
      walletCount: document.querySelector("#wallet-count")?.textContent,
      balance: document.querySelector("#total-balance")?.textContent,
      deletionStatus: document.querySelector("#login-status")?.textContent,
      telegram: document.querySelector("#telegram-identity-value")?.textContent,
      sessions: document.querySelector("#session-summary")?.textContent,
    }))
    .then((result) => ({ ...result, walletStatus, transferStatus }));
};

const runOnboardingChecks = async (browser, testUrl, extensionId) => {
  const existingPages = new Set(await browser.pages());
  const handoff = await browser.newPage();
  handoff.on("pageerror", (error) =>
    stage(`wallet handoff page error: ${error.message}`)
  );
  await handoff.goto(testUrl, { waitUntil: "domcontentloaded" });
  await handoff.waitForFunction(
    () =>
      document
        .querySelector("#wallet-handoff-status")
        ?.textContent?.includes("opened in a new tab"),
    { timeout: 15000 }
  );

  const onboarding = (await browser.pages()).find(
    (page) =>
      !existingPages.has(page) &&
      page
        .url()
        .startsWith(
          `chrome-extension://${extensionId}/home.html#/onboarding/welcome?`
        )
  );
  assert.ok(onboarding, "The website handoff must open extension onboarding.");
  onboarding.on("pageerror", (error) =>
    stage(`onboarding page error: ${error.message}`)
  );
  await onboarding.waitForFunction(
    () =>
      document.querySelector("#state-label")?.textContent ===
        "Active Hive wallet" &&
      document.querySelector("#use-hive")?.disabled === false,
    { timeout: 15000 }
  );

  const onboardingUrl = new URL(onboarding.url());
  const hashParams = new URLSearchParams(
    onboardingUrl.hash.slice(onboardingUrl.hash.indexOf("?") + 1)
  );
  assert.equal(hashParams.get("login"), "new");
  assert.equal(hashParams.get("origin"), expectedOrigin);
  assert.equal(
    await onboarding.$eval(
      "#requesting-site",
      (element) => element.textContent
    ),
    "Requested by 127.0.0.1"
  );
  assert.equal((await onboarding.$$("#google-login")).length, 0);
  assert.equal((await onboarding.$$("#apple-login")).length, 0);
  assert.equal((await onboarding.$$("#email-login")).length, 0);

  const telegramTargetPromise = browser.waitForTarget(
    (target) =>
      target.url() ===
      "https://qsdm.tech/account/?login=telegram&source=extension",
    { timeout: 15000 }
  );
  await onboarding.click("#telegram-login");
  const telegramTarget = await telegramTargetPromise;
  assert.equal(
    await onboarding.$eval("#notice", (element) => element.textContent),
    "Opening secure Telegram login..."
  );
  const telegramPage = await telegramTarget.page();
  await telegramPage?.close();

  if (screenshotDirectory) {
    fs.mkdirSync(screenshotDirectory, { recursive: true });
    await onboarding.setViewport({ width: 1440, height: 900 });
    await onboarding.screenshot({
      path: path.join(
        screenshotDirectory,
        "qsdm-wallet-onboarding-desktop.png"
      ),
      fullPage: true,
    });
    await onboarding.setViewport({ width: 390, height: 844 });
    await onboarding.screenshot({
      path: path.join(screenshotDirectory, "qsdm-wallet-onboarding-mobile.png"),
      fullPage: true,
    });
  }
  await onboarding.click("#use-hive");
  await onboarding.waitForFunction(
    () =>
      document.querySelector("#notice")?.textContent ===
      "Wallet connected. Return to the requesting website.",
    { timeout: 15000 }
  );

  return {
    onboardingUrl: onboarding.url(),
    address: await onboarding.$eval(
      "#wallet-address",
      (element) => element.textContent
    ),
  };
};

const runExtensionWalletDashboardChecks = async (browser, extensionId) => {
  const page = await browser.newPage();
  const consoleProblems = [];
  page.on("pageerror", (error) =>
    consoleProblems.push(`pageerror: ${error.message}`)
  );
  page.on("console", (message) => {
    if (["error", "warning"].includes(message.type())) {
      consoleProblems.push(`${message.type()}: ${message.text()}`);
    }
  });
  await page.goto(`chrome-extension://${extensionId}/home.html#/wallet`, {
    waitUntil: "domcontentloaded",
  });
  await page.waitForFunction(
    () =>
      document.querySelector("#dashboard-balance")?.textContent ===
        "42.5 CELL" &&
      document.querySelector("#send-fields")?.disabled === false,
    { timeout: 15000 }
  );
  assert.equal(
    await page.$eval("#onboarding-view", (element) => element.hidden),
    true
  );
  assert.equal(
    await page.$eval("#wallet-dashboard", (element) => element.hidden),
    false
  );
  await page.type("#send-recipient", TEST_ADDRESS);
  await page.type("#send-amount", "0.5");
  await page.click("#send-cell");
  await page.waitForFunction(
    () =>
      document
        .querySelector("#transfer-status")
        ?.textContent?.includes("mock-qsdm-transaction") &&
      document.querySelector("#balance-state")?.textContent ===
        "Live CELL balance" &&
      document.querySelector("#send-fields")?.disabled === false,
    { timeout: 15000 }
  );
  if (screenshotDirectory) {
    await page.setViewport({ width: 1280, height: 800 });
    await page.screenshot({
      path: path.join(screenshotDirectory, "qsdm-wallet-dashboard-desktop.png"),
      fullPage: true,
    });
    await page.setViewport({ width: 390, height: 844 });
    await page.screenshot({
      path: path.join(screenshotDirectory, "qsdm-wallet-dashboard-mobile.png"),
      fullPage: true,
    });
  }
  assert.deepEqual(consoleProblems, []);
  return page.evaluate(() => ({
    address: document.querySelector("#dashboard-address")?.textContent,
    balance: document.querySelector("#dashboard-balance")?.textContent,
    status: document.querySelector("#transfer-status")?.textContent,
  }));
};

const runMissingExtensionHandoffCheck = async (browser, testUrl) => {
  const page = await browser.newPage();
  await page.goto(testUrl, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(
    () =>
      window.location.pathname === "/download.html" &&
      window.location.hash === "#wallet-extension-title",
    { timeout: 10000 }
  );
  return page.url();
};

let browser;
let nativeHostRegistrySnapshot;
try {
  stage("starting isolated mock broker");
  const address = await listen();
  assert.equal(typeof address, "object");
  expectedOrigin = `http://127.0.0.1:${address.port}`;
  fs.writeFileSync(
    brokerStatePath,
    `${JSON.stringify(
      {
        version: PROVIDER_VERSION,
        host: "127.0.0.1",
        port: address.port,
        token: brokerToken,
        pid: process.pid,
        startedAt: new Date().toISOString(),
      },
      null,
      2
    )}\n`,
    { mode: 0o600 }
  );

  await probeNativeHost();

  stage("launching provider test browser");
  browser = await launchBrowser({
    ...process.env,
    QSDM_HIVE_BROKER_STATE: brokerStatePath,
  });
  attachExtensionDiagnostics(browser);
  stage("testing missing-extension download fallback");
  const fallbackUrl = await withTimeout(
    runMissingExtensionHandoffCheck(
      browser,
      `${expectedOrigin}/wallet-start-acceptance`
    ),
    15000,
    "Missing-extension handoff check"
  );
  assert.equal(
    fallbackUrl,
    `${expectedOrigin}/download.html#wallet-extension-title`
  );
  stage("loading unpacked extension through Chromium debugging API");
  const extensionId = await withTimeout(
    browser.installExtension(extensionDirectory),
    15000,
    "Provider extension install"
  );
  if (!extensionId) {
    throw new Error("Chromium did not return an extension ID.");
  }
  assert.equal(
    extensionId,
    EXPECTED_EXTENSION_ID,
    "The unpacked extension must retain its pinned production identity."
  );
  nativeHostRegistrySnapshot = snapshotNativeHostRegistrations();
  installNativeHost(extensionId);
  stage("testing website provider methods");
  const result = await withTimeout(
    runProviderChecks(browser, `${expectedOrigin}/acceptance`),
    30000,
    "Website provider checks"
  ).catch((error) => {
    stage(
      `mock broker methods before failure: ${
        requests.map((request) => request.method).join(", ") || "(none)"
      }`
    );
    throw error;
  });

  assert.deepEqual(result.initialAccounts, []);
  assert.deepEqual(result.connectedAccounts, [TEST_ADDRESS]);
  assert.equal(result.balance.balance, 42.5);
  assert.equal(result.balance.token, "CELL");
  assert.equal(result.signature.signature, "mock-ml-dsa-signature");
  assert.equal(result.transaction.transactionId, "mock-qsdm-transaction");
  assert.match(result.unsupportedError, /Unsupported QSDM wallet method/);
  assert.deepEqual(result.disconnected, { disconnected: true });
  assert.deepEqual(result.finalAccounts, []);
  assert.deepEqual(result.accountEvents, [[TEST_ADDRESS], []]);
  assert.deepEqual(
    requests.map((request) => request.method),
    [
      "qsdm_accounts",
      "qsdm_requestAccounts",
      "qsdm_getBalance",
      "qsdm_signMessage",
      "qsdm_sendTransaction",
      "qsdm_disconnect",
      "qsdm_accounts",
    ]
  );

  stage("testing qsdm.tech web wallet provider panel");
  const webWalletStart = requests.length;
  const webWallet = await withTimeout(
    runWebWalletChecks(browser, `${expectedOrigin}/wallet-provider-acceptance`),
    30000,
    "Web wallet provider checks"
  );
  assert.equal(webWallet.state, "Ready to connect");
  assert.equal(webWallet.address, "Not connected");
  assert.match(webWallet.notice, /disconnected/i);
  const webWalletMethods = requests
    .slice(webWalletStart)
    .map((request) => request.method);
  assert.equal(webWalletMethods[0], "qsdm_accounts");
  assert.ok(webWalletMethods.includes("qsdm_requestAccounts"));
  assert.ok(webWalletMethods.includes("qsdm_getBalance"));
  assert.ok(webWalletMethods.includes("qsdm_sendTransaction"));
  assert.equal(webWalletMethods.at(-1), "qsdm_disconnect");

  stage("testing QSDM Account wallet-link dashboard");
  const accountStart = requests.length;
  const accountResult = await withTimeout(
    runAccountDashboardChecks(browser, `${expectedOrigin}/account/`),
    30000,
    "QSDM Account dashboard checks"
  );
  assert.match(accountResult.identity, /t\*\*\*@example\.com/);
  assert.equal(accountResult.walletCount, "0");
  assert.equal(accountResult.balance, "0 CELL");
  assert.equal(accountResult.telegram, "@acceptance");
  assert.equal(
    accountResult.walletStatus,
    "Wallet unlinked. Your local wallet was not changed."
  );
  assert.equal(accountResult.sessions, "1 active browser session");
  assert.match(accountResult.transferStatus, /mock-qsdm-transaction/);
  assert.match(accountResult.deletionStatus, /Account data was deleted/);
  const accountMethods = requests
    .slice(accountStart)
    .map((request) => request.method);
  assert.ok(accountMethods.includes("qsdm_accounts"));
  assert.ok(accountMethods.includes("qsdm_requestAccounts"));
  assert.ok(accountMethods.includes("qsdm_signMessage"));
  assert.ok(accountMethods.includes("qsdm_getBalance"));
  assert.ok(accountMethods.includes("qsdm_sendTransaction"));

  stage("testing installed-extension onboarding handoff");
  const onboardingStart = requests.length;
  const onboardingResult = await withTimeout(
    runOnboardingChecks(
      browser,
      `${expectedOrigin}/wallet-start-acceptance`,
      extensionId
    ),
    30000,
    "Extension onboarding checks"
  );
  assert.ok(onboardingResult.onboardingUrl.includes("login=new"));
  assert.equal(
    onboardingResult.address,
    `${TEST_ADDRESS.slice(0, 12)}...${TEST_ADDRESS.slice(-10)}`
  );
  assert.deepEqual(
    requests.slice(onboardingStart).map((request) => request.method),
    [
      "qsdm_ping",
      "qsdm_getWalletInfo",
      "qsdm_getBalance",
      "qsdm_requestAccounts",
    ]
  );

  stage("testing extension wallet dashboard");
  const dashboardStart = requests.length;
  const walletDashboard = await withTimeout(
    runExtensionWalletDashboardChecks(browser, extensionId),
    30000,
    "Extension wallet dashboard checks"
  );
  assert.equal(walletDashboard.address, TEST_ADDRESS);
  assert.equal(walletDashboard.balance, "42.5 CELL");
  assert.match(walletDashboard.status, /mock-qsdm-transaction/);
  assert.deepEqual(
    requests.slice(dashboardStart).map((request) => request.method),
    [
      "qsdm_ping",
      "qsdm_getWalletInfo",
      "qsdm_getBalance",
      "qsdm_sendTransaction",
      "qsdm_ping",
      "qsdm_getWalletInfo",
      "qsdm_getBalance",
    ]
  );

  stage("testing extension popup");
  const popupStart = requests.length;
  const popup = await withTimeout(browser.newPage(), 10000, "Popup creation");
  popup.on("console", (message) =>
    stage(`popup console ${message.type()}: ${message.text()}`)
  );
  popup.on("pageerror", (error) => stage(`popup page error: ${error.message}`));
  await withTimeout(
    popup.goto(`chrome-extension://${extensionId}/popup.html`),
    10000,
    "Popup navigation"
  );
  await withTimeout(
    popup.waitForFunction(
      () =>
        document.querySelector("#hive-status")?.textContent === "Wallet ready",
      { timeout: 10000 }
    ),
    12000,
    "Popup connection check"
  ).catch(async (error) => {
    const popupState = await popup.evaluate(() => ({
      status: document.querySelector("#hive-status")?.textContent,
      address: document.querySelector("#wallet-address")?.textContent,
      notice: document.querySelector("#notice")?.textContent,
    }));
    stage(`popup state before failure: ${JSON.stringify(popupState)}`);
    throw error;
  });
  const popupAddress = await popup.$eval(
    "#wallet-address",
    (element) => element.textContent
  );
  assert.equal(
    popupAddress,
    `${TEST_ADDRESS.slice(0, 10)}...${TEST_ADDRESS.slice(-8)}`
  );
  assert.equal(
    await popup.$eval("#site-name", (element) => element.textContent),
    "Unavailable on this page"
  );
  assert.equal(
    await popup.$eval("#wallet-balance", (element) => element.textContent),
    "42.5 CELL"
  );

  const accountTargetPromise = browser.waitForTarget(
    (target) => target.url() === "https://qsdm.tech/account/",
    { timeout: 15000 }
  );
  await popup.click("#open-account");
  const accountTarget = await accountTargetPromise;
  const accountPage = await accountTarget.page();
  await accountPage?.close();
  if (screenshotDirectory) {
    await popup.setViewport({ width: 390, height: 720 });
    await popup.screenshot({
      path: path.join(screenshotDirectory, "qsdm-wallet-popup.png"),
      fullPage: true,
    });
  }

  const popupMethods = requests
    .slice(popupStart)
    .map((request) => request.method);
  assert.deepEqual(popupMethods, [
    "qsdm_ping",
    "qsdm_getWalletInfo",
    "qsdm_getBalance",
  ]);
  const methods = requests.map((request) => request.method);

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
  stage("cleaning up acceptance processes");
  if (browser) await closeBrowser(browser);
  if (nativeHostRegistrySnapshot) {
    restoreNativeHostRegistrations(nativeHostRegistrySnapshot);
  }
  await withTimeout(closeServer(), 5000, "Mock broker shutdown").catch(
    () => undefined
  );
  if (!keepProfile) {
    removeTemporaryDirectory();
  } else {
    console.log(`Acceptance profile retained at ${temporaryDirectory}`);
  }
}
