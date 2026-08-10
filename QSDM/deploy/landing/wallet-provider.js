// Connects qsdm.tech/wallet to the QSDM Hive browser provider.
// Wallet secrets stay in Hive; this page receives only approved public data.
(function () {
  "use strict";

  const PANEL_ID = "qsdm-hive-provider-panel";
  const ONBOARDING_URL = "/wallet-start.html?login=new";
  const ADDRESS_PATTERN = /^[a-zA-Z0-9]{32,128}$/;
  const AMOUNT_PATTERN = /^(?:0|[1-9]\d*)(?:\.\d{1,8})?$/;
  let provider = null;
  let connectedAddress = "";
  let detectionTimer = null;

  const element = (id) => document.getElementById(id);

  const setText = (id, value) => {
    const target = element(id);
    if (target) target.textContent = value;
  };

  const setNotice = (message, state) => {
    const notice = element("hive-provider-notice");
    if (!notice) return;
    notice.textContent = message || "";
    notice.dataset.state = state || "info";
  };

  const shortenAddress = (address) =>
    address
      ? `${address.slice(0, 12)}...${address.slice(-10)}`
      : "Not connected";

  const setConnectedState = (address) => {
    connectedAddress = typeof address === "string" ? address : "";
    setText("hive-provider-address", shortenAddress(connectedAddress));
    const connected = Boolean(connectedAddress);
    element("hive-provider-connect").hidden = connected;
    element("hive-provider-disconnect").hidden = !connected;
    element("hive-provider-copy").disabled = !connected;
    element("hive-provider-send").disabled = !connected;
    element("hive-provider-recipient").disabled = !connected;
    element("hive-provider-amount").disabled = !connected;
    setText("hive-provider-balance", connected ? "Loading..." : "- CELL");
    setText(
      "hive-provider-state",
      connected ? "Connected through QSDM Hive" : "Ready to connect"
    );
  };

  const refreshBalance = async () => {
    if (!provider || !connectedAddress) return;
    try {
      const result = await provider.request({ method: "qsdm_getBalance" });
      const balance = result && result.balance !== null ? result.balance : "-";
      const token = (result && result.token) || "CELL";
      setText("hive-provider-balance", `${balance} ${token}`);
      if (result && result.reachable === false) {
        setNotice(
          "QSDM Core is temporarily unavailable. The last wallet connection is retained.",
          "warn"
        );
      }
    } catch (error) {
      setText("hive-provider-balance", "- CELL");
      setNotice(
        error instanceof Error ? error.message : String(error),
        "error"
      );
    }
  };

  const refreshAccounts = async () => {
    if (!provider) return;
    try {
      const accounts = await provider.request({ method: "qsdm_accounts" });
      setConnectedState(Array.isArray(accounts) ? accounts[0] || "" : "");
      if (connectedAddress) await refreshBalance();
    } catch (error) {
      setConnectedState("");
      setNotice(
        error instanceof Error ? error.message : String(error),
        "error"
      );
    }
  };

  const attachProvider = () => {
    const candidate = window.qsdm;
    if (
      !candidate ||
      candidate.isQsdmHive !== true ||
      typeof candidate.request !== "function"
    ) {
      return false;
    }
    if (provider === candidate) return true;
    provider = candidate;
    if (detectionTimer) {
      window.clearInterval(detectionTimer);
      detectionTimer = null;
    }
    element("hive-provider-connect").disabled = false;
    element("hive-provider-connect").textContent = "Connect Hive wallet";
    setText("hive-provider-state", "QSDM Wallet extension detected");
    setNotice(
      "Connect once, then approve each signature or CELL transfer in QSDM Hive.",
      "info"
    );
    provider.on?.("accountsChanged", (accounts) => {
      setConnectedState(Array.isArray(accounts) ? accounts[0] || "" : "");
      if (connectedAddress) void refreshBalance();
    });
    void refreshAccounts();
    return true;
  };

  const connect = async () => {
    if (!provider && !attachProvider()) {
      window.location.assign(ONBOARDING_URL);
      return;
    }
    const button = element("hive-provider-connect");
    button.disabled = true;
    setNotice("Approve this website in QSDM Hive.", "info");
    try {
      const accounts = await provider.request({
        method: "qsdm_requestAccounts",
      });
      const address = Array.isArray(accounts) ? accounts[0] || "" : "";
      if (!ADDRESS_PATTERN.test(address))
        throw new Error("Hive returned an invalid wallet address");
      setConnectedState(address);
      setNotice(
        "Wallet connected. Private keys remain inside QSDM Hive.",
        "success"
      );
      await refreshBalance();
    } catch (error) {
      setNotice(
        error instanceof Error ? error.message : String(error),
        "error"
      );
    } finally {
      button.disabled = false;
    }
  };

  const disconnect = async () => {
    if (!provider) return;
    try {
      await provider.request({ method: "qsdm_disconnect" });
      setConnectedState("");
      setNotice(
        "This website is disconnected from your Hive wallet.",
        "success"
      );
    } catch (error) {
      setNotice(
        error instanceof Error ? error.message : String(error),
        "error"
      );
    }
  };

  const copyAddress = async () => {
    if (!connectedAddress) return;
    try {
      await navigator.clipboard.writeText(connectedAddress);
      setNotice("Wallet address copied.", "success");
    } catch {
      setNotice(
        "Clipboard access was blocked. Select the address and copy it manually.",
        "error"
      );
    }
  };

  const sendCell = async () => {
    const recipient = element("hive-provider-recipient").value.trim();
    const amountText = element("hive-provider-amount").value.trim();
    if (!ADDRESS_PATTERN.test(recipient)) {
      setNotice("Enter a valid QSDM wallet address.", "error");
      return;
    }
    if (!AMOUNT_PATTERN.test(amountText) || Number(amountText) <= 0) {
      setNotice(
        "Enter a positive CELL amount with no more than 8 decimal places.",
        "error"
      );
      return;
    }

    const button = element("hive-provider-send");
    button.disabled = true;
    setNotice("Review and approve this transfer in QSDM Hive.", "info");
    try {
      const result = await provider.request({
        method: "qsdm_sendTransaction",
        params: { recipient, amount: Number(amountText) },
      });
      const transactionId =
        result && (result.transaction_id || result.transactionId);
      setNotice(
        transactionId
          ? `CELL transfer accepted: ${transactionId}`
          : "CELL transfer accepted by QSDM Core.",
        "success"
      );
      element("hive-provider-amount").value = "";
      await refreshBalance();
    } catch (error) {
      setNotice(
        error instanceof Error ? error.message : String(error),
        "error"
      );
    } finally {
      button.disabled = !connectedAddress;
    }
  };

  const addStyles = () => {
    if (element("qsdm-hive-provider-styles")) return;
    const style = document.createElement("style");
    style.id = "qsdm-hive-provider-styles";
    style.textContent = `
      .hive-provider-head { display:flex; justify-content:space-between; align-items:flex-start; gap:20px; }
      .hive-provider-head h2 { margin:0 0 6px; }
      .hive-provider-status { color:var(--success); font-size:13px; font-weight:600; }
      .hive-provider-grid { display:grid; grid-template-columns:minmax(0,1fr) minmax(180px,.45fr); gap:10px; margin:18px 0; }
      .hive-provider-field { border:1px solid var(--border); padding:12px 14px; background:rgba(0,0,0,.2); min-width:0; }
      .hive-provider-field span { display:block; color:var(--muted); font-size:12px; margin-bottom:5px; }
      .hive-provider-field strong { display:block; overflow-wrap:anywhere; }
      .hive-provider-send { display:grid; grid-template-columns:minmax(220px,1fr) minmax(120px,.3fr) auto; gap:10px; align-items:end; }
      .hive-provider-send label { color:var(--muted); font-size:12px; }
      .hive-provider-send input { display:block; width:100%; margin-top:6px; }
      .hive-provider-actions { display:flex; flex-wrap:wrap; gap:10px; margin-top:14px; }
      #hive-provider-notice { min-height:20px; margin:14px 0 0; font-size:13px; color:var(--text-2); overflow-wrap:anywhere; }
      #hive-provider-notice[data-state="error"] { color:var(--danger); }
      #hive-provider-notice[data-state="warn"] { color:var(--warn); }
      #hive-provider-notice[data-state="success"] { color:var(--success); }
      @media (max-width:720px) {
        .hive-provider-head, .hive-provider-send { display:flex; flex-direction:column; align-items:stretch; }
        .hive-provider-grid { grid-template-columns:1fr; }
      }
    `;
    document.head.appendChild(style);
  };

  const createPanel = () => {
    if (element(PANEL_ID)) return;
    const panel = document.createElement("section");
    panel.id = PANEL_ID;
    panel.className = "panel";
    panel.innerHTML = `
      <div class="hive-provider-head">
        <div>
          <h2>QSDM Hive wallet</h2>
          <p style="margin:0">Use the same wallet in Hive and supported websites. This page never receives your keystore or passphrase.</p>
        </div>
        <div class="hive-provider-status" id="hive-provider-state">Looking for the QSDM Wallet extension...</div>
      </div>
      <div class="hive-provider-grid">
        <div class="hive-provider-field"><span>Active wallet</span><strong id="hive-provider-address">Not connected</strong></div>
        <div class="hive-provider-field"><span>Balance</span><strong id="hive-provider-balance">- CELL</strong></div>
      </div>
      <div class="hive-provider-send">
        <label>Destination<input id="hive-provider-recipient" type="text" autocomplete="off" placeholder="QSDM wallet address" disabled /></label>
        <label>Amount<input id="hive-provider-amount" type="text" inputmode="decimal" autocomplete="off" placeholder="0.00000000" disabled /></label>
        <button class="btn btn-primary" id="hive-provider-send" type="button" disabled>Send CELL</button>
      </div>
      <div class="hive-provider-actions">
        <button class="btn btn-primary" id="hive-provider-connect" type="button" disabled>Connect Hive wallet</button>
        <button class="btn btn-ghost" id="hive-provider-copy" type="button" disabled>Copy address</button>
        <button class="btn btn-ghost" id="hive-provider-disconnect" type="button" hidden>Disconnect site</button>
        <a class="btn btn-ghost" href="qsdm-hive://open?route=%2Fsettings%2Fwallet">Open QSDM Hive</a>
      </div>
      <p id="hive-provider-notice" data-state="info">Install the QSDM Wallet extension and keep QSDM Hive running.</p>
    `;
    const hero = document.querySelector("main .hero");
    if (hero) hero.insertAdjacentElement("afterend", panel);
    else document.querySelector("main")?.prepend(panel);

    element("hive-provider-connect").addEventListener("click", connect);
    element("hive-provider-disconnect").addEventListener("click", disconnect);
    element("hive-provider-copy").addEventListener("click", copyAddress);
    element("hive-provider-send").addEventListener("click", sendCell);
  };

  const boot = () => {
    addStyles();
    createPanel();
    window.addEventListener("qsdm#initialized", attachProvider, { once: true });
    if (!attachProvider()) {
      detectionTimer = window.setInterval(attachProvider, 500);
      window.setTimeout(() => {
        if (detectionTimer) {
          window.clearInterval(detectionTimer);
          detectionTimer = null;
        }
        if (!provider) {
          element("hive-provider-connect").disabled = false;
          element("hive-provider-connect").textContent = "Get QSDM Wallet";
          setText("hive-provider-state", "Extension not detected");
          setNotice(
            "Continue to the QSDM Wallet setup. Installed extensions open onboarding; otherwise the download page opens.",
            "info"
          );
        }
      }, 2000);
    }
  };

  if (document.readyState === "loading")
    document.addEventListener("DOMContentLoaded", boot, { once: true });
  else boot();
})();
