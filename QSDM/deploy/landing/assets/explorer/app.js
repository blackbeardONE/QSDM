/* QSDM explorer single-page app.
 * Routes:
 *   ?block=H | ?height=H      block page
 *   ?tx=ID | ?tx_id=ID        transaction page
 *   ?address=A                address page
 *   ?q=...                    search
 *   #/blocks #/txs #/miners #/stats   list / stats views
 *   (none) or #/              home
 * Everything is read from the public /api/v1 read endpoints; there is no
 * indexer yet, so history-style views are bounded client-side scans. */
(function () {
  "use strict";

  var X = window.QSDMX;
  var api = X.api, f = X.fmt, T = X.tx;
  var esc = f.esc, DASH = f.DASH;

  var REFRESH_MS = 10000;
  var HOME_BLOCKS = 10, HOME_TXS = 10;
  var AVG_WINDOW = 64;
  var RECENT_RX_WINDOW = 200;      // blocks of receipts kept fresh on home
  var ADDRESS_SCAN_STEP = 1000;    // blocks per address-scan click
  var ADDRESS_SCAN_MAX = 10000;
  var BLOCKS_PAGE = 50;
  var TXS_PAGE = 200;              // heights per "load more" on #/txs
  var STATS_WINDOW = 500;

  var view = document.getElementById("xp-view");
  var liveEl = document.getElementById("xp-live");
  var routeToken = 0;
  var refreshTimer = null;

  // ------------------------------------------------------------------ helpers
  function blockHref(h) { return "/explorer.html?block=" + encodeURIComponent(h); }
  function txHref(id) { return "/explorer.html?tx=" + encodeURIComponent(id); }
  function addrHref(a) { return "/explorer.html?address=" + encodeURIComponent(a); }

  function blockLink(h) {
    if (h === null || h === undefined || h === "") return '<span class="muted">' + DASH + "</span>";
    return '<a class="mono" href="' + blockHref(h) + '">' + f.int(h) + "</a>";
  }
  function addrCell(a, opts) {
    if (!a) return '<span class="muted">' + DASH + "</span>";
    if (a === X.SYSTEM_FUNDER) return '<span class="tag tag-sys" title="' + esc(a) + '">System funder</span>';
    return f.hash(a, Object.assign({ href: addrHref(a), head: 8, tail: 6, label: "address" }, opts || {}));
  }
  var KIND_SHORT = { reward: "Reward", heartbeat: "Heartbeat", system: "System", contract: "Contract", transfer: "Transfer" };
  function kindBadge(kind, compact) {
    var label = compact ? (KIND_SHORT[kind] || kind) : (T.KIND_LABEL[kind] || kind);
    return '<span class="badge badge-' + esc(kind) + '" title="' + esc(T.KIND_LABEL[kind] || kind) + '">' + esc(label) + "</span>";
  }
  function rewardTo(rw, copy) {
    if (!rw) return '<span class="muted" title="No mining reward in this block (heartbeat only)">no reward</span>';
    var extra = rw.recips && rw.recips.size > 1 ? ' <span class="muted small" title="Reward split between ' + rw.recips.size + ' addresses">+' + (rw.recips.size - 1) + "</span>" : "";
    return addrCell(rw.to, { copy: !!copy }) + extra;
  }
  function statusBadge(s) {
    if (s === null || s === undefined) return '<span class="badge">unknown</span>';
    return Number(s) === 1 ? '<span class="badge badge-ok">✓ Success</span>'
      : '<span class="badge badge-err">✗ Failed (' + esc(s) + ")</span>";
  }
  function amountCell(n) {
    if (n === null || n === undefined) return '<span class="muted">' + DASH + "</span>";
    return '<span class="mono num">' + f.cell(n) + '</span> <span class="unit">CELL</span>';
  }
  function errBox(err) {
    var msg = (err && err.message) || String(err);
    if (err && err.status === 429) msg = "The public API asked us to slow down. " + msg;
    return '<div class="errbox" role="alert">' + esc(msg) + "</div>";
  }
  function loading(label) {
    return '<div class="loading"><span class="spinner"></span>' + esc(label || "Loading…") + "</div>";
  }
  function setTitle(t) { document.title = (t ? t + " · " : "") + "QSDM Explorer"; }
  function stale(token) { return token !== routeToken; }

  function setActiveTab(name) {
    document.querySelectorAll(".xp-tabs a[data-tab]").forEach(function (a) {
      if (a.getAttribute("data-tab") === name) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
  }

  function setLive(state) {
    if (!liveEl) return;
    liveEl.dataset.state = state;
    liveEl.textContent = state === "live" ? "Live · refreshes every 10 s"
      : state === "paused" ? "Paused while this tab is hidden"
      : state === "error" ? "Live updates interrupted — retrying"
      : "";
  }

  function stopRefresh() {
    if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null; }
  }

  function rewardByHeight(receiptList) {
    var m = new Map();
    receiptList.forEach(function (r) {
      if (T.kind(r) !== "reward") return;
      var p = T.parties(r);
      var h = Number(r.block_height);
      var cur = m.get(h) || { amount: 0, to: p.to, count: 0, recips: new Set() };
      cur.amount += Number(p.amount) || 0;
      cur.count++;
      if (p.to) cur.recips.add(p.to);
      m.set(h, cur);
      if (!cur.to) cur.to = p.to;
    });
    return m;
  }

  // ------------------------------------------------------------------ search
  function classify(q) {
    q = String(q || "").trim();
    try {
      var u = new URL(q);
      var p = u.searchParams;
      if (p.get("block") || p.get("height")) return { type: "block", value: p.get("block") || p.get("height") };
      if (p.get("tx") || p.get("tx_id")) return { type: "tx", value: p.get("tx") || p.get("tx_id") };
      if (p.get("address")) return { type: "address", value: p.get("address") };
      if (p.get("q")) q = p.get("q");
    } catch (_) { /* not a URL */ }
    q = q.replace(/^qsdm:(\/\/)?/i, "").trim();
    if (!q) return { type: "empty" };
    if (/^#?\d{1,12}$/.test(q.replace(/,/g, ""))) return { type: "block", value: q.replace(/[#,]/g, "") };
    if (/^(solo-|tx-)/i.test(q) || /-/.test(q)) return { type: "tx", value: q };
    if (/^[0-9a-fA-F]{64}$/.test(q)) return { type: "hex64", value: q.toLowerCase() };
    if (q === X.SYSTEM_FUNDER || /^[a-zA-Z0-9]{32,128}$/.test(q)) return { type: "maybe", value: q };
    return { type: "unknown", value: q };
  }

  async function runSearch(raw) {
    var c = classify(raw);
    var input = document.getElementById("xp-q");
    if (c.type === "empty") return;
    if (c.type === "block") return navigate(blockHref(c.value));
    if (c.type === "tx") return navigate(txHref(c.value));
    if (c.type === "unknown") {
      renderSearchHelp(c.value);
      return;
    }
    // 64-hex (or other long id): could be a tx id, a recent block hash, or an address.
    var token = ++routeToken;
    stopRefresh();
    view.innerHTML = loading("Resolving " + f.short(c.value, 10) + "…");
    var hd = api.findHeaderByHash(c.value);
    if (hd) return navigate(blockHref(hd.height));
    try {
      await api.receipt(c.value);
      if (stale(token)) return;
      return navigate(txHref(c.value));
    } catch (err) {
      if (stale(token)) return;
      if (err.status && err.status !== 404) { view.innerHTML = errBox(err); return; }
    }
    if (input) input.value = c.value;
    navigate(addrHref(c.value), { note: "No transaction or recent block has this id, so it is shown as an address. Block lookup by hash is not available yet: only the " + f.int(api.cachedHeaderCount()) + " block headers this page has already loaded were checked." });
  }

  function renderSearchHelp(q) {
    stopRefresh();
    view.innerHTML =
      '<section class="card"><h2>Nothing matched “' + esc(q) + '”</h2>' +
      "<p>Search accepts:</p><ul class=\"plain\">" +
      "<li><strong>Block height</strong> — a number, e.g. <code>749000</code></li>" +
      "<li><strong>Transaction id</strong> — e.g. <code>solo-reward-…</code> or a wallet tx id</li>" +
      "<li><strong>Address</strong> — 64 hex characters, <code>hex(sha256(public_key))</code></li>" +
      "<li><strong>Block hash</strong> — only for blocks this page has already loaded; full hash lookup needs the indexer</li>" +
      "</ul></section>";
  }

  // ------------------------------------------------------------------ home
  var homeState = { hideHeartbeats: true };

  function renderHomeSkeleton() {
    view.innerHTML =
      '<section class="tiles" aria-label="Network statistics">' +
      tile("height", "Block height", "chain tip") +
      tile("age", "Latest block", "since last seal") +
      tile("avg", "Avg block time", "last " + AVG_WINDOW + " blocks · target 10 s") +
      tile("reward", "Block reward", "CELL per sealed block", false, true) +
      tile("emitted", "Emitted", "of 90,000,000 CELL cap", true, true) +
      tile("halving", "Next halving", "block height") +
      tile("miners", "Enrolled miners", "active / total enrollments") +
      tile("rewarded", "Rewarded addresses", "distinct, last " + RECENT_RX_WINDOW + " blocks") +
      "</section>" +
      '<div class="cols">' +
      '<section class="card"><div class="card-head"><h2>Latest blocks</h2><a class="more" href="/explorer.html#/blocks">View all →</a></div><div id="home-blocks">' + loading() + "</div></section>" +
      '<section class="card"><div class="card-head"><h2>Latest transactions</h2>' +
      '<label class="toggle"><input type="checkbox" id="hide-hb"' + (homeState.hideHeartbeats ? " checked" : "") + '> Hide heartbeats</label>' +
      '<a class="more" href="/explorer.html#/txs">View all →</a></div><div id="home-txs">' + loading() + "</div></section>" +
      "</div>";
    document.getElementById("hide-hb").addEventListener("change", function (e) {
      homeState.hideHeartbeats = e.target.checked;
      refreshHome(routeToken, true);
    });
  }

  // CELL coin mark for tiles whose value is a CELL amount.
  var CELL_ICON = '<img class="cell-ico" src="/assets/brand/cell-coin-small.svg" alt="" width="14" height="14" />';

  function tile(key, label, sub, bar, coin) {
    return '<div class="tile"><div class="tile-label">' + (coin ? CELL_ICON : "") + esc(label) + '</div>' +
      '<div class="tile-value mono" data-t="' + key + '">' + DASH + "</div>" +
      (bar ? '<div class="meter" aria-hidden="true"><span data-t="' + key + '-bar"></span></div>' : "") +
      '<div class="tile-sub" data-t="' + key + '-sub">' + esc(sub) + "</div></div>";
  }

  function setTile(key, html, sub) {
    var el = view.querySelector('[data-t="' + key + '"]');
    if (el) el.innerHTML = html;
    if (sub !== undefined) {
      var s = view.querySelector('[data-t="' + key + '-sub"]');
      if (s) s.innerHTML = sub;
    }
  }

  async function refreshHome(token, quick) {
    if (stale(token)) return;
    try {
      var st = await api.status();
      if (stale(token)) return;
      var tip = Number(st.chain_tip);
      var tk = st.tokenomics || {};
      setTile("height", f.int(tip));
      setTile("reward", esc(tk.block_reward_cell || f.cell(f.dustToCell(tk.block_reward_dust))) + ' <span class="unit">CELL</span>',
        "epoch " + f.int(tk.current_epoch) + " · CELL per sealed block");
      var emitted = f.dustToCell(tk.emitted_dust), cap = f.dustToCell(tk.cap_dust);
      if (emitted !== null && cap) {
        var pct = emitted / cap * 100;
        setTile("emitted", f.cell(emitted, { dp: 0 }) + ' <span class="unit">CELL</span>',
          pct.toFixed(2) + "% of " + f.cell(cap, { dp: 0 }) + " CELL cap");
        var bar = view.querySelector('[data-t="emitted-bar"]');
        if (bar) bar.style.width = Math.max(0.5, Math.min(100, pct)) + "%";
      }
      setTile("halving", f.int(tk.next_halving_height), "in ~" + f.duration(tk.next_halving_eta_seconds));

      var hdrs = await api.headers(Math.max(0, tip - AVG_WINDOW + 1), tip);
      if (stale(token)) return;
      if (hdrs.length) {
        setTile("age", f.age(hdrs[0].timestamp), "block " + f.int(hdrs[0].height) + ' · <span title="' + esc(f.utc(hdrs[0].timestamp)) + '">' + esc(f.utc(hdrs[0].timestamp).slice(11)) + "</span>");
        var first = hdrs[hdrs.length - 1], last = hdrs[0];
        var span = (f.toDate(last.timestamp) - f.toDate(first.timestamp)) / 1000;
        var n = Number(last.height) - Number(first.height);
        if (n > 0) setTile("avg", (span / n).toFixed(2) + ' <span class="unit">s</span>');
      }
      var rx = await api.receipts(Math.max(0, tip - RECENT_RX_WINDOW + 1), tip);
      if (stale(token)) return;
      var rewards = rewardByHeight(rx);
      var rewarded = new Set();
      rewards.forEach(function (v) { if (v.to) rewarded.add(v.to); });
      setTile("rewarded", f.int(rewarded.size), "distinct, last " + RECENT_RX_WINDOW + " blocks · " + f.int(rewards.size) + " rewarded blocks");

      renderHomeBlocks(hdrs.slice(0, HOME_BLOCKS), rewards);
      renderHomeTxs(rx);
      setLive(document.hidden ? "paused" : "live");

      if (!quick) {
        api.enrollments().then(function (en) {
          if (stale(token)) return;
          var active = en.records.filter(function (r) { return r.phase === "active"; }).length;
          setTile("miners", f.int(active) + ' <span class="unit">/ ' + f.int(en.total) + "</span>", '<a href="/explorer.html#/miners">active / total enrollments →</a>');
        }).catch(function () { setTile("miners", DASH, "enrollments unavailable"); });
      }
    } catch (err) {
      if (stale(token)) return;
      setLive("error");
      var hb = document.getElementById("home-blocks");
      if (hb && hb.querySelector(".loading")) hb.innerHTML = errBox(err);
      var ht = document.getElementById("home-txs");
      if (ht && ht.querySelector(".loading")) ht.innerHTML = errBox(err);
    }
  }

  function renderHomeBlocks(hdrs, rewards) {
    var root = document.getElementById("home-blocks");
    if (!root) return;
    if (!hdrs.length) { root.innerHTML = '<p class="muted">No blocks yet.</p>'; return; }
    root.innerHTML = '<div class="table-wrap"><table class="xt"><thead><tr><th>Block</th><th>Age</th><th>Reward to</th><th class="r">Reward</th><th class="r">Txs</th></tr></thead><tbody>' +
      hdrs.map(function (b) {
        var rw = rewards.get(Number(b.height));
        return "<tr><td>" + blockLink(b.height) + "</td><td>" + f.age(b.timestamp) + "</td>" +
          "<td>" + rewardTo(rw) + "</td>" +
          '<td class="r">' + (rw ? amountCell(rw.amount) : '<span class="muted">' + DASH + "</span>") + "</td>" +
          '<td class="r mono">' + f.int(b.tx_count) + "</td></tr>";
      }).join("") + "</tbody></table></div>";
  }

  function renderHomeTxs(rx) {
    var root = document.getElementById("home-txs");
    if (!root) return;
    var list = homeState.hideHeartbeats ? rx.filter(function (r) { return T.kind(r) !== "heartbeat"; }) : rx;
    list = list.slice(0, HOME_TXS);
    if (!list.length) { root.innerHTML = '<p class="muted">No matching transactions in the last ' + RECENT_RX_WINDOW + " blocks.</p>"; return; }
    root.innerHTML = '<div class="table-wrap"><table class="xt"><thead><tr><th>Tx</th><th>Type</th><th>From → To</th><th class="r">Amount</th><th>Age</th></tr></thead><tbody>' +
      list.map(txRowCompact).join("") + "</tbody></table></div>";
  }

  function txRowCompact(r) {
    var p = T.parties(r);
    var from = p.from === X.SYSTEM_FUNDER ? '<span class="tag tag-sys" title="' + esc(p.from) + '">System</span>' : addrCell(p.from, { copy: false, head: 6, tail: 4 });
    return "<tr><td>" + f.hash(r.tx_id, { href: txHref(r.tx_id), head: 6, tail: 4, copy: false }) + "</td>" +
      "<td>" + kindBadge(T.kind(r), true) + "</td>" +
      '<td class="ft">' + from + ' <span class="arrow">→</span> ' + addrCell(p.to, { copy: false, head: 6, tail: 4 }) + "</td>" +
      '<td class="r">' + amountCell(p.amount) + "</td><td>" + f.age(r.timestamp) + "</td></tr>";
  }

  function scheduleHome(token) {
    stopRefresh();
    if (document.hidden) { setLive("paused"); return; }
    refreshTimer = setTimeout(function () {
      refreshHome(token).then(function () { if (!stale(token)) scheduleHome(token); });
    }, REFRESH_MS);
  }

  document.addEventListener("visibilitychange", function () {
    if (currentRoute.name !== "home") return;
    if (document.hidden) { stopRefresh(); setLive("paused"); }
    else { var t = routeToken; refreshHome(t, true).then(function () { scheduleHome(t); }); }
  });

  function showHome() {
    setTitle("");
    setActiveTab("home");
    renderHomeSkeleton();
    var t = routeToken;
    refreshHome(t).then(function () { scheduleHome(t); });
  }

  // ------------------------------------------------------------------ block page
  async function showBlock(hRaw, token) {
    setActiveTab("blocks");
    var h = Number(String(hRaw).replace(/[,#\s]/g, ""));
    if (!Number.isInteger(h) || h < 0) { view.innerHTML = errBox(new Error("Block height must be a whole number.")); return; }
    setTitle("Block " + f.int(h));
    view.innerHTML = loading("Loading block " + f.int(h) + "…");
    try {
      var st = await api.status();
      if (stale(token)) return;
      var tip = Number(st.chain_tip);
      if (h > tip) {
        view.innerHTML = '<section class="card"><h2>Block ' + f.int(h) + " has not been produced yet</h2>" +
          "<p>The chain tip is " + blockLink(tip) + ". At ~10 s per block that is about " + f.duration((h - tip) * 10) + " away.</p></section>";
        return;
      }
      var res = await Promise.all([api.block(h), api.headers(h, h).catch(function () { return []; })]);
      if (stale(token)) return;
      var b = res[0], hd = res[1][0] || {};
      if (!b) { view.innerHTML = errBox(new Error("Block " + h + " was not returned by the API.")); return; }
      var txs = b.transactions || [];
      var rw = { amount: 0, to: null, recips: new Set() };
      txs.forEach(function (tx) { if (T.kind(tx) === "reward") { rw.amount += Number(tx.Amount) || 0; rw.to = rw.to || tx.Recipient; rw.recips.add(tx.Recipient); } });
      var raw = JSON.parse(JSON.stringify(b));
      view.innerHTML =
        '<nav class="crumbs"><a href="/explorer.html#/blocks">Blocks</a> / <span class="mono">' + f.int(h) + "</span></nav>" +
        '<section class="card"><div class="card-head"><h2>Block <span class="mono">#' + f.int(h) + "</span></h2>" +
        '<div class="pager">' +
        (h > 0 ? '<a class="btn-sm" href="' + blockHref(h - 1) + '" aria-label="Previous block">← ' + f.int(h - 1) + "</a>" : "") +
        (h < tip ? '<a class="btn-sm" href="' + blockHref(h + 1) + '" aria-label="Next block">' + f.int(h + 1) + " →</a>" : '<span class="btn-sm disabled">tip</span>') +
        "</div></div>" +
        '<dl class="kv">' +
        kv("Height", '<span class="mono">' + f.int(b.height) + '</span> <span class="muted">· ' + f.int(tip - h + 1) + " confirmations</span>") +
        kv("Timestamp", f.age(b.timestamp) + ' <span class="muted mono">' + esc(f.utc(b.timestamp)) + "</span>") +
        kv("Block hash", f.hash(b.hash, { full: true, label: "block hash" })) +
        kv("Parent", h > 0 ? f.hash(b.prev_hash, { full: true, href: blockHref(h - 1), label: "parent hash" }) : '<span class="muted">genesis</span>') +
        kv("Producer", f.hash(b.producer_id, { head: 12, tail: 8, label: "producer id" }) + ' <span class="muted">validator</span>') +
        kv("Transactions", '<span class="mono">' + f.int(txs.length) + "</span>") +
        kv("Mining reward", rw.amount ? amountCell(rw.amount) + " → " + rewardTo(rw, true) : '<span class="muted">none (heartbeat block)</span>') +
        kv("Total fees", amountCell(b.total_fees)) +
        kv("Gas used", '<span class="mono">' + f.int(b.gas_used) + "</span>") +
        kv("State root", f.hash(b.state_root, { full: true, label: "state root" })) +
        (hd.tx_root ? kv("Tx root", f.hash(hd.tx_root, { full: true, label: "tx root" })) : "") +
        "</dl></section>" +
        '<section class="card"><div class="card-head"><h2>Transactions <span class="count">' + f.int(txs.length) + "</span></h2></div>" +
        (txs.length ? '<div class="table-wrap"><table class="xt"><thead><tr><th>Tx id</th><th>Type</th><th>From</th><th>To</th><th class="r">Amount</th><th class="r">Fee</th><th class="r">Nonce</th></tr></thead><tbody>' +
          txs.map(function (tx) {
            return "<tr><td>" + f.hash(tx.ID, { href: txHref(tx.ID), head: 12, tail: 6, label: "tx id" }) + "</td><td>" + kindBadge(T.kind(tx)) + "</td>" +
              "<td>" + addrCell(tx.Sender) + "</td><td>" + addrCell(tx.Recipient) + "</td>" +
              '<td class="r">' + amountCell(tx.Amount) + '</td><td class="r">' + amountCell(tx.Fee) + '</td><td class="r mono">' + f.int(tx.Nonce) + "</td></tr>";
          }).join("") + "</tbody></table></div>" : '<p class="muted">No transactions in this block.</p>') +
        "</section>" +
        rawToggle("Raw block JSON", raw);
    } catch (err) {
      if (stale(token)) return;
      view.innerHTML = errBox(err);
    }
  }

  function kv(k, v) { return "<dt>" + esc(k) + "</dt><dd>" + v + "</dd>"; }

  function rawToggle(label, obj) {
    return '<details class="card raw"><summary>' + esc(label) + "</summary><pre>" + esc(JSON.stringify(obj, null, 2)) + "</pre></details>";
  }

  // ------------------------------------------------------------------ tx page
  async function showTx(id, token) {
    setActiveTab("txs");
    setTitle("Transaction " + f.short(id, 10));
    view.innerHTML = loading("Loading transaction…");
    try {
      var r;
      try {
        r = await api.receipt(id);
      } catch (err) {
        if (stale(token)) return;
        if (err.status === 404) {
          view.innerHTML = '<section class="card"><h2>Transaction not found</h2>' +
            '<p>No receipt exists for <span class="mono">' + esc(id) + "</span>.</p>" +
            "<p class=\"muted\">A transaction that was just submitted may still be in the mempool; receipts appear once it is sealed in a block. Pending transactions are not visible through the public API yet.</p></section>";
          return;
        }
        throw err;
      }
      if (stale(token)) return;
      var st = await api.status().catch(function () { return null; });
      var blk = await api.block(r.block_height).catch(function () { return null; });
      if (stale(token)) return;
      var tip = st ? Number(st.chain_tip) : api.tip();
      var tx = blk ? (blk.transactions || []).filter(function (t) { return t.ID === r.tx_id; })[0] : null;
      var p = T.parties(r);
      var kind = T.kind(r);
      var conf = tip ? tip - Number(r.block_height) + 1 : null;
      view.innerHTML =
        '<nav class="crumbs"><a href="/explorer.html#/txs">Transactions</a> / <span class="mono">' + esc(f.short(r.tx_id, 14)) + "</span></nav>" +
        '<section class="card"><div class="card-head"><h2>Transaction</h2>' + statusBadge(r.status) + "</div>" +
        '<dl class="kv">' +
        kv("Tx id", f.hash(r.tx_id, { full: true, label: "tx id" })) +
        kv("Status", statusBadge(r.status) + (r.error ? ' <span class="err-text">' + esc(r.error) + "</span>" : "")) +
        kv("Type", kindBadge(kind) + (r.contract_id ? ' <span class="mono muted">' + esc(r.contract_id) + "</span>" : "")) +
        kv("Block", blockLink(r.block_height) + (conf !== null ? ' <span class="badge badge-ok">' + f.int(conf) + " confirmations</span>" : "")) +
        kv("Timestamp", f.age(r.timestamp) + ' <span class="muted mono">' + esc(f.utc(r.timestamp)) + "</span>") +
        kv("From", addrCell(p.from, { full: true })) +
        kv("To", addrCell(p.to, { full: true })) +
        kv("Amount", amountCell(p.amount)) +
        kv("Fee", amountCell(r.fee)) +
        kv("Nonce", tx ? '<span class="mono">' + f.int(tx.Nonce) + "</span>" : '<span class="muted">' + DASH + "</span>") +
        kv("Gas used", '<span class="mono">' + f.int(r.gas_used) + "</span>") +
        kv("Position", '<span class="mono">index ' + f.int(r.index_in_block) + "</span> in block") +
        kv("Block hash", f.hash(r.block_hash, { full: true, href: blockHref(r.block_height), label: "block hash" })) +
        "</dl></section>" +
        (r.logs && r.logs.length ? '<section class="card"><h2>Logs</h2><div class="table-wrap"><table class="xt"><thead><tr><th class="r">#</th><th>Topic</th><th>Data</th></tr></thead><tbody>' +
          r.logs.map(function (l) { return '<tr><td class="r mono">' + f.int(l.index) + '</td><td class="mono">' + esc(l.topic) + '</td><td><pre class="inline">' + esc(JSON.stringify(l.data || {}, null, 2)) + "</pre></td></tr>"; }).join("") +
          "</tbody></table></div></section>" : "") +
        rawToggle("Raw JSON (receipt" + (tx ? " + block transaction" : "") + ")", tx ? { receipt: r, transaction: tx } : r);
    } catch (err) {
      if (stale(token)) return;
      view.innerHTML = errBox(err);
    }
  }

  // ------------------------------------------------------------------ address page
  async function showAddress(addr, token, opts) {
    setActiveTab("");
    addr = String(addr).trim();
    setTitle("Address " + f.short(addr, 8));
    var note = opts && opts.note;
    view.innerHTML =
      '<nav class="crumbs"><span>Address</span></nav>' +
      (note ? '<div class="notice">' + esc(note) + "</div>" : "") +
      '<section class="card"><div class="card-head"><h2>Address</h2></div>' +
      '<div class="addr-line">' + f.hash(addr, { full: true, label: "address" }) + "</div>" +
      '<div class="tiles tiles-3">' +
      '<div class="tile"><div class="tile-label">Balance</div><div class="tile-value mono" id="a-bal">' + DASH + '</div><div class="tile-sub" id="a-bal-src">CELL</div></div>' +
      '<div class="tile"><div class="tile-label">Nonce</div><div class="tile-value mono" id="a-nonce">' + DASH + '</div><div class="tile-sub" id="a-next">next nonce ' + DASH + "</div></div>" +
      '<div class="tile"><div class="tile-label">Mining nodes owned</div><div class="tile-value mono" id="a-nodes">' + DASH + '</div><div class="tile-sub">from public enrollments</div></div>' +
      "</div></section>" +
      '<section class="card" id="a-nodes-card" hidden><div class="card-head"><h2>Owned mining nodes</h2></div><div id="a-nodes-list"></div></section>' +
      '<section class="card"><div class="card-head"><h2>Recent activity</h2><span class="muted" id="a-scan-label"></span></div>' +
      '<div class="notice">Recent activity in the last <strong id="a-scan-n">' + f.int(ADDRESS_SCAN_STEP) + "</strong> blocks, scanned in your browser. Full address history is coming with the indexer.</div>" +
      '<div id="a-activity">' + loading("Scanning recent blocks…") + "</div>" +
      '<div class="actions"><button type="button" class="btn-sm" id="a-more" hidden>Scan ' + f.int(ADDRESS_SCAN_STEP) + " more blocks</button></div></section>";

    var shape = /^[0-9a-f]{64}$/i.test(addr) || addr === X.SYSTEM_FUNDER;
    if (!shape) {
      view.querySelector(".card").insertAdjacentHTML("beforeend", '<div class="notice warn">This does not look like a QSDM address (64 hex characters). Results may be empty.</div>');
    }

    api.balance(addr).then(function (b) {
      if (stale(token)) return;
      document.getElementById("a-bal").innerHTML = f.cell(b.balance) + ' <span class="unit">CELL</span>';
      document.getElementById("a-bal-src").textContent = b.source ? "source: " + b.source : "CELL";
    }).catch(function (e) { if (!stale(token)) document.getElementById("a-bal-src").textContent = "unavailable: " + e.message; });
    api.nonce(addr).then(function (n) {
      if (stale(token)) return;
      document.getElementById("a-nonce").textContent = f.int(n.nonce);
      document.getElementById("a-next").textContent = "next nonce " + f.int(n.next);
    }).catch(function () {});
    api.enrollments().then(function (en) {
      if (stale(token)) return;
      var mine = en.records.filter(function (r) { return String(r.owner).toLowerCase() === addr.toLowerCase(); });
      document.getElementById("a-nodes").textContent = f.int(mine.length);
      if (mine.length) {
        document.getElementById("a-nodes-card").hidden = false;
        document.getElementById("a-nodes-list").innerHTML = minersTable(mine, null, true);
      }
    }).catch(function () {});

    var scanned = 0;
    var tip = 0;
    var found = [];
    async function scanMore() {
      var more = document.getElementById("a-more");
      if (more) more.hidden = true;
      try {
        if (!tip) tip = Number((await api.status()).chain_tip);
        var to = tip - scanned, from = Math.max(0, to - ADDRESS_SCAN_STEP + 1);
        var label = document.getElementById("a-scan-label");
        if (label) label.textContent = "scanning blocks " + f.int(from) + "–" + f.int(to) + "…";
        var rx = await api.receipts(from, to);
        if (stale(token)) return;
        scanned += (to - from + 1);
        found = found.concat(rx.filter(function (r) { return T.involves(r, addr); }));
        renderActivity(found, tip, scanned);
        if (label) label.textContent = "blocks " + f.int(Math.max(0, tip - scanned + 1)) + "–" + f.int(tip);
        if (more && scanned < ADDRESS_SCAN_MAX && tip - scanned > 0) more.hidden = false;
      } catch (err) {
        if (stale(token)) return;
        document.getElementById("a-activity").innerHTML = errBox(err);
        if (more) more.hidden = false;
      }
    }
    document.getElementById("a-more").addEventListener("click", scanMore);
    scanMore();
  }

  function renderActivity(list, tip, scanned) {
    var root = document.getElementById("a-activity");
    var n = document.getElementById("a-scan-n");
    if (n) n.textContent = f.int(scanned);
    if (!root) return;
    if (!list.length) { root.innerHTML = '<p class="muted">No transactions involving this address in the last ' + f.int(scanned) + " blocks.</p>"; return; }
    var addr = new URLSearchParams(location.search).get("address") || "";
    var inSum = 0, outSum = 0;
    list.forEach(function (r) {
      var p = T.parties(r);
      if (String(p.to).toLowerCase() === addr.toLowerCase()) inSum += Number(p.amount) || 0;
      if (String(p.from).toLowerCase() === addr.toLowerCase()) outSum += Number(p.amount) || 0;
    });
    root.innerHTML =
      '<p class="summary">' + f.int(list.length) + " transactions · received " + amountCell(inSum) + " · sent " + amountCell(outSum) + "</p>" +
      '<div class="table-wrap"><table class="xt"><thead><tr><th>Tx id</th><th>Type</th><th>Block</th><th>Age</th><th>Direction</th><th>Counterparty</th><th class="r">Amount</th></tr></thead><tbody>' +
      list.map(function (r) {
        var p = T.parties(r);
        var incoming = String(p.to).toLowerCase() === addr.toLowerCase();
        var self = incoming && String(p.from).toLowerCase() === addr.toLowerCase();
        return "<tr><td>" + f.hash(r.tx_id, { href: txHref(r.tx_id), head: 10, tail: 4, copy: false }) + "</td><td>" + kindBadge(T.kind(r)) + "</td>" +
          "<td>" + blockLink(r.block_height) + "</td><td>" + f.age(r.timestamp) + "</td>" +
          "<td>" + (self ? '<span class="badge">self</span>' : incoming ? '<span class="badge badge-in">IN</span>' : '<span class="badge badge-out">OUT</span>') + "</td>" +
          "<td>" + addrCell(incoming ? p.from : p.to, { copy: false }) + "</td>" +
          '<td class="r">' + amountCell(p.amount) + "</td></tr>";
      }).join("") + "</tbody></table></div>";
  }

  // ------------------------------------------------------------------ blocks list
  async function showBlocks(token) {
    setTitle("Blocks");
    setActiveTab("blocks");
    view.innerHTML = '<section class="card"><div class="card-head"><h2>Blocks</h2><span class="muted" id="bl-range"></span></div>' +
      '<div class="table-wrap"><table class="xt"><thead><tr><th>Block</th><th>Age</th><th>Hash</th><th>Reward to</th><th class="r">Reward</th><th class="r">Txs</th><th>Producer</th></tr></thead><tbody id="bl-body"></tbody></table></div>' +
      '<div id="bl-status">' + loading() + '</div><div class="actions"><button type="button" class="btn-sm" id="bl-more" hidden>Load ' + BLOCKS_PAGE + " older blocks</button></div></section>";
    var next = null, top = null;
    async function page() {
      var more = document.getElementById("bl-more");
      var status = document.getElementById("bl-status");
      more.hidden = true;
      status.innerHTML = loading();
      try {
        if (next === null) { top = Number((await api.status()).chain_tip); next = top; }
        var to = next, from = Math.max(0, to - BLOCKS_PAGE + 1);
        var res = await Promise.all([api.headers(from, to), api.receipts(from, to).catch(function () { return []; })]);
        if (stale(token)) return;
        var rewards = rewardByHeight(res[1]);
        document.getElementById("bl-body").insertAdjacentHTML("beforeend", res[0].map(function (b) {
          var rw = rewards.get(Number(b.height));
          return "<tr><td>" + blockLink(b.height) + "</td><td>" + f.age(b.timestamp) + "</td><td>" + f.hash(b.hash, { href: blockHref(b.height), head: 10, tail: 6, label: "block hash" }) + "</td>" +
            "<td>" + rewardTo(rw) + "</td>" +
            '<td class="r">' + (rw ? amountCell(rw.amount) : '<span class="muted">' + DASH + "</span>") + '</td><td class="r mono">' + f.int(b.tx_count) + "</td>" +
            "<td>" + f.hash(b.producer_id, { head: 6, tail: 4, copy: false }) + "</td></tr>";
        }).join(""));
        next = from - 1;
        document.getElementById("bl-range").textContent = "showing " + f.int(next + 1) + "–" + f.int(top);
        status.innerHTML = "";
        if (next >= 0) more.hidden = false;
      } catch (err) {
        if (stale(token)) return;
        status.innerHTML = errBox(err);
        more.hidden = false;
      }
    }
    document.getElementById("bl-more").addEventListener("click", page);
    page();
  }

  // ------------------------------------------------------------------ txs list
  async function showTxs(token) {
    setTitle("Transactions");
    setActiveTab("txs");
    var state = { hide: true };
    view.innerHTML = '<section class="card"><div class="card-head"><h2>Transactions</h2>' +
      '<label class="toggle"><input type="checkbox" id="tx-hide" checked> Hide heartbeats</label><span class="muted" id="tx-range"></span></div>' +
      '<div class="notice">Receipts are read per block range (' + TXS_PAGE + ' blocks per page). Heartbeats are empty system transactions sealed in blocks without a mining reward.</div>' +
      '<div class="table-wrap"><table class="xt"><thead><tr><th>Tx id</th><th>Type</th><th>Block</th><th>Age</th><th>From</th><th>To</th><th class="r">Amount</th><th>Status</th></tr></thead><tbody id="tx-body"></tbody></table></div>' +
      '<div id="tx-status">' + loading() + '</div><div class="actions"><button type="button" class="btn-sm" id="tx-more" hidden>Load ' + TXS_PAGE + " older blocks</button></div></section>";
    var all = [], next = null, top = null;
    function draw() {
      var list = state.hide ? all.filter(function (r) { return T.kind(r) !== "heartbeat"; }) : all;
      document.getElementById("tx-body").innerHTML = list.map(function (r) {
        var p = T.parties(r);
        return "<tr><td>" + f.hash(r.tx_id, { href: txHref(r.tx_id), head: 12, tail: 4, label: "tx id" }) + "</td><td>" + kindBadge(T.kind(r)) + "</td><td>" + blockLink(r.block_height) + "</td>" +
          "<td>" + f.age(r.timestamp) + "</td><td>" + addrCell(p.from, { copy: false }) + "</td><td>" + addrCell(p.to, { copy: false }) + "</td>" +
          '<td class="r">' + amountCell(p.amount) + "</td><td>" + statusBadge(r.status) + "</td></tr>";
      }).join("") || '<tr><td colspan="8" class="muted">No matching transactions in this range.</td></tr>';
    }
    async function page() {
      var more = document.getElementById("tx-more"), status = document.getElementById("tx-status");
      more.hidden = true;
      status.innerHTML = loading();
      try {
        if (next === null) { top = Number((await api.status()).chain_tip); next = top; }
        var to = next, from = Math.max(0, to - TXS_PAGE + 1);
        var rx = await api.receipts(from, to);
        if (stale(token)) return;
        all = all.concat(rx);
        next = from - 1;
        document.getElementById("tx-range").textContent = "blocks " + f.int(next + 1) + "–" + f.int(top) + " · " + f.int(all.length) + " receipts";
        draw();
        status.innerHTML = "";
        if (next >= 0) more.hidden = false;
      } catch (err) {
        if (stale(token)) return;
        status.innerHTML = errBox(err);
        more.hidden = false;
      }
    }
    document.getElementById("tx-hide").addEventListener("change", function (e) { state.hide = e.target.checked; draw(); });
    document.getElementById("tx-more").addEventListener("click", page);
    page();
  }

  // ------------------------------------------------------------------ miners
  function minersTable(records, rewardCounts, compact) {
    return '<div class="table-wrap"><table class="xt"><thead><tr><th>Node id</th>' + (compact ? "" : "<th>Owner</th>") +
      '<th>Phase</th><th>Bond</th><th>Fully bonded</th><th class="r">Enrolled at</th>' + (rewardCounts ? '<th class="r" title="Mining-reward transactions to the owner address in the last ' + RECENT_RX_WINDOW + ' blocks">Recent rewards</th>' : "") + "</tr></thead><tbody>" +
      records.map(function (r) {
        var stake = f.dustToCell(r.stake_dust), req = f.dustToCell(r.required_stake_dust);
        var pct = req ? Math.min(100, stake / req * 100) : 0;
        return '<tr><td><span class="mono" title="GPU ' + esc(r.gpu_uuid || "") + '">' + esc(r.node_id) + "</span>" + f.copyBtn(r.node_id, "node id") + "</td>" +
          (compact ? "" : "<td>" + addrCell(r.owner, { copy: false }) + "</td>") +
          '<td><span class="badge badge-phase-' + esc(r.phase) + '">' + esc(String(r.phase || "").replace(/_/g, " ")) + "</span></td>" +
          '<td><div class="bond"><span class="mono">' + f.cell(stake) + " / " + f.cell(req) + '</span> <span class="unit">CELL</span>' +
          '<div class="meter small" title="' + pct.toFixed(1) + '% bonded"><span class="w' + Math.round(pct / 5) * 5 + '"></span></div></div>' +
          (r.bond_mode ? '<div class="muted small">' + esc(String(r.bond_mode).replace(/_/g, " ")) + "</div>" : "") + "</td>" +
          "<td>" + (r.fully_bonded ? '<span class="badge badge-ok">✓ yes</span>' : '<span class="badge badge-warn">○ no</span>') + "</td>" +
          '<td class="r">' + blockLink(r.enrolled_at_height) + "</td>" +
          (rewardCounts ? '<td class="r mono">' + f.int(rewardCounts.get(r.owner) || 0) + "</td>" : "") + "</tr>";
      }).join("") + "</tbody></table></div>";
  }

  async function showMiners(token) {
    setTitle("Miners");
    setActiveTab("miners");
    view.innerHTML = loading("Loading enrollments…");
    try {
      var en = await api.enrollments();
      if (stale(token)) return;
      var tip = api.tip() || Number((await api.status()).chain_tip);
      var rx = await api.receipts(Math.max(0, tip - RECENT_RX_WINDOW + 1), tip).catch(function () { return []; });
      if (stale(token)) return;
      var counts = new Map();
      rx.forEach(function (r) { if (T.kind(r) === "reward") { var to = T.parties(r).to; counts.set(to, (counts.get(to) || 0) + 1); } });
      var recs = en.records.slice().sort(function (a, b) { return (Number(a.enrolled_at_height) || 0) - (Number(b.enrolled_at_height) || 0); });
      var phases = {};
      recs.forEach(function (r) { phases[r.phase] = (phases[r.phase] || 0) + 1; });
      var bonded = recs.filter(function (r) { return r.fully_bonded; }).length;
      var owners = new Set(recs.map(function (r) { return r.owner; }));
      var filter = "all";
      view.innerHTML =
        '<section class="tiles tiles-4">' +
        '<div class="tile"><div class="tile-label">Enrollments</div><div class="tile-value mono">' + f.int(en.total) + '</div><div class="tile-sub">all phases</div></div>' +
        '<div class="tile"><div class="tile-label">Active</div><div class="tile-value mono">' + f.int(phases.active || 0) + '</div><div class="tile-sub">' + f.int(phases.pending_unbond || 0) + " pending unbond · " + f.int(phases.revoked || 0) + " revoked</div></div>" +
        '<div class="tile"><div class="tile-label">Fully bonded</div><div class="tile-value mono">' + f.int(bonded) + '</div><div class="tile-sub">of ' + f.int(recs.length) + " nodes</div></div>" +
        '<div class="tile"><div class="tile-label">Owner addresses</div><div class="tile-value mono">' + f.int(owners.size) + '</div><div class="tile-sub">' + f.int(counts.size) + " rewarded in last " + RECENT_RX_WINDOW + " blocks</div></div>" +
        "</section>" +
        '<section class="card"><div class="card-head"><h2>Mining nodes</h2><div class="chips" role="group" aria-label="Filter by phase">' +
        ["all", "active", "pending_unbond", "revoked"].map(function (p) { return '<button type="button" class="chip" data-phase="' + p + '"' + (p === "all" ? ' aria-pressed="true"' : ' aria-pressed="false"') + ">" + esc(p.replace(/_/g, " ")) + "</button>"; }).join("") +
        '</div></div><div id="m-table"></div>' +
        '<p class="muted small">Source: <code>/mining/enrollments</code>. “Recent rewards” counts mining-reward transactions to the owner address in the last ' + RECENT_RX_WINDOW + " blocks; rewards are paid per owner address, not per node.</p></section>";
      function draw() {
        var list = filter === "all" ? recs : recs.filter(function (r) { return r.phase === filter; });
        document.getElementById("m-table").innerHTML = list.length ? minersTable(list, counts) : '<p class="muted">No nodes in this phase.</p>';
      }
      view.querySelectorAll(".chip[data-phase]").forEach(function (b) {
        b.addEventListener("click", function () {
          filter = b.getAttribute("data-phase");
          view.querySelectorAll(".chip[data-phase]").forEach(function (o) { o.setAttribute("aria-pressed", o === b ? "true" : "false"); });
          draw();
        });
      });
      draw();
    } catch (err) {
      if (stale(token)) return;
      view.innerHTML = errBox(err);
    }
  }

  // ------------------------------------------------------------------ stats
  async function showStats(token) {
    setTitle("Statistics");
    setActiveTab("stats");
    view.innerHTML = loading("Loading the last " + STATS_WINDOW + " blocks (a few small requests)…");
    try {
      var tip = Number((await api.status()).chain_tip);
      var from = Math.max(0, tip - STATS_WINDOW + 1);
      var hdrs = await api.headers(from, tip);
      if (stale(token)) return;
      var rx = await api.receipts(from, tip);
      if (stale(token)) return;
      hdrs = hdrs.slice().reverse(); // ascending
      var intervals = [];
      for (var i = 1; i < hdrs.length; i++) {
        if (Number(hdrs[i].height) !== Number(hdrs[i - 1].height) + 1) continue;
        var dt = (f.toDate(hdrs[i].timestamp) - f.toDate(hdrs[i - 1].timestamp)) / 1000;
        if (isFinite(dt)) intervals.push(dt);
      }
      var sorted = intervals.slice().sort(function (a, b) { return a - b; });
      var mean = intervals.reduce(function (s, v) { return s + v; }, 0) / (intervals.length || 1);
      var pick = function (q) { return sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))] : NaN; };
      // histogram: 1 s buckets 0..29, last bucket 30+
      var MAXB = 30, hist = new Array(MAXB + 1).fill(0);
      intervals.forEach(function (v) { hist[Math.min(MAXB, Math.max(0, Math.floor(v)))]++; });
      var lastNonZero = 0;
      hist.forEach(function (c, idx) { if (c) lastNonZero = idx; });
      var hb = Math.max(15, Math.min(MAXB, lastNonZero + 2));
      var histData = hist.slice(0, hb + 1).map(function (c, idx) {
        var label = idx === MAXB ? "30+" : String(idx);
        return { label: label + "s", value: c, tip: (idx === MAXB ? "≥ 30 s" : idx + "–" + (idx + 1) + " s") + ": " + c + " blocks (" + (c / (intervals.length || 1) * 100).toFixed(1) + "%)" };
      });

      var rewards = rewardByHeight(rx);
      var BUCKET = 10;
      var rewardSeries = [];
      for (var b0 = Number(hdrs.length ? hdrs[0].height : from); b0 <= tip; b0 += BUCKET) {
        var sum = 0, nb = 0;
        for (var h = b0; h < b0 + BUCKET && h <= tip; h++) { var rw = rewards.get(h); if (rw) { sum += rw.amount; nb++; } }
        rewardSeries.push({ label: f.int(b0), value: sum, tip: "Blocks " + f.int(b0) + "–" + f.int(Math.min(tip, b0 + BUCKET - 1)) + ": " + f.cell(sum, { dp: 4 }) + " CELL in " + nb + " reward tx" + (nb === 1 ? "" : "s") });
      }
      var totalReward = 0;
      var byAddr = new Map();
      rewards.forEach(function (v) { totalReward += v.amount; byAddr.set(v.to, (byAddr.get(v.to) || 0) + v.amount); });
      var top = Array.from(byAddr.entries()).sort(function (a, b) { return b[1] - a[1]; });
      var nBlocks = hdrs.length;

      view.innerHTML =
        '<section class="tiles tiles-4">' +
        '<div class="tile"><div class="tile-label">Mean block time</div><div class="tile-value mono">' + (isFinite(mean) ? mean.toFixed(2) : DASH) + ' <span class="unit">s</span></div><div class="tile-sub">target 10 s · ' + f.int(intervals.length) + " intervals</div></div>" +
        '<div class="tile"><div class="tile-label">Median / p95</div><div class="tile-value mono">' + (isFinite(pick(0.5)) ? pick(0.5).toFixed(0) : DASH) + " / " + (isFinite(pick(0.95)) ? pick(0.95).toFixed(0) : DASH) + ' <span class="unit">s</span></div><div class="tile-sub">block interval</div></div>' +
        '<div class="tile"><div class="tile-label">Blocks with a reward</div><div class="tile-value mono">' + f.int(rewards.size) + ' <span class="unit">/ ' + f.int(nBlocks) + '</span></div><div class="tile-sub">' + (nBlocks ? (rewards.size / nBlocks * 100).toFixed(1) : "0") + "% of recent blocks</div></div>" +
        '<div class="tile"><div class="tile-label">' + CELL_ICON + 'Rewards paid</div><div class="tile-value mono">' + f.cell(totalReward, { dp: 2 }) + ' <span class="unit">CELL</span></div><div class="tile-sub">to ' + f.int(byAddr.size) + " addresses</div></div>" +
        "</section>" +
        '<section class="card"><div class="card-head"><h2>Block time distribution</h2><span class="muted">last ' + f.int(nBlocks) + " blocks (" + f.int(from) + "–" + f.int(tip) + ")</span></div>" +
        window.QSDMCharts.bars({ data: histData, color: "var(--teal)", title: "Histogram of block intervals in seconds", xEvery: 2, refX: { index: 10, label: "target 10 s" } }) +
        '<p class="muted small">Seconds between consecutive block timestamps (header timestamps have 1 s resolution). Hover a bar for counts.</p></section>' +
        '<section class="card"><div class="card-head"><h2>Mining rewards per ' + BUCKET + " blocks</h2><span class=\"muted\">CELL</span></div>" +
        window.QSDMCharts.bars({ data: rewardSeries, color: "var(--gold)", title: "CELL paid as mining rewards per 10-block bucket", yFmt: function (v) { return v.toFixed(v < 10 ? 1 : 0); }, xEvery: 10 }) +
        '<p class="muted small">Each block either carries one mining-reward transaction or an empty heartbeat. Blocks without a reward are expected while few miners are online.</p></section>' +
        '<section class="card"><div class="card-head"><h2>Reward recipients</h2><span class="muted">last ' + f.int(nBlocks) + " blocks</span></div>" +
        (top.length ? '<div class="table-wrap"><table class="xt"><thead><tr><th>#</th><th>Address</th><th class="r">Rewards</th><th class="r">Share</th></tr></thead><tbody>' +
          top.map(function (e, idx) {
            return '<tr><td class="mono">' + (idx + 1) + "</td><td>" + addrCell(e[0]) + '</td><td class="r">' + amountCell(e[1]) + '</td><td class="r mono">' + (e[1] / totalReward * 100).toFixed(1) + "%</td></tr>";
          }).join("") + "</tbody></table></div>" : '<p class="muted">No mining rewards in this window.</p>') +
        "</section>" +
        '<p class="muted small">Computed in your browser from a few small, cached requests to <code>/mining/blocks</code> and <code>/receipts</code>. Longer ranges and daily series need the Phase 2 indexer.</p>';
    } catch (err) {
      if (stale(token)) return;
      view.innerHTML = errBox(err);
    }
  }

  // ------------------------------------------------------------------ router
  var currentRoute = { name: "" };

  function parseRoute() {
    var p = new URLSearchParams(location.search);
    var block = p.get("block") || p.get("height");
    var tx = p.get("tx") || p.get("tx_id");
    var addr = p.get("address");
    var q = p.get("q");
    if (block) return { name: "block", value: block };
    if (tx) return { name: "tx", value: tx };
    if (addr) return { name: "address", value: addr };
    if (q) return { name: "search", value: q };
    var hsh = location.hash.replace(/^#\/?/, "").split(/[?/]/)[0];
    if (hsh === "blocks" || hsh === "txs" || hsh === "miners" || hsh === "stats") return { name: hsh };
    return { name: "home" };
  }

  function route(opts) {
    var r = parseRoute();
    var token = ++routeToken;
    stopRefresh();
    currentRoute = r;
    setLive(r.name === "home" ? "live" : "");
    if (r.name !== "home") window.scrollTo(0, 0);
    var input = document.getElementById("xp-q");
    if (input && (r.name === "block" || r.name === "tx" || r.name === "address")) input.value = r.value;
    switch (r.name) {
      case "block": return showBlock(r.value, token);
      case "tx": return showTx(r.value, token);
      case "address": return showAddress(r.value, token, opts);
      case "search": return runSearch(r.value);
      case "blocks": return showBlocks(token);
      case "txs": return showTxs(token);
      case "miners": return showMiners(token);
      case "stats": return showStats(token);
      default: return showHome();
    }
  }

  function navigate(href, opts) {
    var u = new URL(href, location.href);
    if (u.href !== location.href) history.pushState(null, "", u.pathname + u.search + u.hash);
    route(opts);
  }

  // Intercept in-app links so navigation doesn't reload the page.
  document.addEventListener("click", function (ev) {
    if (ev.defaultPrevented || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return;
    var a = ev.target.closest && ev.target.closest("a[href]");
    if (!a || a.target) return;
    var u = new URL(a.getAttribute("href"), location.href);
    if (u.origin !== location.origin || u.pathname !== location.pathname) return;
    var isHashRoute = /^(#\/?(blocks|txs|miners|stats)?)?$/.test(u.hash) && u.search === "";
    var isQueryRoute = /[?&](block|height|tx|tx_id|address|q)=/.test(u.search);
    if (!isHashRoute && !isQueryRoute) return;
    ev.preventDefault();
    navigate(u.pathname + u.search + u.hash);
  });

  window.addEventListener("popstate", function () { route(); });
  window.addEventListener("hashchange", function () {
    // A manual hash edit on a ?block=/?tx= URL: drop the query so the hash wins.
    if (/^#\/(blocks|txs|miners|stats)/.test(location.hash) && location.search) {
      history.replaceState(null, "", location.pathname + location.hash);
    }
    route();
  });

  document.getElementById("xp-search").addEventListener("submit", function (ev) {
    ev.preventDefault();
    runSearch(document.getElementById("xp-q").value);
  });

  // "/" focuses search (tronscan-style shortcut).
  document.addEventListener("keydown", function (ev) {
    if (ev.key === "/" && !/^(INPUT|TEXTAREA|SELECT)$/.test((document.activeElement || {}).tagName || "")) {
      ev.preventDefault();
      document.getElementById("xp-q").focus();
    }
  });

  route();
})();
