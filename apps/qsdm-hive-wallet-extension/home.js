(() => {
  "use strict";

  const NATIVE_HOST = "tech.qsdm.hive_wallet";
  const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";
  const INTERNAL_ORIGIN = "qsdm-extension://wallet-popup";
  const HIVE_WALLET_URL = "qsdm-hive://open?route=%2Fsettings%2Fwallet";
  const ACCOUNT_URL = "https://qsdm.tech/account/";

  const useHiveButton = document.getElementById("use-hive");
  const openHiveButton = document.getElementById("open-hive");
  const stateLabel = document.getElementById("state-label");
  const addressElement = document.getElementById("wallet-address");
  const siteElement = document.getElementById("requesting-site");
  const noticeElement = document.getElementById("notice");
  const onboardingView = document.getElementById("onboarding-view");
  const walletDashboard = document.getElementById("wallet-dashboard");
  const dashboardAddress = document.getElementById("dashboard-address");
  const dashboardBalance = document.getElementById("dashboard-balance");
  const balanceState = document.getElementById("balance-state");
  const copyAddressButton = document.getElementById("copy-address");
  const refreshBalanceButton = document.getElementById("refresh-balance");
  const sendForm = document.getElementById("send-form");
  const sendFields = document.getElementById("send-fields");
  const sendRecipient = document.getElementById("send-recipient");
  const sendAmount = document.getElementById("send-amount");
  const sendCellButton = document.getElementById("send-cell");
  const transferStatus = document.getElementById("transfer-status");
  const openAccountButton = document.getElementById("open-account");
  const dashboardOpenHiveButton = document.getElementById(
    "dashboard-open-hive"
  );

  let walletAddress = "";
  let walletBalance = null;
  const walletMode = window.location.hash.startsWith("#/wallet");

  onboardingView.hidden = walletMode;
  walletDashboard.hidden = !walletMode;
  if (walletMode) document.title = "QSDM Wallet Dashboard";

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

  const setTransferStatus = (message, state = "info") => {
    transferStatus.textContent = message || "";
    transferStatus.dataset.state = state;
  };

  const shortenAddress = (address) =>
    address && address.length > 24
      ? `${address.slice(0, 12)}...${address.slice(-10)}`
      : address || "";

  const formatBalance = (value) =>
    Number.isFinite(Number(value))
      ? `${Number(value).toLocaleString(undefined, {
          maximumFractionDigits: 8,
        })} CELL`
      : "Unavailable";

  const transactionReference = (value) =>
    value?.transaction_id ||
    value?.transactionId ||
    value?.tx_id ||
    value?.id ||
    "accepted by QSDM Core";

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
    const previousAddress = walletAddress;
    const previousBalance = walletBalance;
    const hasExistingWallet = Boolean(previousAddress);
    useHiveButton.disabled = true;
    stateLabel.textContent = "Checking QSDM Hive...";
    if (!hasExistingWallet) {
      addressElement.textContent = "";
      walletAddress = "";
      walletBalance = null;
      dashboardAddress.textContent = "Checking...";
      dashboardAddress.title = "";
      dashboardBalance.textContent = "Unavailable";
      copyAddressButton.disabled = true;
    } else {
      walletBalance = previousBalance;
      dashboardAddress.textContent = previousAddress;
      dashboardAddress.title = previousAddress;
      dashboardBalance.textContent = formatBalance(previousBalance);
      copyAddressButton.disabled = false;
    }
    balanceState.textContent = hasExistingWallet
      ? "Refreshing from Hive..."
      : "Waiting for Hive";
    sendFields.disabled = true;
    if (requestingOrigin) {
      siteElement.textContent = `Requested by ${new URL(requestingOrigin).hostname}`;
    } else {
      siteElement.textContent = "Opened directly from the QSDM Wallet extension";
    }

    const ping = await requestInternal("qsdm_ping", undefined, 5000);
    if (!ping?.ok) {
      stateLabel.textContent = "QSDM Hive is not running";
      setNotice("Start Hive, then select Open wallet management.", "error");
      dashboardAddress.textContent = "QSDM Hive is not running";
      balanceState.textContent = "Start Hive to use this wallet";
      if (walletMode) {
        setTransferStatus(
          "Start QSDM Hive to check balances or send CELL.",
          "error"
        );
      }
      return;
    }

    const info = await requestInternal("qsdm_getWalletInfo", undefined, 5000);
    walletAddress =
      info?.ok && info.result?.ready ? info.result.address || "" : "";
    if (!walletAddress) {
      stateLabel.textContent = "Wallet setup is needed";
      setNotice("Create, import, or unlock your wallet in QSDM Hive.", "error");
      dashboardAddress.textContent = "Wallet setup is needed";
      balanceState.textContent = "Create, import, or unlock a wallet in Hive";
      return;
    }

    stateLabel.textContent = "Active Hive wallet";
    addressElement.textContent = shortenAddress(walletAddress);
    addressElement.title = walletAddress;
    dashboardAddress.textContent = walletAddress;
    dashboardAddress.title = walletAddress;
    copyAddressButton.disabled = false;

    const balance = await requestInternal("qsdm_getBalance", undefined, 8000);
    walletBalance = balance?.ok ? Number(balance.result?.balance) : null;
    if (!Number.isFinite(walletBalance)) walletBalance = null;
    dashboardBalance.textContent = formatBalance(walletBalance);
    balanceState.textContent = balance?.ok
      ? "Live CELL balance"
      : balance?.error || "Balance is temporarily unavailable";
    sendFields.disabled = !balance?.ok;
    setNotice(
      requestingOrigin
        ? "Use this wallet, then approve the website in Hive."
        : "Your wallet is ready. Open a supported website to connect it.",
      "success"
    );
    useHiveButton.disabled = false;
  };

  const openAccountLogin = async (loginMethod) => {
    const loginLabel = loginMethod === "telegram" ? "Telegram" : "email";
    setNotice(`Opening secure ${loginLabel} login...`);
    const target = new URL(ACCOUNT_URL);
    target.searchParams.set("login", loginMethod);
    target.searchParams.set("source", "extension");
    await chrome.tabs.create({ url: target.toString() });
  };

  const openHiveWallet = async () => {
    const response = await requestInternal("qsdm_openWallet", undefined, 5000);
    if (!response?.ok) await chrome.tabs.create({ url: HIVE_WALLET_URL });
  };

  document
    .getElementById("telegram-login")
    .addEventListener("click", () => void openAccountLogin("telegram"));

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
      setNotice(
        response?.error || "The website connection was not approved.",
        "error"
      );
      useHiveButton.disabled = false;
      return;
    }

    const accounts = Array.isArray(response.result) ? response.result : [];
    if (!accounts.includes(walletAddress)) {
      setNotice(
        "Hive returned a different active wallet. Refresh and try again.",
        "error"
      );
      useHiveButton.disabled = false;
      return;
    }
    setNotice("Wallet connected. Return to the requesting website.", "success");
    useHiveButton.textContent = "Wallet Connected";
  });

  openHiveButton.addEventListener("click", async () => {
    setNotice("Opening QSDM Hive...");
    await openHiveWallet();
    setTimeout(() => void refreshWallet(), 800);
  });

  refreshBalanceButton.addEventListener("click", () => void refreshWallet());

  copyAddressButton.addEventListener("click", async () => {
    if (!walletAddress) return;
    try {
      await navigator.clipboard.writeText(walletAddress);
      setTransferStatus("Wallet address copied.", "success");
    } catch {
      setTransferStatus(
        "The browser could not copy the wallet address.",
        "error"
      );
    }
  });

  sendForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const recipient = sendRecipient.value.trim();
    const amountText = sendAmount.value.trim();
    if (!/^[a-zA-Z0-9]{32,128}$/.test(recipient)) {
      setTransferStatus(
        "Enter a valid QSDM recipient wallet address.",
        "error"
      );
      sendRecipient.focus();
      return;
    }
    if (!/^(?:0|[1-9]\d*)(?:\.\d{1,8})?$/.test(amountText)) {
      setTransferStatus(
        "Enter an amount greater than zero with no more than 8 decimals.",
        "error"
      );
      sendAmount.focus();
      return;
    }
    const amount = Number(amountText);
    if (!Number.isFinite(amount) || amount <= 0) {
      setTransferStatus("Enter an amount greater than zero.", "error");
      sendAmount.focus();
      return;
    }
    if (walletBalance !== null && amount > walletBalance) {
      setTransferStatus(
        "The amount is greater than the available balance.",
        "error"
      );
      sendAmount.focus();
      return;
    }

    sendFields.disabled = true;
    sendCellButton.textContent = "Waiting for Hive approval...";
    setTransferStatus(
      `Review ${amountText} CELL to ${shortenAddress(recipient)} in QSDM Hive.`
    );
    const response = await requestInternal("qsdm_sendTransaction", {
      recipient,
      amount,
    });
    sendCellButton.textContent = "Review in Hive";
    if (!response?.ok) {
      setTransferStatus(
        response?.error || "The transfer was not completed.",
        "error"
      );
      sendFields.disabled = false;
      return;
    }

    sendForm.reset();
    setTransferStatus(
      `Transfer ${transactionReference(response.result)}.`,
      "success"
    );
    await refreshWallet();
  });

  openAccountButton.addEventListener("click", async () => {
    await chrome.tabs.create({ url: ACCOUNT_URL });
  });

  dashboardOpenHiveButton.addEventListener("click", async () => {
    await openHiveWallet();
  });

  void refreshWallet();
})();
