const statusElement = document.getElementById("hive-status");
const addressElement = document.getElementById("wallet-address");
const noticeElement = document.getElementById("notice");
const connectButton = document.getElementById("connect-site");
const openWalletButton = document.getElementById("open-wallet");

const NATIVE_HOST = "tech.qsdm.hive_wallet";
const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";
const INTERNAL_ORIGIN = "qsdm-extension://wallet-popup";

const normalizeWebOrigin = (rawUrl) => {
  const parsed = new URL(rawUrl);
  const localHttp =
    parsed.protocol === "http:" &&
    ["localhost", "127.0.0.1", "::1"].includes(parsed.hostname);
  if (parsed.protocol !== "https:" && !localHttp) {
    throw new Error("QSDM wallet connections require HTTPS");
  }
  return parsed.origin;
};

const sendNative = (origin, method, params) =>
  new Promise((resolve) => {
    let settled = false;
    let port;
    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      port?.disconnect();
      resolve({ ok: false, error: "QSDM Hive wallet request timed out" });
    }, 120000);
    const finish = (response) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      resolve(response || { ok: false, error: "QSDM Hive did not answer" });
      port?.disconnect();
    };
    try {
      port = chrome.runtime.connectNative(NATIVE_HOST);
      port.onMessage.addListener((response) => finish(response));
      port.onDisconnect.addListener(() => {
        const runtimeError = chrome.runtime.lastError;
        finish({
          ok: false,
          error:
            runtimeError?.message ||
            "QSDM Hive native wallet bridge disconnected",
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

const request = async (method, params) => {
  if (method === "qsdm_connectActiveTab") {
    const [tab] = await chrome.tabs.query({
      active: true,
      currentWindow: true,
    });
    return sendNative(
      normalizeWebOrigin(tab?.url || ""),
      "qsdm_requestAccounts",
      params
    );
  }
  return sendNative(INTERNAL_ORIGIN, method, params);
};

const setNotice = (message) => {
  noticeElement.textContent = message || "";
};

const refresh = async () => {
  const ping = await request("qsdm_ping");
  if (!ping?.ok) {
    statusElement.textContent = "Hive is not running";
    addressElement.textContent = "Start QSDM Hive";
    connectButton.disabled = true;
    setNotice(ping?.error || "Native wallet bridge is unavailable.");
    return;
  }

  const info = await request("qsdm_getWalletInfo");
  statusElement.textContent = "Hive connected";
  addressElement.textContent = info?.result?.address || "Wallet setup needed";
  addressElement.title = info?.result?.address || "";
  connectButton.disabled = !info?.result?.ready;
  setNotice(
    info?.result?.ready ? "" : "Open Hive to create or import a wallet."
  );
};

connectButton.addEventListener("click", async () => {
  connectButton.disabled = true;
  setNotice("Approve the connection in QSDM Hive.");
  const response = await request("qsdm_connectActiveTab");
  setNotice(
    response?.ok
      ? "This site is connected."
      : response?.error || "Connection was not approved."
  );
  connectButton.disabled = false;
});

openWalletButton.addEventListener("click", async () => {
  const response = await request("qsdm_openWallet");
  if (!response?.ok) setNotice(response?.error || "Could not open QSDM Hive.");
});

refresh().catch((error) => setNotice(error.message));
