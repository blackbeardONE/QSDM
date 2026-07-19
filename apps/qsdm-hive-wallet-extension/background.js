const NATIVE_HOST = "tech.qsdm.hive_wallet";
const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";

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

const handleMessage = async (message, sender) => {
  if (message?.source === "qsdm-hive-content") {
    const origin = normalizeWebOrigin(sender.tab?.url || sender.url || "");
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
