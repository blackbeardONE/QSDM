const NATIVE_HOST = "tech.qsdm.hive_wallet";
const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";
const ONBOARDING_METHOD = "qsdm_openOnboarding";
let onboardingTabId = null;

// The wallet pages (popup.html) read these public, read-only QSDM API paths through
// here, and nothing else: no credentials, no writes, no other host. Websites cannot
// use this: only pages of this extension are answered.
const QSDM_API_BASE = "https://api.qsdm.tech/api/v1";
const WALLET_API_SOURCE = "qsdm-wallet-api";
const WALLET_API_PATHS = [
  /^\/status$/,
  /^\/receipts\/[A-Za-z0-9._:-]{1,160}$/,
  /^\/receipts\?from=\d{1,12}&to=\d{1,12}&limit=\d{1,3}$/,
];
const WALLET_API_MAX_BYTES = 1048576;

const isWalletPage = (sender) =>
  sender?.id === chrome.runtime.id &&
  typeof sender.url === "string" &&
  sender.url.startsWith(chrome.runtime.getURL(""));

const readPublicApi = async (path) => {
  if (
    typeof path !== "string" ||
    !WALLET_API_PATHS.some((pattern) => pattern.test(path))
  ) {
    return { ok: false, status: 0 };
  }
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 8000);
  try {
    const response = await fetch(`${QSDM_API_BASE}${path}`, {
      method: "GET",
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      signal: controller.signal,
      headers: { Accept: "application/json" },
    });
    const text = await response.text();
    let json = null;
    if (text && text.length <= WALLET_API_MAX_BYTES) {
      try {
        json = JSON.parse(text);
      } catch {
        json = null;
      }
    }
    return { ok: response.ok, status: response.status, json };
  } catch {
    // Offline, or the API does not answer this extension's origin yet.
    return { ok: false, status: 0 };
  } finally {
    clearTimeout(timer);
  }
};

const normalizeOnboardingMode = (value) =>
  value === "existing" ? "existing" : "new";

const openOnboarding = async (origin, params) => {
  const login = normalizeOnboardingMode(params?.login);
  const page = chrome.runtime.getURL("home.html");
  const route = new URLSearchParams({ login, origin }).toString();
  const url = `${page}#/onboarding/welcome?${route}`;
  let reused = false;
  if (Number.isInteger(onboardingTabId)) {
    try {
      await chrome.tabs.get(onboardingTabId);
      await chrome.tabs.update(onboardingTabId, { url, active: true });
      reused = true;
    } catch {
      onboardingTabId = null;
    }
  }
  if (!reused) {
    const tab = await chrome.tabs.create({ url, active: true });
    onboardingTabId = Number.isInteger(tab?.id) ? tab.id : null;
  }
  return {
    ok: true,
    result: { opened: true, reused },
  };
};

chrome.tabs.onRemoved.addListener((tabId) => {
  if (tabId === onboardingTabId) onboardingTabId = null;
});

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

// ---------------------------------------------------------------- site approvals
// A website's connection request, transfer request or message signature first
// opens this extension's approval window (approve.html), which shows the real
// origin and exactly what the site asks for. Cancel, or closing the window,
// refuses the request without disturbing Hive. Continue hands the same request
// to QSDM Hive, which shows it again and decides: Hive keeps the key and the
// site permissions, and its approval stays the only one that signs or connects.
const INTERNAL_ORIGIN = "qsdm-extension://wallet-popup";
const APPROVAL_METHODS = new Set([
  "qsdm_requestAccounts",
  "qsdm_sendTransaction",
  "qsdm_signMessage",
]);
const APPROVAL_PORT = "qsdm-approval:";
const CANCELLED = {
  ok: false,
  code: 4001,
  error: "The request was cancelled in the QSDM Wallet window.",
};
const approvals = new Map();

const shownParams = (method, params) => {
  if (method === "qsdm_sendTransaction") {
    return {
      recipient: String(params?.recipient ?? "").slice(0, 200),
      amount: Number(params?.amount),
    };
  }
  if (method === "qsdm_signMessage") {
    return { message: String(params?.message ?? "").slice(0, 16384) };
  }
  return {};
};

const settleApproval = (approval, response) => {
  if (approval.settled) return;
  approval.settled = true;
  approvals.delete(approval.id);
  approval.resolve(response);
};

