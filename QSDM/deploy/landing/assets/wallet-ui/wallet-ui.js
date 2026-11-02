/* QSDM web wallet: UX layer (no cryptography in this file).
 *
 *  - First-run "Before you continue" checklist, remembered in localStorage.
 *  - Address-bar check next to the trust statement.
 *  - Compact unlocked view: header icons (network popover, settings sheet,
 *    lock), CELL / Hive segment switch, balance card with live dot,
 *    Receive / Send sheets, address card with QR toggle, transaction
 *    history, "Connect QSDM Hive" and "Mining rewards" cards.
 *  - Receive: QR code drawn locally (assets/wallet-ui/qr.js) + explorer link.
 *  - History: recent on-chain transactions for the wallet address, found by
 *    scanning recent blocks through the public read API (assets/explorer/core.js),
 *    merged with the local send log kept by vault.js.
 *  - Send: review dialog (to, amount, fee, nonce) that vault.js awaits before
 *    it calls the unchanged sign + submit code.
 *  - Hive: reads the connection state of the panel built by wallet-provider.js
 *    and reuses its Connect button; while the vault is unlocked that panel is
 *    shown under the "Hive" segment.
 *
 * Exposes window.QSDMWalletUI = { onAddress, onState, onBalance,
 *   onLocalActivity, reviewSend, onSent, openChecklist }.
 */
