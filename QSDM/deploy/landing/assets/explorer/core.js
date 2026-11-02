/* QSDM explorer core: API client (cache, de-dupe, polite queue) + formatters.
 * Shared by /explorer.html and the web wallet's read-only "recent activity" view.
 * Read-only GETs only. No secrets, no writes.
 *
 * API base resolution:
 *   1. window.QSDM_API_BASE (string), if set before this file loads;
 *   2. on qsdm.tech: same-origin /api/v1;
 *   3. elsewhere: same-origin /api/v1 (a local preview proxy), then
 *      https://api.qsdm.tech/api/v1 as a fallback.
 */
(function () {
  "use strict";

  var PUBLIC_BASE = "https://api.qsdm.tech/api/v1";
  var MAX_CONCURRENT = 2;
  var MIN_GAP_MS = 220;            // <= ~4.5 request starts per second
  var HEADERS_MAX = 200;           // /mining/blocks cap
  var RECEIPTS_MAX = 200;          // /receipts cap (records AND height window)
  var RECEIPTS_SPAN = 160;         // heights per request: leaves headroom for
                                   // blocks with 2+ txs so pages rarely fill up
  var CHAIN_BLOCKS_MAX = 64;       // /chain/blocks cap

  // ---------------------------------------------------------------- bases
  function candidateBases() {
    if (typeof window.QSDM_API_BASE === "string" && window.QSDM_API_BASE) {
      return [window.QSDM_API_BASE.replace(/\/+$/, "")];
    }
    var h = location.hostname;
    if (h === "qsdm.tech" || h === "www.qsdm.tech") return ["/api/v1"];
    if (location.protocol === "file:") return [PUBLIC_BASE];
    return ["/api/v1", PUBLIC_BASE];
  }
  var bases = candidateBases();
  var activeBase = null;

  // ---------------------------------------------------------------- errors
  function ApiError(message, status, path) {
    var e = new Error(message);
    e.name = "ApiError";
    e.status = status || 0;
    e.path = path || "";
    return e;
  }

  // ---------------------------------------------------------------- queue
  var queue = [];
  var running = 0;
  var lastStart = 0;
  var pausedUntil = 0;
  var stats = { requests: 0, cacheHits: 0, rateLimited: 0 };

  function pump() {
    if (running >= MAX_CONCURRENT || queue.length === 0) return;
    var now = Date.now();
    var wait = Math.max(lastStart + MIN_GAP_MS - now, pausedUntil - now, 0);
    if (wait > 0) {
      setTimeout(pump, wait);
      return;
    }
    var job = queue.shift();
    running++;
    lastStart = Date.now();
    job().finally(function () {
      running--;
      pump();
    });
    pump();
  }

  function enqueue(fn) {
    return new Promise(function (resolve, reject) {
      queue.push(function () {
        return fn().then(resolve, reject);
      });
      pump();
    });
  }

  async function fetchOnce(base, path) {
    var res = await fetch(base + path, {
      cache: "no-store",
      credentials: "omit",
      headers: { Accept: "application/json" }
    });
    stats.requests++;
    if (res.status === 429) {
      stats.rateLimited++;
      var ra = Number(res.headers.get("Retry-After")) || 30;
      pausedUntil = Date.now() + Math.min(ra, 120) * 1000;
      throw ApiError("The public API is rate limiting this browser. Pausing requests for " + Math.min(ra, 120) + " s.", 429, path);
    }
    var ct = res.headers.get("Content-Type") || "";
    if (!res.ok) {
      var text = "";
      try { text = (await res.text()).trim().slice(0, 200); } catch (_) {}
      var err = ApiError(text || ("HTTP " + res.status), res.status, path);
      err.nonJson = ct.indexOf("json") === -1;
      throw err;
    }
    if (ct.indexOf("json") === -1) {
      var e2 = ApiError("Unexpected non-JSON response", res.status, path);
      e2.nonJson = true;
      throw e2;
    }
    return res.json();
  }

  async function rawGet(path) {
    var order = activeBase ? [activeBase] : bases.slice();
    var lastErr = null;
    for (var i = 0; i < order.length; i++) {
      try {
        var data = await fetchOnce(order[i], path);
        activeBase = order[i];
        return data;
      } catch (err) {
        lastErr = err;
        // Same-origin base that isn't an API (static server, 404 HTML,
        // network error) -> try the next base. Real API errors stop here.
        var tryNext = !activeBase && i < order.length - 1 &&
          (err.status === 0 || err.nonJson || err.name === "TypeError");
        if (!tryNext) throw err;
      }
    }
    throw lastErr;
  }

  // ---------------------------------------------------------------- cache
  var cache = new Map();     // path -> {t, ttl, data}
  var inflight = new Map();  // path -> promise

  function get(path, ttlMs) {
    var hit = cache.get(path);
    if (hit && (hit.ttl === Infinity || Date.now() - hit.t < hit.ttl)) {
      stats.cacheHits++;
      return Promise.resolve(hit.data);
    }
    if (inflight.has(path)) return inflight.get(path);
    var p = enqueue(function () { return rawGet(path); }).then(function (data) {
      if (ttlMs) cache.set(path, { t: Date.now(), ttl: ttlMs, data: data });
      if (cache.size > 400) cache.delete(cache.keys().next().value);
      return data;
    }).finally(function () {
      inflight.delete(path);
    });
    inflight.set(path, p);
    return p;
  }

  // ---------------------------------------------------------------- chain state
  var headerByHeight = new Map();   // height -> header (immutable once sealed)
  var receiptsByHeight = new Map(); // height -> [receipts] (complete heights only)
  var provisional = new Set();      // heights near the tip: re-read next time
  var knownTip = 0;

  function noteTip(t) {
    t = Number(t) || 0;
    if (t > knownTip) knownTip = t;
    return knownTip;
  }

  async function status() {
    var s = await get("/status", 8000);
    noteTip(s.chain_tip);
    return s;
  }

  function missingRanges(map, from, to, maxSpan, recheck) {
    var out = [];
    var start = null;
    for (var h = from; h <= to; h++) {
      if (!map.has(h) || (recheck && recheck.has(h))) {
        if (start === null) start = h;
        if (h - start + 1 === maxSpan) { out.push([start, h]); start = null; }
      } else if (start !== null) {
        out.push([start, h - 1]);
        start = null;
      }
    }
    if (start !== null) out.push([start, to]);
    return out;
  }

  // Headers in [from, to], newest first. Fetches only heights not cached.
  async function headers(from, to) {
    from = Math.max(0, Math.floor(from));
    to = Math.floor(to);
    if (to < from) return [];
    var gaps = missingRanges(headerByHeight, from, to, HEADERS_MAX);
    await Promise.all(gaps.map(async function (g) {
      var d = await get("/mining/blocks?from=" + g[0] + "&to=" + g[1] + "&limit=" + (g[1] - g[0] + 1), 0);
      noteTip(d.tip);
      (d.headers || []).forEach(function (hd) { headerByHeight.set(Number(hd.height), hd); });
    }));
    var out = [];
    for (var h = to; h >= from; h--) if (headerByHeight.has(h)) out.push(headerByHeight.get(h));
    return out;
  }

  // Receipts for heights [from, to], newest first. Handles the 200-record cap:
  // the API returns newest-first, so if a page is full the lowest height in it
  // may be partial and gets re-fetched with the next window.
  async function receipts(from, to) {
    from = Math.max(0, Math.floor(from));
    to = Math.floor(to);
    if (to < from) return [];
    var gaps = missingRanges(receiptsByHeight, from, to, RECEIPTS_SPAN, provisional);
    for (var gi = 0; gi < gaps.length; gi++) {
      var lo = gaps[gi][0], hi = gaps[gi][1];
      var guard = 0;
      while (hi >= lo && guard++ < 50) {
        var d = await get("/receipts?from=" + lo + "&to=" + hi + "&limit=" + RECEIPTS_MAX, 0);
        noteTip(d.tip);
        var list = d.receipts || [];
        var byH = new Map();
        list.forEach(function (r) {
          var h = Number(r.block_height);
          if (!byH.has(h)) byH.set(h, []);
          byH.get(h).push(r);
        });
        var full = list.length >= RECEIPTS_MAX;
        var minH = list.length ? Math.min.apply(null, list.map(function (r) { return Number(r.block_height); })) : lo;
        var completeFrom = full ? minH + 1 : lo;
        var effectiveHi = Math.min(hi, Number(d.to));
        for (var h = completeFrom; h <= effectiveHi; h++) {
          var arr = byH.get(h) || [];
          arr.sort(function (a, b) { return (a.index_in_block || 0) - (b.index_in_block || 0); });
          receiptsByHeight.set(h, arr);
          if (h >= Number(d.tip) - 1) provisional.add(h); else provisional.delete(h);
        }
        if (!full) break;
        if (minH >= hi) {
          // One height alone fills the page; keep what we have for it.
          receiptsByHeight.set(minH, byH.get(minH) || []);
          hi = minH - 1;
        } else {
          hi = minH; // re-fetch the possibly partial lowest height
        }
      }
    }
    var out = [];
    for (var h2 = to; h2 >= from; h2--) {
      var rs = receiptsByHeight.get(h2);
      if (rs) for (var i = rs.length - 1; i >= 0; i--) out.push(rs[i]);
    }
    return out;
  }

  function block(height) {
    var h = Math.floor(Number(height));
    var ttl = knownTip && h < knownTip - 2 ? Infinity : 5000;
    return get("/chain/blocks?from=" + h + "&to=" + h + "&limit=1", ttl).then(function (d) {
      noteTip(d.tip);
      return (d.blocks || [])[0] || null;
    });
  }

  function receipt(txId) {
    return get("/receipts/" + encodeURIComponent(txId), Infinity);
  }

  function balance(addr) { return get("/wallet/balance?address=" + encodeURIComponent(addr), 10000); }
  function nonce(addr) { return get("/wallet/nonce?sender=" + encodeURIComponent(addr), 10000); }
  function miningAccount(addr) { return get("/mining/account?address=" + encodeURIComponent(addr), 10000); }

  var enrollCache = null;
  async function enrollments() {
    if (enrollCache && Date.now() - enrollCache.t < 120000) return enrollCache.data;
    var records = [];
    var cursor = "";
    var total = null;
    for (var page = 0; page < 10; page++) {
      var d = await get("/mining/enrollments?limit=500" + (cursor ? "&cursor=" + encodeURIComponent(cursor) : ""), 60000);
      records = records.concat(d.records || []);
      if (total === null && typeof d.total_matches === "number") total = d.total_matches;
      if (!d.has_more || !d.next_cursor) break;
      cursor = d.next_cursor;
    }
    var data = { records: records, total: total === null ? records.length : total };
    enrollCache = { t: Date.now(), data: data };
    return data;
  }

  // ---------------------------------------------------------------- formatting
  function esc(s) {
    if (s === null || s === undefined) return "";
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  var DASH = "—";

  function fmtInt(n) {
    if (n === null || n === undefined || n === "" || !isFinite(Number(n))) return DASH;
    return Math.round(Number(n)).toLocaleString("en-US");
  }

  // CELL amounts arrive as JSON numbers in CELL (8 decimals). Format without
  // float noise: round to 8 dp, trim trailing zeros.
  function fmtCell(n, opts) {
    if (n === null || n === undefined || n === "" || !isFinite(Number(n))) return DASH;
    var maxDp = (opts && opts.dp !== undefined) ? opts.dp : 8;
    var s = Number(n).toLocaleString("en-US", { minimumFractionDigits: 0, maximumFractionDigits: maxDp });
    return s;
  }

  function dustToCell(dust) {
    if (dust === null || dust === undefined || dust === "") return null;
    return Number(dust) / 1e8;
  }

  function short(s, head, tail) {
    if (!s) return DASH;
    s = String(s);
    head = head || 8;
    tail = tail === undefined ? head : tail;
    if (s.length <= head + tail + 1) return s;
    return s.slice(0, head) + "…" + s.slice(-tail);
  }

  function toDate(ts) {
    if (!ts) return null;
    var d = new Date(ts);
    return isNaN(d.getTime()) ? null : d;
  }

  function utc(ts) {
    var d = toDate(ts);
    if (!d) return DASH;
    return d.toISOString().replace("T", " ").replace(/\.\d+Z$/, " UTC").replace(/Z$/, " UTC");
  }

  function ageText(ts, now) {
    var d = toDate(ts);
    if (!d) return DASH;
    var s = Math.max(0, Math.round(((now || Date.now()) - d.getTime()) / 1000));
    if (s < 60) return s + "s ago";
    var m = Math.floor(s / 60);
    if (m < 60) return m + "m " + (s % 60) + "s ago";
    var h = Math.floor(m / 60);
    if (h < 48) return h + "h " + (m % 60) + "m ago";
    return Math.floor(h / 24) + "d ago";
  }

  function duration(sec) {
    if (sec === null || sec === undefined || !isFinite(sec)) return DASH;
    sec = Math.round(sec);
    var d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    if (d > 365) return (d / 365.25).toFixed(1) + " years";
    if (d > 0) return d + "d " + h + "h";
    if (h > 0) return h + "h " + m + "m";
    return m + "m " + (sec % 60) + "s";
  }

  // <time> element that the global ticker keeps fresh.
  function age(ts) {
    var d = toDate(ts);
    if (!d) return '<span class="muted">' + DASH + "</span>";
    return '<time class="age" datetime="' + esc(d.toISOString()) + '" title="' + esc(utc(ts)) + '">' + esc(ageText(ts)) + "</time>";
  }

  function tickAges() {
    if (document.hidden) return;
    var now = Date.now();
    var els = document.querySelectorAll("time.age[datetime]");
    for (var i = 0; i < els.length; i++) {
      els[i].textContent = ageText(els[i].getAttribute("datetime"), now);
    }
  }
  setInterval(tickAges, 1000);

  function copyBtn(value, label) {
    if (!value) return "";
    return '<button type="button" class="cp" data-copy="' + esc(value) + '" aria-label="Copy ' + esc(label || "value") + '" title="Copy">' +
      '<svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><path fill="currentColor" d="M5 1h8a2 2 0 0 1 2 2v8h-2V3H5V1zm-2 4h7a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2zm0 2v7h7V7H3z"/></svg></button>';
  }

  // Truncated mono hash with optional link + copy button.
  function hash(value, opts) {
    opts = opts || {};
    if (!value) return '<span class="muted">' + DASH + "</span>";
    var txt = opts.full ? esc(value) : esc(short(value, opts.head || 8, opts.tail));
    var inner = opts.href ? '<a href="' + esc(opts.href) + '">' + txt + "</a>" : txt;
    return '<span class="hash mono" title="' + esc(value) + '">' + inner + "</span>" + (opts.copy === false ? "" : copyBtn(value, opts.label));
  }

  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest && ev.target.closest("button.cp[data-copy]");
    if (!btn) return;
    ev.preventDefault();
    var v = btn.getAttribute("data-copy");
    var done = function () {
      btn.classList.add("copied");
      btn.setAttribute("title", "Copied");
      setTimeout(function () { btn.classList.remove("copied"); btn.setAttribute("title", "Copy"); }, 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(v).then(done, function () {});
    }
  });

  // ---------------------------------------------------------------- tx helpers
  var SYSTEM_FUNDER = "qsdm-system-funder";
  var REWARD_CONTRACT = "qsdm/mining-reward/v1";

  function txKind(idOrObj, contract, sender) {
    var id = idOrObj;
    if (idOrObj && typeof idOrObj === "object") {
      id = idOrObj.tx_id || idOrObj.ID || idOrObj.id || "";
      contract = idOrObj.contract_id || contract;
      var p = parties(idOrObj);
      sender = p.from;
    }
    id = String(id || "");
    if (contract === REWARD_CONTRACT || id.indexOf("solo-reward-") === 0) return "reward";
    if (id.indexOf("solo-heartbeat-") === 0) return "heartbeat";
    if (sender === SYSTEM_FUNDER) return "system";
    if (contract) return "contract";
    return "transfer";
  }

  var KIND_LABEL = { reward: "Mining reward", heartbeat: "Heartbeat", system: "System", contract: "Contract", transfer: "Transfer" };

  // Sender / recipient / amount from a receipt (TxApplied log) or a block tx.
  function parties(o) {
    if (!o) return {};
    if (o.Sender !== undefined || o.Recipient !== undefined) {
      return { from: o.Sender, to: o.Recipient, amount: o.Amount, fee: o.Fee };
    }
    var data = {};
    (o.logs || []).some(function (l) {
      if (l && l.data && (l.data.sender !== undefined || l.data.recipient !== undefined)) { data = l.data; return true; }
      return false;
    });
    return {
      from: data.sender !== undefined ? data.sender : o.sender,
      to: data.recipient !== undefined ? data.recipient : o.recipient,
      amount: data.amount !== undefined ? data.amount : o.amount,
      fee: o.fee
    };
  }

  function involves(o, addr) {
    var p = parties(o);
    var a = String(addr).toLowerCase();
    return String(p.from || "").toLowerCase() === a || String(p.to || "").toLowerCase() === a;
  }

  window.QSDMX = {
    PUBLIC_BASE: PUBLIC_BASE,
    LIMITS: { HEADERS_MAX: HEADERS_MAX, RECEIPTS_MAX: RECEIPTS_MAX, CHAIN_BLOCKS_MAX: CHAIN_BLOCKS_MAX },
    SYSTEM_FUNDER: SYSTEM_FUNDER,
    REWARD_CONTRACT: REWARD_CONTRACT,
    api: {
      get: get, status: status, headers: headers, receipts: receipts, block: block, receipt: receipt,
      balance: balance, nonce: nonce, miningAccount: miningAccount, enrollments: enrollments,
      tip: function () { return knownTip; },
      noteTip: noteTip,
      base: function () { return activeBase || bases[0]; },
      stats: stats,
      cachedHeader: function (h) { return headerByHeight.get(h); },
      findHeaderByHash: function (hx) {
        hx = String(hx).toLowerCase();
        var found = null;
        headerByHeight.forEach(function (hd) { if (!found && String(hd.hash).toLowerCase() === hx) found = hd; });
        return found;
      },
      cachedHeaderCount: function () { return headerByHeight.size; }
    },
    fmt: {
      esc: esc, DASH: DASH, int: fmtInt, cell: fmtCell, dustToCell: dustToCell, short: short,
      utc: utc, ageText: ageText, age: age, duration: duration, hash: hash, copyBtn: copyBtn, toDate: toDate
    },
    tx: { kind: txKind, KIND_LABEL: KIND_LABEL, parties: parties, involves: involves }
  };
})();