const approveThenSend = async (origin, method, params) => {
  // Hive closed or locked, or a site that is not connected yet asking to sign
  // or send: Hive answers these itself, as before, without a window.
  const ping = await sendNative(INTERNAL_ORIGIN, "qsdm_ping", undefined, 5000);
  if (!ping?.ok) return sendNative(origin, method, params);
  const info = await sendNative(
    INTERNAL_ORIGIN,
    "qsdm_getWalletInfo",
    undefined,
    5000
  );
  const address = info?.ok && info.result?.ready ? info.result.address : "";
  if (!address) return sendNative(origin, method, params);
  const accounts = await sendNative(origin, "qsdm_accounts", undefined, 5000);
  const connected =
    accounts?.ok && Array.isArray(accounts.result) && accounts.result.length > 0;
  if (method === "qsdm_requestAccounts" && connected) {
    return sendNative(origin, method, params); // remembered by Hive: no prompt
  }
  if (method !== "qsdm_requestAccounts" && !connected) {
    return sendNative(origin, method, params); // Hive refuses it
  }

  for (const waiting of approvals.values()) {
    if (waiting.origin !== origin) continue;
    if (Number.isInteger(waiting.windowId)) {
      chrome.windows.update(waiting.windowId, { focused: true }, () => {
        void chrome.runtime.lastError;
      });
    }
    return {
      ok: false,
      error:
        "Another request from this site is waiting in the QSDM Wallet window.",
    };
  }

  const status = await readPublicApi("/status");
  return new Promise((resolve) => {
    const id = crypto.randomUUID();
    const approval = {
      id,
      origin,
      method,
      params,
      address,
      network:
        status.ok && typeof status.json?.network === "string"
          ? status.json.network.slice(0, 40)
          : "",
      resolve,
      settled: false,
      decided: false,
      windowId: null,
    };
    approvals.set(id, approval);
    chrome.windows.create(
      {
        url: chrome.runtime.getURL(`approve.html?id=${encodeURIComponent(id)}`),
        type: "popup",
        width: 400,
        height: 640,
        focused: true,
      },
      (created) => {
        if (chrome.runtime.lastError || !created) {
          settleApproval(approval, {
            ok: false,
            error: "QSDM Wallet could not open its approval window.",
          });
          return;
        }
        approval.windowId = created.id;
      }
    );
  });
};

chrome.runtime.onConnect.addListener((port) => {
  if (!port.name.startsWith(APPROVAL_PORT)) return;
  const approval = approvals.get(port.name.slice(APPROVAL_PORT.length));
  if (!isWalletPage(port.sender) || !approval || approval.port) {
    port.postMessage({ type: "gone" });
    port.disconnect();
    return;
  }
  approval.port = port;
  const tell = (message) => {
    try {
      port.postMessage(message);
    } catch {
      // the window is gone; the request carries on or was cancelled
    }
  };
  tell({
    type: "request",
    origin: approval.origin,
    method: approval.method,
    params: shownParams(approval.method, approval.params),
    address: approval.address,
    network: approval.network,
  });
  port.onMessage.addListener(async (message) => {
    if (approval.decided || approval.settled) return;
    if (message?.type === "cancel") {
      approval.decided = true;
      settleApproval(approval, CANCELLED);
      return;
    }
    if (message?.type !== "approve") return; // "ping" keeps this worker awake
    approval.decided = true;
    tell({ type: "hive" });
    const response = await sendNative(
      approval.origin,
      approval.method,
      approval.params
    );
    tell({
      type: "done",
      ok: Boolean(response?.ok),
      error: response?.ok ? "" : response?.error || "QSDM Hive did not approve it",
      txId: String(
        response?.result?.transaction_id ||
          response?.result?.transactionId ||
          ""
      ).slice(0, 200),
    });
    settleApproval(approval, response);
  });
  port.onDisconnect.addListener(() => {
    approval.port = null;
    if (!approval.decided) {
      approval.decided = true;
      settleApproval(approval, CANCELLED);
    }
  });
});

const handleMessage = async (message, sender) => {
  if (message?.source === "qsdm-hive-content") {
    const origin = normalizeWebOrigin(sender.tab?.url || sender.url || "");
    if (message.method === ONBOARDING_METHOD) {
      return openOnboarding(origin, message.params);
    }
    if (APPROVAL_METHODS.has(message.method)) {
      return approveThenSend(origin, message.method, message.params);
    }
    return sendNative(origin, message.method, message.params);
  }

  return { ok: false, error: "Invalid QSDM extension request" };
};

const sendContentResponse = (sender, id, response) => {
  if (!Number.isInteger(sender.tab?.id) || typeof id !== "string") return;
  chrome.tabs.sendMessage(
    sender.tab.id,
    {
      source: "qsdm-hive-background-response",
      id,
      response,
    },
    { frameId: sender.frameId || 0 },
    () => void chrome.runtime.lastError
  );
};

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message?.source === WALLET_API_SOURCE) {
    if (!isWalletPage(sender)) {
      sendResponse({ ok: false, status: 0 });
      return false;
    }
    readPublicApi(message.path).then(sendResponse);
    return true;
  }

  if (message?.source === "qsdm-hive-content") {
    sendResponse({ accepted: true });
    handleMessage(message, sender)
      .then((response) => sendContentResponse(sender, message.id, response))
      .catch((error) =>
        sendContentResponse(sender, message.id, {
          ok: false,
          error: error instanceof Error ? error.message : String(error),
        })
      );
    return false;
  }

  handleMessage(message, sender)
    .then((response) => {
      sendResponse(response);
    })
    .catch((error) =>
      sendResponse({
        ok: false,
        error: error instanceof Error ? error.message : String(error),
      })
    );
  return true;
});
