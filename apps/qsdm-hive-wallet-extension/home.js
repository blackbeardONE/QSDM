(() => {
  "use strict";

  const NATIVE_HOST = "tech.qsdm.hive_wallet";
  const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";
  const INTERNAL_ORIGIN = "qsdm-extension://wallet-popup";
  const HIVE_WALLET_URL = "qsdm-hive://open?route=%2Fsettings%2Fwallet";
  const SOCIAL_AUTH_NOTICE =
    "Verified account login is not enabled yet. No Telegram or email data was collected.";

  const useHiveButton = document.getElementById("use-hive");
  const openHiveButton = document.getElementById("open-hive");
  const stateLabel = document.getElementById("state-label");
  const addressElement = document.getElementById("wallet-address");
  const siteElement = document.getElementById("requesting-site");
  const noticeElement = document.getElementById("notice");

  let walletAddress = "";

  const parameters = (() => {
    const queryIndex = window.location.hash.indexOf("?");
    return new URLSearchParams(
      queryIndex >= 0 ? window.location.hash.slice(queryIndex + 1) : ""
    );
  })();

  const normalizeOrigin = (value) => {
    if (!value) return "";
    const parsed = new URL(value);
    const localHttp =
      parsed.protocol === "http:" &&
      ["localhost", "127.0.0.1", "::1"].includes(parsed.hostname);
    if (parsed.protocol !== "https:" && !localHttp) return "";
    return parsed.origin;
  };

  const requestingOrigin = (() => {
    try {
      return normalizeOrigin(parameters.get("origin"));
    } catch {
      return "";
    }
  })();

  const setNotice = (message, state = "info") => {
    noticeElement.textContent = message || "";
    noticeElement.dataset.state = state;
  };

  const shortenAddress = (address) =>
    address && address.length > 24
      ? `${address.slice(0, 12)}...${address.slice(-10)}`
      : address || "";

  const sendNative = (origin, method, params, timeoutMs = 120000) =>
    new Promise((resolve) => {
      let settled = false;
      let port;
      const timeout = setTimeout(() => {
        if (settled) return;
        settled = true;
        port?.disconnect();
        resolve({ ok: false, error: "QSDM Hive did not answer" });
      }, timeoutMs);
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
            error: runtimeError?.message || "Start QSDM Hive to continue",
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

  const refreshWallet = async () => {
    useHiveButton.disabled = true;
    stateLabel.textContent = "Checking QSDM Hive...";
    addressElement.textContent = "";
    if (requestingOrigin) {
      siteElement.textContent = `Requested by ${new URL(requestingOrigin).hostname}`;
    } else {
      siteElement.textContent = "Opened directly from the QSDM Wallet extension";
    }

    const ping = await requestInternal("qsdm_ping", undefined, 5000);
    if (!ping?.ok) {
      stateLabel.textContent = "QSDM Hive is not running";
      setNotice("Start Hive, then select Open wallet management.", "error");
      return;
    }

    const info = await requestInternal("qsdm_getWalletInfo", undefined, 5000);
    walletAddress = info?.ok && info.result?.ready ? info.result.address || "" : "";
    if (!walletAddress) {
      stateLabel.textContent = "Wallet setup is needed";
      setNotice("Create, import, or unlock your wallet in QSDM Hive.", "error");
      return;
    }

    stateLabel.textContent = "Active Hive wallet";
    addressElement.textContent = shortenAddress(walletAddress);
    addressElement.title = walletAddress;
    useHiveButton.disabled = false;
    setNotice(
      requestingOrigin
        ? "Use this wallet, then approve the website in Hive."
        : "Your wallet is ready. Open a supported website to connect it.",
      "success"
    );
  };

  const explainUnavailableSocialLogin = () => {
    setNotice(SOCIAL_AUTH_NOTICE, "error");
  };

  document
    .getElementById("telegram-login")
    .addEventListener("click", explainUnavailableSocialLogin);
  document
    .getElementById("email-login")
    .addEventListener("click", explainUnavailableSocialLogin);

  useHiveButton.addEventListener("click", async () => {
    if (!walletAddress) return;
    if (!requestingOrigin) {
      setNotice("Open a supported website and select Connect QSDM Wallet.");
      return;
    }

    useHiveButton.disabled = true;
    setNotice("Approve this website in QSDM Hive.");
    const response = await sendNative(
      requestingOrigin,
      "qsdm_requestAccounts",
      undefined
    );
    if (!response?.ok) {
      setNotice(response?.error || "The website connection was not approved.", "error");
      useHiveButton.disabled = false;
      return;
    }

    const accounts = Array.isArray(response.result) ? response.result : [];
    if (!accounts.includes(walletAddress)) {
      setNotice("Hive returned a different active wallet. Refresh and try again.", "error");
      useHiveButton.disabled = false;
      return;
    }
    setNotice("Wallet connected. Return to the requesting website.", "success");
    useHiveButton.textContent = "Wallet Connected";
  });

  openHiveButton.addEventListener("click", async () => {
    setNotice("Opening QSDM Hive...");
    const response = await requestInternal("qsdm_openWallet", undefined, 5000);
    if (!response?.ok) {
      await chrome.tabs.create({ url: HIVE_WALLET_URL });
    }
    setTimeout(() => void refreshWallet(), 800);
  });

  void refreshWallet();
})();