(function () {
  "use strict";

  var ACK_KEY = "qsdm.wallet.preflight.v1";
  var SCAN_STEP = 1000;
  var SCAN_MAX = 10000;
  var HIST_PAGE = 10;
  var LIVE_STALE_MS = 75000;
  var NET_POLL_MS = 60000;
  var OFFICIAL_HOSTS = ["qsdm.tech", "www.qsdm.tech"];
  var $ = function (id) { return document.getElementById(id); };
  var X = window.QSDMX || null;
  var esc = X ? X.fmt.esc : function (s) {
    return String(s === null || s === undefined ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  };

  function isOfficialHost() { return OFFICIAL_HOSTS.indexOf(location.hostname) !== -1 && location.protocol === "https:"; }
  function isLocalPreview() { return /^(localhost|127\.0\.0\.1|\[::1\])$/.test(location.hostname); }

  function fmtCell(n) {
    if (n === null || n === undefined || !isFinite(Number(n))) return "—";
    return Number(n).toLocaleString("en-US", { maximumFractionDigits: 8 });
  }
  function shortHash(s, head, tail) {
    s = String(s || "");
    head = head || 10; tail = tail || 8;
    return s.length > head + tail + 3 ? s.slice(0, head) + "..." + s.slice(-tail) : s;
  }
  function timeText(d) {
    return d.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  }

  // ------------------------------------------------------------ checklist gate
  function ackGiven() {
    try { return !!localStorage.getItem(ACK_KEY); } catch (_) { return false; }
  }

  function openChecklist(force) {
    var dlg = $("wui-gate");
    if (!dlg || typeof dlg.showModal !== "function") return;
    if (!force && ackGiven()) return;
    var bad = $("wui-gate-domain");
    if (bad) {
      bad.hidden = isOfficialHost() || isLocalPreview();
      if (!bad.hidden) bad.textContent = "Warning: this page is served from “" + location.host + "”, not https://qsdm.tech. Do not create, import or unlock a wallet here.";
    }
    if (!dlg.open) dlg.showModal();
    var ok = $("wui-gate-ok");
    if (ok) ok.focus();
  }

  function bindChecklist() {
    var dlg = $("wui-gate");
    if (!dlg) return;
    $("wui-gate-ok").addEventListener("click", function () {
      try { localStorage.setItem(ACK_KEY, new Date().toISOString()); } catch (_) {}
      dlg.close("ok");
    });
    $("wui-gate-back").addEventListener("click", function () {
      dlg.close("back");
      if (!ackGiven()) {
        if (document.referrer && new URL(document.referrer).origin === location.origin && history.length > 1) history.back();
        else location.href = "/";
      }
    });
    // Esc on a first visit counts as "Go back"; once acknowledged it just closes.
    dlg.addEventListener("cancel", function (ev) {
      if (!ackGiven()) { ev.preventDefault(); $("wui-gate-back").click(); }
    });
    var again = $("wui-gate-reopen");
    if (again) again.addEventListener("click", function () { openChecklist(true); });
  }

  // ------------------------------------------------------------ URL check
  function renderUrlCheck() {
    var el = $("wui-urlcheck");
    if (!el) return;
    if (isOfficialHost()) {
      el.innerHTML = "Address bar check: <b>✓ " + esc(location.origin) + "</b>";
    } else {
      el.classList.add("bad");
      el.innerHTML = "Address bar check: <b>✗ " + esc(location.origin) + "</b> is not https://qsdm.tech" +
        (isLocalPreview() ? " (local preview)" : " — do not use a wallet here");
    }
  }

  // ------------------------------------------------------------ view state
  var view = { state: "", local: [], histAll: false, lastBal: null, balOk: false, balErr: "", netTimer: null, liveTimer: null };

  function onState(st) {
    var prev = view.state;
    view.state = st;
    placeHivePanel();
    if (st !== "unlocked") {
      closeSheets();
      closeNetPop();
      var set = $("wui-settings");
      if (set && set.open) set.close();
      selectSegment("cell");
      stopTimers();
      view.lastBal = null;
      view.balOk = false;
    } else if (prev !== "unlocked") {
      startTimers();
    }
  }

  function stopTimers() {
    if (view.netTimer) { clearInterval(view.netTimer); view.netTimer = null; }
    if (view.liveTimer) { clearInterval(view.liveTimer); view.liveTimer = null; }
  }
  function startTimers() {
    stopTimers();
    refreshNet();
    view.netTimer = setInterval(refreshNet, NET_POLL_MS);
    view.liveTimer = setInterval(renderLive, 15000);
  }

  // ------------------------------------------------------------ balance card
  function onBalance(r) {
    var val = $("vault-balance-value"), sub = $("vault-balance-source");
    view.balOk = !!(r && r.ok);
    if (r && r.ok) {
      var bal = Number(r.balance) || 0;
      view.lastBal = { at: new Date(), bal: bal, source: r.source || "" };
      view.balErr = "";
      var txt = bal.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 8 });
      if (val) {
        val.textContent = txt;
        val.classList.toggle("is-long", txt.length > 10);
        val.classList.toggle("is-xlong", txt.length > 15);
      }
      if (sub) {
        var dust = Math.round(bal * 1e8);
        sub.textContent = dust.toLocaleString("en-US") + " dust" + (r.source ? " · " + r.source : "") + " · updated " + timeText(view.lastBal.at);
      }
    } else {
      view.balErr = (r && r.error) || "unavailable";
      if (val) { val.textContent = "—"; val.classList.remove("is-long", "is-xlong"); }
      if (sub) sub.textContent = "Balance unavailable (" + view.balErr + ")" +
        (view.lastBal ? " · last " + fmtCell(view.lastBal.bal) + " CELL at " + timeText(view.lastBal.at) : "");
    }
    renderLive();
  }

  function renderLive() {
    var dot = $("wui-live"), netBal = $("wui-net-bal");
    var fresh = view.balOk && view.lastBal && (Date.now() - view.lastBal.at.getTime() < LIVE_STALE_MS);
    var cls = fresh ? "is-live" : view.balOk ? "is-stale" : (view.balErr ? "is-down" : "");
    var title = fresh ? "Live: balance refreshed at " + timeText(view.lastBal.at)
      : view.balOk ? "Stale: last refresh at " + timeText(view.lastBal.at)
      : view.balErr ? "Balance refresh failed (" + view.balErr + ")" : "Waiting for the first balance refresh";
    if (dot) { dot.className = "wlive " + cls; dot.title = title; dot.setAttribute("aria-label", title); }
    if (netBal) netBal.textContent = view.balOk ? "updated " + timeText(view.lastBal.at) : (view.balErr ? "failed: " + view.balErr : "—");
  }

  // ------------------------------------------------------------ network popover
  async function refreshNet() {
    var api = $("wui-net-api"), tip = $("wui-net-tip"), node = $("wui-net-node"), dot = $("wui-net-dot");
    if (!X) { if (api) api.textContent = "explorer client missing"; return; }
    try {
      var s = await X.api.status();
      if (api) api.innerHTML = '<span class="wok">Online</span>';
      if (tip) tip.textContent = Number(s.chain_tip).toLocaleString("en-US");
      if (node) {
        node.textContent = (s.node_role || "node") + (s.version ? " · " + String(s.version).slice(0, 32) : "");
        node.title = s.version || "";
      }
      if (dot) dot.className = "wapp-icon-dot ok";
    } catch (err) {
      if (api) api.innerHTML = '<span class="wbad">Unreachable</span> <span class="wmuted">' + esc(err.message) + "</span>";
      if (dot) dot.className = "wapp-icon-dot bad";
    }
  }

  function closeNetPop() {
    var pop = $("wui-net-pop"), btn = $("wui-net-btn");
    if (pop) pop.hidden = true;
    if (btn) btn.setAttribute("aria-expanded", "false");
  }

  function bindNetPop() {
    var pop = $("wui-net-pop"), btn = $("wui-net-btn");
    if (!pop || !btn) return;
    btn.addEventListener("click", function (ev) {
      ev.stopPropagation();
      var open = pop.hidden;
      pop.hidden = !open;
      btn.setAttribute("aria-expanded", open ? "true" : "false");
      if (open) { refreshNet(); renderLive(); }
    });
    document.addEventListener("click", function (ev) {
      if (!pop.hidden && !pop.contains(ev.target) && ev.target !== btn) closeNetPop();
    });
    document.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape" && !pop.hidden) { closeNetPop(); btn.focus(); }
    });
  }

  // ------------------------------------------------------------ settings sheet
  function bindSettings() {
    var dlg = $("wui-settings"), btn = $("wui-settings-btn");
    if (!dlg || !btn || typeof dlg.showModal !== "function") return;
    btn.addEventListener("click", function () { closeNetPop(); dlg.showModal(); });
    $("wui-settings-close").addEventListener("click", function () { dlg.close(); });
    dlg.addEventListener("click", function (ev) { if (ev.target === dlg) dlg.close(); }); // backdrop
    var cl = $("wui-settings-checklist");
    if (cl) cl.addEventListener("click", function () { dlg.close(); openChecklist(true); });
  }

  // ------------------------------------------------------------ sheets (receive / send)
  function closeSheets() { openSheet(null); }
  function openSheet(name) {
    ["receive", "send"].forEach(function (n) {
      var sh = $("wui-sheet-" + n), b = $("wui-" + n + "-btn");
      var on = n === name;
      if (sh) sh.hidden = !on;
      if (b) { b.setAttribute("aria-expanded", on ? "true" : "false"); b.classList.toggle("is-on", on); }
    });
    if (!name) return;
    var sh = $("wui-sheet-" + name);
    if (sh && sh.scrollIntoView) sh.scrollIntoView({ block: "nearest", behavior: "smooth" });
    if (name === "send") { var r = $("vault-send-recipient"); if (r) r.focus({ preventScroll: true }); }
  }
  function bindSheets() {
    ["receive", "send"].forEach(function (n) {
      var b = $("wui-" + n + "-btn");
      if (b) b.addEventListener("click", function () {
        var sh = $("wui-sheet-" + n);
        openSheet(sh && !sh.hidden ? null : n);
      });
    });
    document.querySelectorAll("[data-wui-close]").forEach(function (b) {
      b.addEventListener("click", function () {
        var n = b.getAttribute("data-wui-close");
        openSheet(null);
        var t = $("wui-" + n + "-btn"); if (t) t.focus();
      });
    });
    var qt = $("wui-qr-toggle"), qm = $("wui-qr-mini");
    if (qt && qm) qt.addEventListener("click", function () {
      qm.hidden = !qm.hidden;
      qt.setAttribute("aria-expanded", qm.hidden ? "false" : "true");
      qt.classList.toggle("is-on", !qm.hidden);
    });
    var cp = $("vault-unlocked-copy");
    if (cp) cp.addEventListener("click", function () {
      // vault.js copies too; this repeats the (idempotent) write only to learn
      // whether the clipboard accepted it, so the feedback is truthful.
      var label = "Copy Address", addrEl = $("vault-unlocked-addr");
      function feedback(t) {
        cp.textContent = t;
        cp.classList.add("is-done");
        setTimeout(function () { cp.textContent = label; cp.classList.remove("is-done"); }, 1600);
      }
      if (!navigator.clipboard || !addrEl) { feedback("Clipboard unavailable: select the address"); return; }
      navigator.clipboard.writeText(addrEl.textContent).then(function () { feedback("Copied"); },
        function () { feedback("Copy blocked: select the address"); });
    });
  }

  // ------------------------------------------------------------ address + scan
  var current = { addr: null, scanned: 0, tip: 0, found: [], busy: false, token: 0, error: "" };

  function drawQr(el, addr, scale) {
    if (!el || el.getAttribute("data-addr") === addr) return;
    try {
      el.innerHTML = window.QSDMQR.svg(addr, { scale: scale, label: "QR code for address " + addr });
      el.setAttribute("data-addr", addr);
    } catch (e) {
      el.textContent = "QR unavailable";
    }
  }

  function onAddress(addr) {
    if (!addr) return;
    drawQr($("wui-qr"), addr, 5);
    drawQr($("wui-qr-mini"), addr, 4);
    var href = "/explorer.html?address=" + encodeURIComponent(addr);
    ["wui-explorer-link", "wui-onchain-explorer", "wui-mine-explorer", "wui-settings-explorer"].forEach(function (id) { var a = $(id); if (a) a.href = href; });
    if (current.addr !== addr) {
      current = { addr: addr, scanned: 0, tip: 0, found: [], busy: false, token: current.token + 1, error: "" };
      view.histAll = false;
      mining = { addr: addr, nodes: null, error: "" };
      renderHistory();
      loadMining(addr);
    }
    if (current.scanned === 0) scan();
  }

  async function scan() {
    if (!X || !current.addr || current.busy) return;
    var my = current;
    my.busy = true;
    my.error = "";
    var more = $("wui-onchain-more"), note = $("wui-onchain-note");
    if (more) { more.hidden = false; more.disabled = true; more.innerHTML = '<span class="spinner"></span>Scanning…'; }
    renderHistory();
    try {
      if (!my.tip) my.tip = Number((await X.api.status()).chain_tip);
      var to = my.tip - my.scanned, from = Math.max(0, to - SCAN_STEP + 1);
      var rx = await X.api.receipts(from, to);
      if (my !== current) return;
      my.scanned += to - from + 1;
      my.found = my.found.concat(rx.filter(function (r) { return X.tx.involves(r, my.addr); }));
      if (note) note.textContent = "On-chain activity in the last " + my.scanned.toLocaleString("en-US") + " blocks (" +
        Math.max(0, my.tip - my.scanned + 1).toLocaleString("en-US") + "–" + my.tip.toLocaleString("en-US") +
        "), scanned in your browser from the public API, plus sends from this browser. Full history is coming with the indexer.";
    } catch (err) {
      if (my !== current) return;
      my.error = err.message || String(err);
    } finally {
      my.busy = false;
      if (my === current) {
        if (more) {
          more.disabled = false;
          more.textContent = "Scan " + SCAN_STEP.toLocaleString("en-US") + " more blocks";
          more.hidden = !(my.error || (my.scanned < SCAN_MAX && my.tip - my.scanned > 0));
        }
        renderHistory();
        renderMining();
      }
    }
  }

  function onLocalActivity(arr) {
    view.local = Array.isArray(arr) ? arr.slice() : [];
    renderHistory();
  }

  function pill(kind, text) { return '<span class="wpill ' + kind + '">' + esc(text) + "</span>"; }
  function explorerBtn(txId) {
    return '<a class="wbtn-sm" href="/explorer.html?tx=' + encodeURIComponent(txId) + '">View in Explorer</a>';
  }

  function historyItems() {
    var my = current, items = [];
    var seen = {};
    var a = String(my.addr || "").toLowerCase();
    var onchain = my.found.slice().sort(function (x, y) {
      return (Number(y.block_height) - Number(x.block_height)) || ((y.index_in_block || 0) - (x.index_in_block || 0));
    });
    onchain.forEach(function (r) { seen[r.tx_id] = true; });
    // Local sends not (yet) seen in the scanned blocks: pending or failed.
    view.local.forEach(function (e) {
      if (!e || seen[e.tx_id]) return;
      var st = e.status === "failed" ? "failed" : "pending";
      items.push({
        kind: st, txId: e.tx_id || "", dir: "out", amount: e.amount,
        meta: "Sent from this browser · to " + shortHash(e.recipient, 8, 6) + " · " + new Date(e.timestamp || Date.now()).toLocaleString(),
        title: e.recipient || ""
      });
    });
    onchain.forEach(function (r) {
      var p = X.tx.parties(r);
      var to = String(p.to || "").toLowerCase(), from = String(p.from || "").toLowerCase();
      var dir = to === a && from === a ? "self" : to === a ? "in" : "out";
      var other = dir === "in" ? p.from : p.to;
      var otherTxt = other === X.SYSTEM_FUNDER ? "system funder" : shortHash(other, 8, 6);
      var label = X.tx.KIND_LABEL[X.tx.kind(r)] || "Transfer";
      items.push({
        kind: "confirmed", block: Number(r.block_height), txId: r.tx_id, dir: dir, amount: p.amount,
        meta: label + " · " + (dir === "in" ? "from " : "to ") + otherTxt + " · " + X.fmt.ageText(r.timestamp),
        title: other || ""
      });
    });
    return items;
  }

  function renderHistory() {
    var box = $("wui-history");
    if (!box) return;
    var my = current;
    var items = (X && my.addr) ? historyItems() : [];
    var head = "";
    if (my.busy && !my.scanned) head = '<p class="wui-fine"><span class="spinner"></span>Scanning the last ' + SCAN_STEP.toLocaleString("en-US") + " blocks…</p>";
    else if (my.error) head = '<p class="wui-fine wbad">Could not read recent blocks: ' + esc(my.error) + "</p>";
    if (!items.length) {
      box.innerHTML = head || (my.scanned ? '<p class="whist-empty">No transactions to or from this address in the scanned blocks.</p>'
        : '<p class="whist-empty">No transactions yet.</p>');
      return;
    }
    var shown = view.histAll ? items : items.slice(0, HIST_PAGE);
    box.innerHTML = head + shown.map(function (it) {
      var p = it.kind === "confirmed" ? pill("ok", "Confirmed (Block " + it.block + ")")
        : it.kind === "failed" ? pill("bad", "Failed") : pill("wait", "Pending");
      var sign = it.dir === "in" ? "+" : it.dir === "out" ? "−" : "";
      return '<div class="whist-item is-' + it.kind + '">' +
        '<div class="whist-top">' + p + '<span class="whist-amt ' + it.dir + '">' + sign + esc(fmtCell(it.amount)) + " CELL</span></div>" +
        '<div class="whist-meta" title="' + esc(it.title) + '">' + esc(it.meta) + "</div>" +
        '<div class="whist-bot"><span class="whist-hash mono" title="' + esc(it.txId) + '">' + esc(shortHash(it.txId, 10, 8)) + "</span>" +
        (it.kind !== "failed" && it.txId ? explorerBtn(it.txId) : "") + "</div></div>";
    }).join("") +
      (items.length > shown.length ? '<button type="button" class="wui-linkbtn whist-all" id="wui-hist-all">Show all ' + items.length + " entries</button>" : "");
    var all = $("wui-hist-all");
    if (all) all.addEventListener("click", function () { view.histAll = true; renderHistory(); });
  }

  function onSent() {
    // A new transfer was accepted: rescan from the top so it shows up once mined.
    if (current.addr) {
      current = { addr: current.addr, scanned: 0, tip: 0, found: [], busy: false, token: current.token + 1, error: "" };
      setTimeout(function () { if (view.state === "unlocked") scan(); }, 4000);
    }
  }

  // ------------------------------------------------------------ mining rewards card
  var mining = { addr: null, nodes: null, error: "" };

  async function loadMining(addr) {
    if (!X) return;
    renderMining();
    try {
      var en = await X.api.enrollments();
      if (mining.addr !== addr) return;
      var a = addr.toLowerCase();
      mining.nodes = (en.records || []).filter(function (r) { return String(r.owner || "").toLowerCase() === a; });
    } catch (err) {
      if (mining.addr !== addr) return;
      mining.error = err.message || String(err);
    }
    renderMining();
  }

  function renderMining() {
    var box = $("wui-mine");
    if (!box) return;
    var html = "";
    if (mining.error) html += '<p class="wui-fine wbad">Enrollments unavailable: ' + esc(mining.error) + "</p>";
    else if (mining.nodes === null) html += '<p class="wui-fine"><span class="spinner"></span>Loading enrollments…</p>';
    else {
      var n = mining.nodes.length;
      html += '<div class="wmine-stats"><div><span class="wmine-num">' + n + '</span><span class="wmine-lbl">enrolled node' + (n === 1 ? "" : "s") + "</span></div>";
      var a = String(current.addr || "").toLowerCase();
      var rewards = X ? current.found.filter(function (r) {
        return X.tx.kind(r) === "reward" && String(X.tx.parties(r).to || "").toLowerCase() === a;
      }) : [];
      var sum = rewards.reduce(function (s, r) { return s + (Number(X.tx.parties(r).amount) || 0); }, 0);
      html += '<div><span class="wmine-num">' + (current.scanned ? rewards.length : "…") + '</span><span class="wmine-lbl">rewards in last ' +
        (current.scanned ? current.scanned.toLocaleString("en-US") : "…") + " blocks" + (rewards.length ? " · " + esc(fmtCell(Math.round(sum * 1e8) / 1e8)) + " CELL" : "") + "</span></div></div>";
      if (n) {
        html += '<ul class="wmine-nodes">' + mining.nodes.slice(0, 4).map(function (r) {
          var phase = String(r.phase || "unknown");
          return '<li><span class="mono" title="' + esc(r.gpu_uuid || "") + '">' + esc(r.node_id) + "</span>" +
            pill(phase === "active" ? "ok" : "wait", phase + (r.fully_bonded ? "" : " · bonding")) + "</li>";
        }).join("") + (n > 4 ? '<li class="wmuted">+' + (n - 4) + " more in the explorer</li>" : "") + "</ul>";
      } else {
        html += '<p class="wui-fine">No mining nodes are enrolled with this address as owner. Mining runs in <a href="/download.html">QSDM Hive</a>.</p>';
      }
    }
    box.innerHTML = html;
  }

  // ------------------------------------------------------------ Hive bridge
  var hiveHome = null; // where wallet-provider.js put its panel

  function hivePanel() { return $("qsdm-hive-provider-panel"); }
  function hiveConnected() {
    var d = $("hive-provider-disconnect");
    return !!(d && !d.hidden);
  }

  function placeHivePanel() {
    var panel = hivePanel(), pane = $("wui-pane-hive");
    if (!panel || !pane) return;
    if (!hiveHome) hiveHome = { parent: panel.parentNode, next: panel.nextSibling };
    if (view.state === "unlocked") {
      if (panel.parentNode !== pane) pane.appendChild(panel);
    } else if (panel.parentNode === pane && hiveHome.parent) {
      hiveHome.parent.insertBefore(panel, hiveHome.next && hiveHome.next.parentNode === hiveHome.parent ? hiveHome.next : null);
    }
  }

  var hiveClicked = false;
  function updateHive() {
    var on = hiveConnected();
    var seg = $("wui-seg-hive");
    if (seg) {
      seg.disabled = !on;
      seg.title = on ? "QSDM Hive wallet" : "Connect QSDM Hive";
    }
    if (!on && seg && seg.classList.contains("active")) selectSegment("cell");
    var btn = $("wui-hive-connect"), text = $("wui-hive-text"), note = $("wui-hive-note"), badge = $("wui-hive-badge");
    var conn = $("hive-provider-connect"), addrEl = $("hive-provider-address"), notice = $("hive-provider-notice");
    if (btn) {
      if (on) {
        btn.textContent = "Open Hive wallet";
        btn.disabled = false;
      } else {
        btn.textContent = "Connect QSDM Hive";
        btn.disabled = !!(conn && conn.disabled && hiveClicked);
      }
    }
    if (badge) { badge.textContent = on ? "Connected" : "Desktop"; badge.classList.toggle("ok", on); }
    if (text) text.textContent = on
      ? "Connected as " + ((addrEl && addrEl.textContent) || "your Hive wallet") + ". Transfers are signed in the QSDM Hive desktop app; your keys stay in Hive."
      : "Sign CELL transfers with the QSDM Hive desktop app; your keys stay in Hive and this page only sees the address you approve.";
    if (note) {
      var msg = notice ? notice.textContent : "";
      note.hidden = !(msg && (hiveClicked || on));
      note.textContent = msg;
      note.setAttribute("data-state", notice ? (notice.getAttribute("data-state") || "info") : "info");
    }
  }

  function bindHive() {
    var btn = $("wui-hive-connect");
    if (btn) btn.addEventListener("click", function () {
      if (hiveConnected()) { selectSegment("hive"); return; }
      hiveClicked = true;
      var conn = $("hive-provider-connect");
      if (!conn) { location.href = "/wallet-start.html?login=new"; return; }
      if (conn.disabled) {
        var note = $("wui-hive-note");
        if (note) { note.hidden = false; note.textContent = "Still looking for the QSDM Wallet extension; try again in a moment."; }
        return;
      }
      conn.click(); // wallet-provider.js: connect, or open the Hive onboarding page
      updateHive();
    });
    var segs = { cell: $("wui-seg-cell"), hive: $("wui-seg-hive") };
    Object.keys(segs).forEach(function (k) {
      if (segs[k]) segs[k].addEventListener("click", function () { if (!segs[k].disabled) selectSegment(k); });
    });
    var tablist = document.querySelector(".wapp-seg");
    if (tablist) tablist.addEventListener("keydown", function (ev) {
      if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight") return;
      var next = segs.cell.classList.contains("active") ? "hive" : "cell";
      if (segs[next] && !segs[next].disabled) { selectSegment(next); segs[next].focus(); ev.preventDefault(); }
    });
    attachHiveObserver(0);
  }

  function attachHiveObserver(tries) {
    var panel = hivePanel();
    if (!panel) {
      if (tries < 40) setTimeout(function () { attachHiveObserver(tries + 1); }, 250);
      return;
    }
    placeHivePanel();
    new MutationObserver(updateHive).observe(panel, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["hidden", "disabled", "data-state"] });
    updateHive();
  }

  function selectSegment(name) {
    var cell = $("wui-seg-cell"), hive = $("wui-seg-hive");
    var pc = $("wui-pane-cell"), ph = $("wui-pane-hive");
    if (!cell || !hive) return;
    var isHive = name === "hive";
    cell.classList.toggle("active", !isHive); cell.setAttribute("aria-selected", isHive ? "false" : "true");
    hive.classList.toggle("active", isHive); hive.setAttribute("aria-selected", isHive ? "true" : "false");
    cell.tabIndex = isHive ? -1 : 0; hive.tabIndex = isHive ? 0 : -1;
    if (pc) pc.hidden = isHive;
    if (ph) ph.hidden = !isHive;
    if (isHive) closeSheets();
  }

  // ------------------------------------------------------------ send review
  function highlightAddr(addr) {
    var s = String(addr);
    if (s.length < 16) return esc(s);
    return "<mark>" + esc(s.slice(0, 6)) + "</mark>" + esc(s.slice(6, -6)) + "<mark>" + esc(s.slice(-6)) + "</mark>";
  }

  function reviewSend(d) {
    var dlg = $("wui-review");
    if (!dlg || typeof dlg.showModal !== "function") {
      return Promise.resolve(window.confirm("Send " + d.amount + " CELL (fee " + d.fee + ") to " + d.to + "?"));
    }
    var total = Math.round((Number(d.amount) + Number(d.fee)) * 1e8) / 1e8;
    $("wui-rv-to").innerHTML = '<span class="addr-full">' + highlightAddr(d.to) + '</span><span class="sub">Compare the highlighted first and last 6 characters with the address you were given.</span>';
    $("wui-rv-amount").textContent = fmtCell(d.amount) + " CELL";
    $("wui-rv-fee").textContent = fmtCell(d.fee) + " CELL";
    $("wui-rv-total").textContent = fmtCell(total) + " CELL";
    $("wui-rv-nonce").innerHTML = d.nonce > 0 ? esc(String(d.nonce)) + '<span class="sub">next nonce reported by the network for your address</span>'
      : 'auto<span class="sub">the network did not report a nonce; the validator assigns it</span>';
    $("wui-rv-from").innerHTML = '<span class="addr-full">' + esc(X ? X.fmt.short(d.from, 10, 8) : d.from) + "</span>";
    $("wui-rv-geotag").textContent = d.geotag || "—";
    var warn = $("wui-rv-warn");
    var warnings = [];
    if (String(d.to).toLowerCase() === String(d.from).toLowerCase()) warnings.push("You are sending to your own address.");
    warn.textContent = warnings.join(" ");
    warn.hidden = !warnings.length;
    var balEl = $("wui-rv-balance");
    balEl.textContent = "—";
    if (X) {
      X.api.balance(d.from).then(function (b) {
        var bal = Number(b.balance);
        if (!isFinite(bal)) return;
        balEl.textContent = bal >= total
          ? fmtCell(bal) + " CELL → " + fmtCell(Math.round((bal - total) * 1e8) / 1e8) + " CELL after"
          : fmtCell(bal) + " CELL (not enough)";
        if (bal < total) {
          warn.hidden = false;
          warn.className = "wui-warn err";
          warn.textContent = (warn.textContent ? warn.textContent + " " : "") + "Amount plus fee is more than the current balance; the network will reject it.";
        }
      }).catch(function () {});
    }
    return new Promise(function (resolve) {
      var ok = $("wui-rv-ok"), cancel = $("wui-rv-cancel");
      function done(result) {
        ok.removeEventListener("click", onOk);
        cancel.removeEventListener("click", onCancel);
        dlg.removeEventListener("cancel", onEsc);
        dlg.removeEventListener("close", onClose);
        if (dlg.open) dlg.close();
        warn.className = "wui-warn";
        resolve(result);
      }
      function onOk() { done(true); }
      function onCancel() { done(false); }
      function onEsc(ev) { ev.preventDefault(); done(false); }
      function onClose() { done(false); }
      ok.addEventListener("click", onOk);
      cancel.addEventListener("click", onCancel);
      dlg.addEventListener("cancel", onEsc);
      dlg.addEventListener("close", onClose);
      dlg.showModal();
      cancel.focus(); // safer default focus
    });
  }

  // ------------------------------------------------------------ boot
  function boot() {
    bindChecklist();
    renderUrlCheck();
    openChecklist(false);
    bindNetPop();
    bindSettings();
    bindSheets();
    bindHive();
    var more = $("wui-onchain-more");
    if (more) more.addEventListener("click", scan);
  }

  window.QSDMWalletUI = {
    onAddress: onAddress, onState: onState, onBalance: onBalance, onLocalActivity: onLocalActivity,
    reviewSend: reviewSend, onSent: onSent, openChecklist: openChecklist
  };

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
