// QSDM Wallet extension: the wallet app in the popup (popup.html) and in a tab (popup.html?full=1).
//
// QSDM Hive holds the key. Everything that needs the wallet goes to Hive through the native bridge
// (tech.qsdm.hive_wallet) as the internal origin; every transfer is approved in Hive before it is signed. Public chain
// reads (status, receipts) go through background.js, which fetches a fixed set of read-only QSDM API paths; this page
// itself has no network access (connect-src 'none').
(() => {
  "use strict";

  const NATIVE_HOST = "tech.qsdm.hive_wallet";
  const PROVIDER_VERSION = "qsdm-hive-wallet-provider/v1";
  const INTERNAL_ORIGIN = "qsdm-extension://wallet-popup";
  const HIVE_WALLET_URL = "qsdm-hive://open?route=%2Fsettings%2Fwallet";
  const ACCOUNT_URL = "https://qsdm.tech/account/";
  const EXPLORER_URL = "https://qsdm.tech/explorer.html";
  const DUST = 100000000n;
  const ADDRESS_RE = /^[0-9a-f]{64}$/;
  const BALANCE_REFRESH_MS = 20000;
  const SCAN_BLOCKS = 1000; // recent blocks History reads for this wallet's transfers
  const SCAN_SPAN = 160; // heights per receipts request (the API returns at most 200 receipts)
  const KEEP_SENDS = 50;
  const KEEP_SEEN = 100;

  const full = new URLSearchParams(window.location.search).get("full") === "1";
  const $ = (id) => document.getElementById(id);

  const state = {
    hive: "checking", // checking | offline | locked | ready
    address: "",
    connectedSites: null,
    balanceDust: null,
    balanceBusy: false,
    hidden: false,
    prefs: { theme: "system", hideBalance: false },
    api: { ok: null, checkedAt: 0, tip: null },
    view: "home",
    draft: null, // { to, dust } while sending
    sending: false,
    site: { origin: "", name: "", connected: false },
  };

  // ------------------------------------------------------------------ storage (localStorage: this extension only)
  const store = {
    get(key, fallback) {
      try {
        const raw = window.localStorage.getItem(`qsdm-wallet:${key}`);
        return raw ? JSON.parse(raw) : fallback;
      } catch {
        return fallback;
      }
    },
    set(key, value) {
      try {
        window.localStorage.setItem(`qsdm-wallet:${key}`, JSON.stringify(value));
      } catch {
        // storage full or blocked: the wallet still works, it just forgets
      }
    },
  };

  // ------------------------------------------------------------------ amounts (integer dust, 1 CELL = 100,000,000)
  // The bridge reports CELL as a JSON number that mirrors whole dust; toFixed(8) gives back that dust exactly for any
  // balance below 2^26 CELL (more than 74% of the 90 M supply).
  const numberToDust = (value) => {
    const n = Number(value);
    if (value === null || value === undefined || value === "" || !Number.isFinite(n) || n < 0) return null;
    return parseCell(n.toFixed(8));
  };

  // A typed CELL amount to dust: digits with at most 8 decimals; null when it is not one.
  const parseCell = (text) => {
    const t = String(text ?? "").trim();
    const m = /^(\d{0,11})(?:\.(\d{0,8}))?$/.exec(t);
    if (!m || (m[1] === "" && (m[2] ?? "") === "")) return null;
    return BigInt(m[1] || "0") * DUST + BigInt((m[2] ?? "").padEnd(8, "0"));
  };

  const dustText = (d, { group = true } = {}) => {
    const neg = d < 0n;
    const v = neg ? -d : d;
    const whole = v / DUST;
    const frac = (v % DUST).toString().padStart(8, "0");
    return `${neg ? "-" : ""}${group ? whole.toLocaleString("en-US") : whole.toString()}.${frac}`;
  };

  // The shortest plain decimal for an amount field ("0.5", "42").
  const dustInput = (d) => dustText(d, { group: false }).replace(/\.?0+$/, "");

  const HIDDEN = "••••••";
  const money = (d) => (state.hidden ? HIDDEN : dustText(d));

  const shortAddress = (a) => (a && a.length > 20 ? `${a.slice(0, 8)}…${a.slice(-8)}` : a || "");

  const groupedAddress = (a) => (a || "").replace(/(.{8})/g, "$1 ").trim();

  const ago = (ms) => {
    const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    if (s < 60) return "just now";
    if (s < 3600) return `${Math.round(s / 60)} min ago`;
    if (s < 86400) return `${Math.round(s / 3600)} h ago`;
    return new Date(ms).toLocaleDateString(undefined, { day: "numeric", month: "short" });
  };

  const txUrl = (id) => `${EXPLORER_URL}?tx=${encodeURIComponent(id)}`;
  const addressUrl = (a) => `${EXPLORER_URL}?address=${encodeURIComponent(a)}`;

  // ------------------------------------------------------------------ QSDM Hive bridge
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
          finish({ ok: false, error: runtimeError?.message || "Start QSDM Hive to use your wallet" });
        });
        port.postMessage({ version: PROVIDER_VERSION, id: crypto.randomUUID(), origin, method, params });
      } catch (error) {
        finish({ ok: false, error: error instanceof Error ? error.message : String(error) });
      }
    });

  const hive = (method, params, timeoutMs) => sendNative(INTERNAL_ORIGIN, method, params, timeoutMs);

  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  const transactionReference = (value) =>
    value?.transaction_id || value?.transactionId || value?.tx_id || value?.id || "";

  // ------------------------------------------------------------------ public QSDM API (through background.js)
  const api = (path) =>
    new Promise((resolve) => {
      try {
        chrome.runtime.sendMessage({ source: "qsdm-wallet-api", path }, (response) => {
          void chrome.runtime.lastError;
          resolve(response || { ok: false, status: 0 });
        });
      } catch {
        resolve({ ok: false, status: 0 });
      }
    });

  // Whether the public API answers this extension; asked again at most once a minute.
  const checkApi = async (force = false) => {
    if (!force && state.api.ok !== null && Date.now() - state.api.checkedAt < 60000) return state.api.ok;
    const status = await api("/status");
    state.api.checkedAt = Date.now();
    state.api.ok = Boolean(status.ok && status.json && Number.isFinite(Number(status.json.chain_tip)));
    if (state.api.ok) state.api.tip = Number(status.json.chain_tip);
    return state.api.ok;
  };

  // Sender, recipient and amount from a receipt's TxApplied log (the explorer reads them the same way).
  const parties = (receipt) => {
    const log = (receipt?.logs || []).find((l) => l?.data && (l.data.sender !== undefined || l.data.recipient !== undefined));
    const data = log?.data || {};
    return { from: String(data.sender || ""), to: String(data.recipient || ""), amount: data.amount };
  };

  // ------------------------------------------------------------------ history (this browser's sends + chain receipts)
  const historyKey = () => `history:${state.address}`;
  const loadHistory = () => {
    const h = store.get(historyKey(), null);
    return {
      sends: Array.isArray(h?.sends) ? h.sends : [],
      seen: Array.isArray(h?.seen) ? h.seen : [],
      scannedTo: Number.isFinite(h?.scannedTo) ? h.scannedTo : 0,
    };
  };
  const saveHistory = (h) =>
    store.set(historyKey(), { sends: h.sends.slice(0, KEEP_SENDS), seen: h.seen.slice(0, KEEP_SEEN), scannedTo: h.scannedTo });

  // CELL sent from this browser that is not in a block yet. Without the public API the wallet cannot see blocks, so it
  // counts only sends of the last 10 minutes (a QSDM block comes about every 10 seconds).
  const PENDING_WINDOW_MS = 10 * 60 * 1000;
  const pendingDust = () =>
    loadHistory()
      .sends.filter((s) => !s.height && !s.failed && (state.api.ok || Date.now() - s.at < PENDING_WINDOW_MS))
      .reduce((sum, s) => sum + BigInt(s.dust), 0n);

  // Puts block heights on this browser's sends whose receipts exist now.
  const confirmSends = async () => {
    if (!state.address || !(await checkApi())) return false;
    const h = loadHistory();
    let changed = false;
    for (const s of h.sends.filter((x) => !x.height).slice(0, 10)) {
      const r = await api(`/receipts/${encodeURIComponent(s.id)}`);
      if (r.ok && r.json && Number.isFinite(Number(r.json.block_height))) {
        s.height = Number(r.json.block_height);
        s.failed = Number(r.json.status) === 0; // chain.ReceiptFailed (1 is ReceiptSuccess)
        changed = true;
      }
    }
    if (changed) saveHistory(h);
    return changed;
  };

  // Reads the receipts of the most recent blocks (up to SCAN_BLOCKS, only the ones not read before) and keeps the
  // transfers to or from this wallet.
  const scanChain = async () => {
    if (!state.address || !(await checkApi(true))) return false;
    const h = loadHistory();
    const tip = state.api.tip;
    let from = Math.max(h.scannedTo + 1, tip - SCAN_BLOCKS + 1, 0);
    const me = state.address;
    const known = new Set(h.seen.map((x) => x.id));
    while (from <= tip) {
      const to = Math.min(tip, from + SCAN_SPAN - 1);
      const r = await api(`/receipts?from=${from}&to=${to}&limit=200`);
      if (!r.ok || !Array.isArray(r.json?.receipts)) break;
      for (const receipt of r.json.receipts) {
        const p = parties(receipt);
        const fromMe = p.from.toLowerCase() === me;
        const toMe = p.to.toLowerCase() === me;
        if ((!fromMe && !toMe) || known.has(receipt.tx_id)) continue;
        const dust = numberToDust(p.amount);
        if (dust === null) continue;
        known.add(receipt.tx_id);
        h.seen.push({
          id: String(receipt.tx_id),
          dir: fromMe && toMe ? "self" : fromMe ? "out" : "in",
          other: fromMe ? p.to : p.from,
          dust: dust.toString(),
          height: Number(receipt.block_height),
          at: Date.parse(receipt.timestamp) || Date.now(),
        });
      }
      h.scannedTo = to;
      from = to + 1;
    }
    h.seen.sort((a, b) => b.height - a.height);
    saveHistory(h);
    return true;
  };

  // ------------------------------------------------------------------ small UI helpers
  let toastTimer;
  const toast = (text) => {
    const el = $("toast");
    el.textContent = text;
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      el.hidden = true;
    }, 1600);
  };

  const copy = async (text, what) => {
    try {
      await navigator.clipboard.writeText(text);
      toast(`${what} copied`);
    } catch {
      toast("Copy is not available here");
    }
  };

  const setNotice = (text, kind = "info") => {
    const el = $("notice");
    el.textContent = text || "";
    el.dataset.state = kind;
  };

  const icon = (name) => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "icon");
    svg.setAttribute("aria-hidden", "true");
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", `#i-${name}`);
    svg.append(use);
    return svg;
  };

  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };

  const openTab = async (url) => {
    try {
      await chrome.tabs.create({ url });
    } catch {
      window.open(url, "_blank", "noopener");
    }
  };

  const openHive = async () => {
    setNotice("Opening QSDM Hive...");
    const response = await hive("qsdm_openWallet", undefined, 5000);
    if (!response?.ok) {
      try {
        await chrome.tabs.create({ url: HIVE_WALLET_URL });
      } catch {
        setNotice("Start QSDM Hive from your applications menu.", "error");
        return;
      }
    }
    setNotice("");
    await sleep(800);
    await connect();
  };

  // ------------------------------------------------------------------ theme and privacy
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const resolvedTheme = () => (state.prefs.theme === "system" ? (media.matches ? "dark" : "light") : state.prefs.theme);

  const applyTheme = () => {
    const theme = resolvedTheme();
    document.documentElement.dataset.theme = theme;
    const toggle = $("theme-toggle");
    toggle.replaceChildren(icon(theme === "dark" ? "sun" : "moon"));
    toggle.setAttribute("aria-label", theme === "dark" ? "Switch to light theme" : "Switch to dark theme");
    for (const b of document.querySelectorAll("[data-theme-choice]")) {
      b.setAttribute("aria-pressed", String(b.dataset.themeChoice === state.prefs.theme));
    }
  };

  const setTheme = (choice) => {
    state.prefs.theme = choice;
    store.set("prefs", state.prefs);
    applyTheme();
  };

  const applyHidden = () => {
    const b = $("toggle-hide");
    b.replaceChildren(icon(state.hidden ? "eye-off" : "eye"));
    b.setAttribute("aria-label", state.hidden ? "Show balance" : "Hide balance");
    b.title = state.hidden ? "Show balance" : "Hide balance";
    $("hide-switch").setAttribute("aria-checked", String(state.prefs.hideBalance));
    renderBalance();
    renderRecent();
    if (state.view === "history") renderHistory();
  };

  // ------------------------------------------------------------------ views
  const VIEWS = ["home", "receive", "send", "review", "result", "history", "sites", "settings"];
  const NAV_OF = { home: "home", receive: "home", send: "home", review: "home", result: "home", history: "home", sites: "sites", settings: "settings" };

  const show = (view) => {
    state.view = view;
    for (const section of document.querySelectorAll("[data-view]")) {
      section.hidden = section.dataset.view !== view;
    }
    for (const b of document.querySelectorAll("[data-nav]")) {
      if (b.dataset.nav === NAV_OF[view]) b.setAttribute("aria-current", "page");
      else b.removeAttribute("aria-current");
    }
    document.querySelector(".content").scrollTop = 0;
    if (view === "receive") renderReceive();
    if (view === "send") renderSendForm();
    if (view === "history") openHistory();
    if (view === "sites") renderSites();
  };

  // ------------------------------------------------------------------ wallet home
  const renderHive = () => {
    document.querySelector(".app").dataset.hive = state.hive;
    const dot = $("hive-dot");
    dot.dataset.state = state.hive;
    const chip = $("network-chip");
    chip.title = {
      checking: "Checking QSDM Hive",
      offline: "QSDM Hive is not running",
      locked: "QSDM Hive is running; the wallet is locked or not set up",
      ready: "QSDM Hive is connected",
    }[state.hive];

    const ready = state.hive === "ready";
    $("blocked").hidden = ready || state.hive === "checking";
    for (const id of ["action-receive", "action-send", "action-history", "copy-address", "refresh-balance"]) {
      $(id).disabled = !ready;
    }
    $("send-fields").disabled = !ready || state.sending;
    if (state.hive === "offline") {
      $("blocked-title").textContent = "Open QSDM Hive";
      $("blocked-text").textContent = "Your keys live in QSDM Hive. Start it and this wallet connects by itself.";
      $("blocked-action").textContent = "Open QSDM Hive";
    } else if (state.hive === "locked") {
      $("blocked-title").textContent = "Unlock your wallet";
      $("blocked-text").textContent = "Create, import or unlock your QSDM wallet in Hive to see your balance and send CELL.";
      $("blocked-action").textContent = "Open wallet in Hive";
    }

    const address = $("dashboard-address");
    address.textContent = state.address ? shortAddress(state.address) : ready ? "" : state.hive === "checking" ? "Checking..." : "Not available";
    address.title = state.address;
    $("avatar").textContent = state.address ? state.address.slice(0, 2).toUpperCase() : "--";
  };

  const renderBalance = () => {
    const box = $("dashboard-balance");
    const avail = $("balance-available");
    const pending = $("balance-pending");
    if (state.balanceDust === null) {
      if (state.hive === "checking" || state.balanceBusy) box.replaceChildren(el("span", "skeleton"));
      else box.replaceChildren(el("span", "num", "-"));
      avail.textContent = "-";
      pending.textContent = "-";
      return;
    }
    // textContent reads "42.50000000 CELL"
    box.replaceChildren(el("span", "num", money(state.balanceDust)), document.createTextNode(" "), el("span", "unit", "CELL"));
    avail.textContent = money(state.balanceDust);
    pending.textContent = money(pendingDust());
  };

  const refreshBalance = async ({ quiet = false } = {}) => {
    if (state.hive !== "ready" || state.balanceBusy) return;
    state.balanceBusy = true;
    const button = $("refresh-balance");
    button.classList.add("spin");
    if (!quiet) $("balance-state").textContent = "Refreshing...";
    if (state.balanceDust === null) renderBalance();
    const response = await hive("qsdm_getBalance", undefined, 10000);
    state.balanceBusy = false;
    button.classList.remove("spin");
    const dust = response?.ok ? numberToDust(response.result?.balance) : null;
    if (dust !== null) {
      state.balanceDust = dust;
      $("balance-state").textContent = "Live CELL balance";
    } else {
      $("balance-state").textContent =
        response?.ok && response.result?.reachable === false
          ? "Hive cannot reach the QSDM network right now"
          : response?.error || "Balance unavailable";
    }
    renderBalance();
    if (state.view === "send") renderSendForm(false);
  };

  // Connects to Hive: ping, wallet info, then the balance.
  const connect = async () => {
    state.hive = "checking";
    renderHive();
    let ping;
    for (let attempt = 0; attempt < 4; attempt += 1) {
      ping = await hive("qsdm_ping", undefined, 5000);
      if (ping?.ok) break;
      if (attempt < 3) await sleep(500);
    }
    if (!ping?.ok) {
      state.hive = "offline";
      state.address = "";
      state.balanceDust = null;
      renderHive();
      renderBalance();
      $("balance-state").textContent = "QSDM Hive is not running";
      return;
    }
    const info = await hive("qsdm_getWalletInfo", undefined, 5000);
    const address = String(info?.result?.address || "").toLowerCase();
    if (!info?.ok || !info.result?.ready || !ADDRESS_RE.test(address)) {
      state.hive = "locked";
      state.address = "";
      state.balanceDust = null;
      renderHive();
      renderBalance();
      $("balance-state").textContent = info?.ok ? "Wallet locked in Hive" : info?.error || "Wallet unavailable";
      return;
    }
    if (address !== state.address) state.balanceDust = null;
    state.address = address;
    state.connectedSites = Number.isFinite(Number(info.result.connectedSites)) ? Number(info.result.connectedSites) : null;
    state.hive = "ready";
    renderHive();
    renderRecent();
    await refreshBalance();
    // Confirmations of earlier sends, in the background.
    confirmSends().then((changed) => {
      if (!changed) return;
      renderBalance();
      renderRecent();
    });
  };

  // ------------------------------------------------------------------ receive
  const renderReceive = () => {
    const a = state.address;
    const box = $("qr");
    box.replaceChildren();
    if (a && window.QSDMQR) {
      const q = window.QSDMQR.matrix(a);
      const border = 2;
      const n = q.size + border * 2;
      const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("viewBox", `0 0 ${n} ${n}`);
      svg.setAttribute("shape-rendering", "crispEdges");
      svg.setAttribute("aria-hidden", "true");
      const bg = document.createElementNS("http://www.w3.org/2000/svg", "rect");
      bg.setAttribute("width", String(n));
      bg.setAttribute("height", String(n));
      bg.setAttribute("fill", "#ffffff");
      const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
      let d = "";
      for (let y = 0; y < q.size; y += 1) {
        for (let x = 0; x < q.size; x += 1) {
          if (q.modules[y][x]) d += `M${x + border},${y + border}h1v1h-1z`;
        }
      }
      path.setAttribute("d", d);
      path.setAttribute("fill", "#061116");
      svg.append(bg, path);
      box.append(svg);
    }
    const code = $("receive-address");
    code.replaceChildren();
    // The first and last 8 characters stand out: the ones people compare.
    if (a) code.append(el("b", "", a.slice(0, 8)), document.createTextNode(` ${groupedAddress(a.slice(8, -8))} `), el("b", "", a.slice(-8)));
    $("receive-explorer").href = a ? addressUrl(a) : EXPLORER_URL;
  };

  // ------------------------------------------------------------------ send
  const setFieldError = (wrap, errorId, text) => {
    $(wrap).dataset.invalid = text ? "true" : "false";
    $(errorId).textContent = text || "";
  };

  const renderSendForm = (resetErrors = true) => {
    $("send-available").textContent = state.balanceDust === null ? "-" : `${money(state.balanceDust)} CELL`;
    $("send-max").disabled = state.balanceDust === null;
    if (resetErrors) {
      setFieldError("recipient-wrap", "recipient-error", "");
      setFieldError("amount-wrap", "amount-error", "");
    }
  };

  const validateSend = () => {
    const to = $("send-recipient").value.trim().toLowerCase();
    const dust = parseCell($("send-amount").value);
    let ok = true;
    if (!ADDRESS_RE.test(to)) {
      setFieldError("recipient-wrap", "recipient-error", "Enter a QSDM address: 64 characters, 0-9 and a-f.");
      ok = false;
    } else setFieldError("recipient-wrap", "recipient-error", "");
    if (dust === null || dust <= 0n) {
      setFieldError("amount-wrap", "amount-error", "Enter an amount above 0, with at most 8 decimals.");
      ok = false;
    } else if (state.balanceDust !== null && dust > state.balanceDust) {
      setFieldError("amount-wrap", "amount-error", `That is more than the ${dustText(state.balanceDust)} CELL available.`);
      ok = false;
    } else setFieldError("amount-wrap", "amount-error", "");
    return ok ? { to, dust } : null;
  };

  const renderReview = () => {
    const { to, dust } = state.draft;
    $("review-from").textContent = groupedAddress(state.address);
    $("review-to").textContent = groupedAddress(to);
    $("review-amount").textContent = `${dustText(dust)} CELL`;
    $("review-total").textContent = `${dustText(dust)} CELL`;
    const self = to === state.address;
    const callout = document.querySelector("[data-view='review'] .callout span");
    callout.textContent = self
      ? "This is this wallet's own address: the CELL stays here. QSDM Hive shows the transfer again and signs it only after you approve it there."
      : "QSDM Hive shows this transfer again and signs it only after you approve it there. Transfers cannot be undone.";
  };

  const showResult = (kind, title, text, txId) => {
    const box = $("result");
    box.dataset.state = kind;
    $("result-icon").setAttribute("href", kind === "ok" ? "#i-check" : kind === "error" ? "#i-x" : "#i-hive");
    $("result-title").textContent = title;
    $("result-text").textContent = text;
    $("result-txid").hidden = !txId;
    $("result-txid-text").textContent = txId || "";
    $("result-explorer").hidden = !txId;
    if (txId) $("result-explorer").href = txUrl(txId);
    $("result-done").hidden = kind === "waiting";
    $("result-done").textContent = kind === "error" ? "Back to the form" : "Done";
    $("result-done").dataset.next = kind === "error" ? "send" : "home";
  };

  const submitTransfer = async () => {
    if (!state.draft || state.sending) return;
    const { to, dust } = state.draft;
    state.sending = true;
    $("confirm-send").disabled = true;
    renderHive();
    show("result");
    showResult("waiting", "Approve in QSDM Hive", "Hive shows the exact recipient and amount. Approve it there to send.", "");
    const response = await hive("qsdm_sendTransaction", { recipient: to, amount: Number(dustInput(dust)) }, 120000);
    state.sending = false;
    $("confirm-send").disabled = false;
    if (!response?.ok) {
      renderHive();
      showResult("error", "Not sent", response?.error || "The transfer was not approved.", "");
      return;
    }
    const txId = String(transactionReference(response.result) || "");
    if (txId) {
      const h = loadHistory();
      h.sends.unshift({ id: txId, to, dust: dust.toString(), at: Date.now(), height: null });
      saveHistory(h);
    }
    state.draft = null;
    $("send-form").reset();
    renderHive();
    showResult(
      "ok",
      "Sent",
      txId ? `${dustText(dust)} CELL is on its way. Its transaction id:` : `${dustText(dust)} CELL is on its way.`,
      txId
    );
    renderRecent();
    await refreshBalance({ quiet: true });
  };

  // ------------------------------------------------------------------ history
  let historyBusy = false;

  const historyItems = () => {
    const h = loadHistory();
    const byId = new Map();
    for (const s of h.seen) byId.set(s.id, { ...s, source: "chain" });
    for (const s of h.sends) {
      const seen = byId.get(s.id);
      if (seen) seen.local = true;
      else byId.set(s.id, { id: s.id, dir: s.to === state.address ? "self" : "out", other: s.to, dust: s.dust, height: s.height, at: s.at, failed: s.failed, source: "local" });
    }
    return [...byId.values()].sort((a, b) => (b.at || 0) - (a.at || 0)).slice(0, 60);
  };

  const statusOf = (item) => {
    if (item.failed) return { text: "Failed", state: "failed" };
    if (!item.height) return state.api.ok ? { text: "Pending", state: "pending" } : { text: "Submitted", state: "" };
    if (state.api.tip) {
      const n = Math.max(1, state.api.tip - item.height + 1);
      return { text: `${n.toLocaleString("en-US")} confirmation${n === 1 ? "" : "s"}`, state: "ok" };
    }
    return { text: `Block ${item.height.toLocaleString("en-US")}`, state: "ok" };
  };

  // One transfer as a row: direction, counterparty and age on the left; amount and confirmations on the right.
  const txRow = (item) => {
    const row = el("button", "tx");
    row.type = "button";
    row.dataset.dir = item.dir;
    row.title = "Open in the QSDM Explorer";
    const iconBox = el("span", "tx-icon");
    iconBox.append(icon(item.dir === "in" ? "in" : item.dir === "self" ? "self" : "out"));
    const main = el("span", "tx-main");
    main.append(el("div", "tx-title", item.dir === "in" ? "Received" : item.dir === "self" ? "Self transfer" : "Sent"));
    const who = el("div", "tx-sub");
    if (item.dir === "self") who.textContent = "Within this wallet";
    else who.append(document.createTextNode(item.dir === "in" ? "From " : "To "), el("span", "mono", shortAddress(item.other)));
    main.append(who, el("div", "tx-sub", ago(item.at || Date.now())));
    const side = el("span", "tx-side");
    const sign = item.dir === "in" ? "+" : item.dir === "out" ? "-" : "";
    side.append(el("div", "tx-amount", state.hidden ? HIDDEN : `${sign}${dustText(BigInt(item.dust))}`));
    const status = statusOf(item);
    const statusEl = el("div", "tx-status", status.text);
    statusEl.dataset.state = status.state;
    side.append(statusEl);
    row.append(iconBox, main, side);
    row.addEventListener("click", () => openTab(txUrl(item.id)));
    return row;
  };

  // The three newest transfers on the wallet page (hidden while there are none).
  const renderRecent = () => {
    const items = state.address ? historyItems().slice(0, 3) : [];
    $("recent").hidden = !items.length;
    $("recent-list").replaceChildren(...items.map(txRow));
  };

  const renderHistory = () => {
    const list = $("history-list");
    const items = historyItems();
    list.replaceChildren();
    if (!items.length) {
      list.append(el("div", "empty", state.api.ok ? "No transfers in the recent blocks." : "No transfers from this browser yet."));
    }
    for (const item of items) list.append(txRow(item));
    const note = $("history-note");
    note.hidden = state.api.ok !== false;
    $("history-note-text").textContent =
      "Confirmations and received CELL show here once the QSDM network lets this extension read it. Until then, tap a transfer or All activity to follow it in the Explorer.";
    const h = loadHistory();
    $("history-range").textContent = state.api.ok && h.scannedTo ? `Recent blocks to ${h.scannedTo.toLocaleString("en-US")}` : "Sent from this browser";
    $("history-explorer").href = state.address ? addressUrl(state.address) : EXPLORER_URL;
  };

  const openHistory = async () => {
    renderHistory();
    if (historyBusy) return;
    historyBusy = true;
    $("history-refresh").classList.add("spin");
    try {
      await scanChain();
      await confirmSends();
    } finally {
      historyBusy = false;
      $("history-refresh").classList.remove("spin");
    }
    if (state.view === "history") renderHistory();
    renderBalance();
    renderRecent();
  };

  // ------------------------------------------------------------------ sites
  const normalizeWebOrigin = (rawUrl) => {
    const parsed = new URL(rawUrl);
    const localHttp = parsed.protocol === "http:" && ["localhost", "127.0.0.1", "::1"].includes(parsed.hostname);
    if (parsed.protocol !== "https:" && !localHttp) throw new Error("not a website");
    return parsed.origin;
  };

  const renderSites = async () => {
    $("sites-count").textContent = state.connectedSites === null ? "-" : String(state.connectedSites);
    const button = $("connect-site");
    const help = $("site-help");
    const pill = $("site-state");
    button.disabled = true;
    state.site = { origin: "", name: "", connected: false };
    if (full) {
      $("site-name").textContent = "Open from a website";
      pill.hidden = true;
      button.hidden = true;
      help.textContent = "To connect a website, open it and click the QSDM Wallet button in the browser toolbar.";
      return;
    }
    try {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      state.site.origin = normalizeWebOrigin(tab?.url || "");
      state.site.name = new URL(state.site.origin).hostname;
    } catch {
      $("site-name").textContent = "This page cannot connect";
      pill.hidden = true;
      button.hidden = true;
      help.textContent = "Wallet connections need an HTTPS website.";
      return;
    }
    $("site-name").textContent = state.site.name;
    $("site-name").title = state.site.origin;
    pill.hidden = false;
    button.hidden = false;
    if (state.hive !== "ready") {
      pill.textContent = "Hive needed";
      pill.dataset.state = "";
      help.textContent = "Open QSDM Hive and unlock your wallet to connect sites.";
      return;
    }
    const accounts = await sendNative(state.site.origin, "qsdm_accounts", undefined, 5000);
    state.site.connected =
      Boolean(accounts?.ok) && Array.isArray(accounts.result) && accounts.result.some((a) => typeof a === "string" && a.toLowerCase() === state.address);
    pill.textContent = state.site.connected ? "Connected" : "Not connected";
    pill.dataset.state = state.site.connected ? "on" : "";
    button.textContent = state.site.connected ? "Disconnect this site" : "Connect this site";
    button.classList.toggle("primary", !state.site.connected);
    help.textContent = state.site.connected
      ? `${state.site.name} can ask this wallet for actions. Each transfer or signature still needs your approval in Hive.`
      : "Connect once. Hive remembers the site until you disconnect it.";
    button.disabled = false;
  };

  const toggleSite = async () => {
    if (!state.site.origin) return;
    const button = $("connect-site");
    button.disabled = true;
    $("site-help").textContent = state.site.connected ? `Disconnecting ${state.site.name}...` : "Approve this site in QSDM Hive.";
    const response = await sendNative(state.site.origin, state.site.connected ? "qsdm_disconnect" : "qsdm_requestAccounts", undefined);
    if (!response?.ok) {
      $("site-help").textContent = response?.error || "The request was not approved.";
      button.disabled = false;
      return;
    }
    const info = await hive("qsdm_getWalletInfo", undefined, 5000);
    if (info?.ok && Number.isFinite(Number(info.result?.connectedSites))) state.connectedSites = Number(info.result.connectedSites);
    await renderSites();
  };

  // ------------------------------------------------------------------ wiring
  const wire = () => {
    document.body.classList.toggle("full", full);
    if (full) {
      $("expand").hidden = true;
      $("open-full").hidden = true;
      document.title = "QSDM Wallet";
    }
    $("version").textContent = chrome.runtime.getManifest?.().version || "-";

    $("theme-toggle").addEventListener("click", () => setTheme(resolvedTheme() === "dark" ? "light" : "dark"));
    media.addEventListener?.("change", () => state.prefs.theme === "system" && applyTheme());
    for (const b of document.querySelectorAll("[data-theme-choice]")) {
      b.addEventListener("click", () => setTheme(b.dataset.themeChoice));
    }
    $("expand").addEventListener("click", async () => {
      await openTab(chrome.runtime.getURL("popup.html?full=1"));
      window.close();
    });
    $("open-full").addEventListener("click", async () => {
      await openTab(chrome.runtime.getURL("popup.html?full=1"));
      if (!full) window.close();
    });

    $("toggle-hide").addEventListener("click", () => {
      state.hidden = !state.hidden;
      applyHidden();
    });
    $("hide-switch").addEventListener("click", () => {
      state.prefs.hideBalance = !state.prefs.hideBalance;
      store.set("prefs", state.prefs);
      state.hidden = state.prefs.hideBalance;
      applyHidden();
    });
    $("refresh-balance").addEventListener("click", async () => {
      await refreshBalance();
      confirmSends().then((changed) => changed && renderBalance());
    });
    $("copy-address").addEventListener("click", () => copy(state.address, "Address"));
    $("copy-receive").addEventListener("click", () => copy(state.address, "Address"));
    $("copy-txid").addEventListener("click", () => copy($("result-txid-text").textContent, "Transaction id"));
    $("account-open").addEventListener("click", openHive);
    $("blocked-action").addEventListener("click", openHive);
    $("open-wallet").addEventListener("click", openHive);
    $("manage-sites").addEventListener("click", openHive);
    $("open-account").addEventListener("click", () => openTab(ACCOUNT_URL));
    $("open-explorer").addEventListener("click", () => openTab(state.address ? addressUrl(state.address) : EXPLORER_URL));
    $("connect-site").addEventListener("click", toggleSite);

    $("action-receive").addEventListener("click", () => show("receive"));
    $("action-send").addEventListener("click", () => show("send"));
    $("action-history").addEventListener("click", () => show("history"));
    $("recent-all").addEventListener("click", () => show("history"));
    $("history-refresh").addEventListener("click", () => {
      state.api.checkedAt = 0;
      openHistory();
    });
    for (const b of document.querySelectorAll("[data-nav]")) {
      b.addEventListener("click", () => show(b.dataset.nav));
    }
    for (const b of document.querySelectorAll("[data-back]")) {
      b.addEventListener("click", () => show(b.dataset.back || "home"));
    }
    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape" || state.sending) return;
      const back = { receive: "home", send: "home", history: "home", review: "send", result: "home" }[state.view];
      if (back) {
        event.preventDefault();
        show(back);
      }
    });

    $("send-max").addEventListener("click", () => {
      if (state.balanceDust === null) return;
      $("send-amount").value = dustInput(state.balanceDust);
      setFieldError("amount-wrap", "amount-error", "");
    });
    $("send-recipient").addEventListener("input", () => setFieldError("recipient-wrap", "recipient-error", ""));
    $("send-amount").addEventListener("input", () => setFieldError("amount-wrap", "amount-error", ""));
    $("send-form").addEventListener("submit", (event) => {
      event.preventDefault();
      if (state.hive !== "ready") return;
      const draft = validateSend();
      if (!draft) return;
      state.draft = draft;
      renderReview();
      show("review");
    });
    $("confirm-send").addEventListener("click", submitTransfer);
    $("result-done").addEventListener("click", () => show($("result-done").dataset.next || "home"));

    setInterval(() => {
      if (document.hidden || state.sending) return;
      if (["home", "send"].includes(state.view)) refreshBalance({ quiet: true });
    }, BALANCE_REFRESH_MS);
  };

  // ------------------------------------------------------------------ start
  state.prefs = { ...state.prefs, ...store.get("prefs", {}) };
  if (!["system", "light", "dark"].includes(state.prefs.theme)) state.prefs.theme = "system";
  state.hidden = Boolean(state.prefs.hideBalance);
  applyTheme();
  wire();
  applyHidden();
  renderHive();
  renderBalance();
  connect().catch((error) => setNotice(error instanceof Error ? error.message : String(error), "error"));
})();
