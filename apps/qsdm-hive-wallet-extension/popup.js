const statusElement = document.getElementById("hive-status");
const addressElement = document.getElementById("wallet-address");
const siteNameElement = document.getElementById("site-name");
const siteStateElement = document.getElementById("site-state");
const noticeElement = document.getElementById("notice");
const connectButton = document.getElementById("connect-site");
const openWalletButton = document.getElementById("open-wallet");

const NATIVE_HOST = "tech.qsdm.hive_wallet";
const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";
const INTERNAL_ORIGIN = "qsdm-extension://wallet-popup";
const HIVE_WALLET_URL = "qsdm-hive://open?route=%2Fsettings%2Fwallet";

let activeOrigin = "";
let activeSiteName = "";
let siteConnected = false;

const sleep = (milliseconds) =>
  new Promise((resolve) => setTimeout(resolve, milliseconds));

const normalizeWebOrigin = (rawUrl) => {
  const parsed = new URL(rawUrl);
  const localHttp =
    parsed.protocol === "http:" &&
    ["localhost", "127.0.0.1", "::1"].includes(parsed.hostname);
  if (parsed.protocol !== "https:" && !localHttp) {
    throw new Error("Open an HTTPS website to connect your QSDM wallet");
  }
  return parsed.origin;
};

const sendNative = (origin, method, params, timeoutMs = 120000) =>
  new Promise((resolve) => {
    let settled = false;
    let port;
    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      port?.disconnect();
      resolve({ ok: false, error: "QSDM Wallet did not answer" });
    }, timeoutMs);
    const finish = (response) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      resolve(response || { ok: false, error: "QSDM Wallet did not answer" });
      port?.disconnect();
    };
    try {
      port = chrome.runtime.connectNative(NATIVE_HOST);
      port.onMessage.addListener((response) => finish(response));
      port.onDisconnect.addListener(() => {
        const runtimeError = chrome.runtime.lastError;
        finish({
          ok: false,
          error: runtimeError?.message || "Open QSDM Hive to use your wallet",
        });
      });
      port.postMessage({
        version: PROVIDER_VERSION,
        id: crypto.randomUUID(),
        origin,
        method,
        params,
      });
    } catch (error) {
      finish({
        ok: false,
        error: error instanceof Error ? error.message : String(error),
      });
    }
  });

const requestInternal = (method, params, timeoutMs) =>
  sendNative(INTERNAL_ORIGIN, method, params, timeoutMs);

const setNotice = (message) => {
  noticeElement.textContent = message || "";
};

const formatAddress = (address) =>
  address && address.length > 22
    ? `${address.slice(0, 10)}...${address.slice(-8)}`
    : address || "Wallet setup needed";

const setSiteConnected = (connected) => {
  siteConnected = connected;
  siteStateElement.textContent = connected ? "Connected" : "Not connected";
  siteStateElement.classList.toggle("connected", connected);
  connectButton.textContent = connected
    ? "Disconnect Current Site"
    : "Connect Current Site";
};

const getActiveSite = async () => {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  activeOrigin = normalizeWebOrigin(tab?.url || "");
  activeSiteName = new URL(activeOrigin).hostname;
  siteNameElement.textContent = activeSiteName;
  siteNameElement.title = activeOrigin;
};

const pingWithRetry = async (attempts = 4) => {
  let response;
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    response = await requestInternal("qsdm_ping", undefined, 5000);
    if (response?.ok) return response;
    if (attempt + 1 < attempts) await sleep(500);
  }
  return response;
};

const refresh = async () => {
  connectButton.disabled = true;
  setSiteConnected(false);
  try {
    await getActiveSite();
  } catch (error) {
    activeOrigin = "";
    activeSiteName = "";
    siteNameElement.textContent = "Unavailable on this page";
    siteNameElement.title = "";
  }

  const ping = await pingWithRetry();
  if (!ping?.ok) {
    statusElement.textContent = "Open Hive to continue";
    addressElement.textContent = "Wallet unavailable";
    addressElement.title = "";
    setNotice("Start QSDM Hive, then this wallet reconnects automatically.");
    return;
  }

  const info = await requestInternal("qsdm_getWalletInfo", undefined, 5000);
  if (!info?.ok) {
    statusElement.textContent = "Wallet unavailable";
    setNotice(info?.error || "Open QSDM Hive to finish wallet setup.");
    return;
  }

  const walletAddress = info.result?.address || "";
  const walletReady = Boolean(info.result?.ready && walletAddress);
  statusElement.textContent = walletReady ? "Wallet ready" : "Wallet locked";
  addressElement.textContent = formatAddress(walletAddress);
  addressElement.title = walletAddress;
  if (!walletReady) {
    setNotice("Open Hive to create, import, or unlock your QSDM wallet.");
    return;
  }

  if (!activeOrigin) {
    setNotice("Open an HTTPS website to connect it to this wallet.");
    return;
  }

  const accounts = await sendNative(
    activeOrigin,
    "qsdm_accounts",
    undefined,
    5000
  );
  const connected =
    accounts?.ok &&
    Array.isArray(accounts.result) &&
    accounts.result.some(
      (account) =>
        typeof account === "string" &&
        account.toLowerCase() === walletAddress.toLowerCase()
    );
  setSiteConnected(connected);
  connectButton.disabled = false;
  setNotice(
    connected
      ? `${activeSiteName} can request actions from this wallet.`
      : "Connect once. Hive will remember this site until you disconnect it."
  );
};

connectButton.addEventListener("click", async () => {
  if (!activeOrigin) return;
  connectButton.disabled = true;
  setNotice(
    siteConnected
      ? `Disconnecting ${activeSiteName}...`
      : "Approve this site in QSDM Hive."
  );
  const response = await sendNative(
    activeOrigin,
    siteConnected ? "qsdm_disconnect" : "qsdm_requestAccounts",
    undefined
  );
  if (!response?.ok) {
    setNotice(response?.error || "The wallet request was not approved.");
    connectButton.disabled = false;
    return;
  }
  await refresh();
});

openWalletButton.addEventListener("click", async () => {
  setNotice("Opening QSDM Hive...");
  const response = await requestInternal("qsdm_openWallet", undefined, 5000);
  if (!response?.ok) {
    try {
      await chrome.tabs.create({ url: HIVE_WALLET_URL });
    } catch {
      setNotice("Start QSDM Hive from your applications menu.");
      return;
    }
  }
  await sleep(750);
  await refresh();
});

refresh().catch((error) => setNotice(error.message));
