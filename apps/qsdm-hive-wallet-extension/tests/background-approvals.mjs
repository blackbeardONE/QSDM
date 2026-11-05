// Runs the extension's real background.js in Node with stand-in chrome APIs and
// checks the approval flow for website requests (the approval window, its
// port, approve / cancel / close, the requests that pass straight to Hive, one
// waiting request per site) and the read-only public API relay. No browser,
// no Hive, no network. Usage: node tests/background-approvals.mjs
import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const extensionDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const SRC = fs.readFileSync(path.join(extensionDirectory, "background.js"), "utf8");
const EXT_ID = "habkkkednignfkoffhpbjahcjbikkahh";
const BASE = `chrome-extension://${EXT_ID}/`;
const ME = "13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9";
// Windows timers tick at about 15 ms: each stand-in Hive answer is a timer, so waits are generous.
const tick = (ms = 5) => new Promise((r) => setTimeout(r, ms * 12));

const load = (hive) => {
  const log = { native: [], windows: [], updates: [], tabMessages: [], fetches: [] };
  const listeners = { message: [], connect: [], removed: [] };
  const chrome = {
    runtime: {
      id: EXT_ID,
      lastError: null,
      getURL: (p) => BASE + p,
      onMessage: { addListener: (f) => listeners.message.push(f) },
      onConnect: { addListener: (f) => listeners.connect.push(f) },
      connectNative: () => {
        const l = { message: [], disconnect: [] };
        return {
          onMessage: { addListener: (f) => l.message.push(f) },
          onDisconnect: { addListener: (f) => l.disconnect.push(f) },
          disconnect: () => {},
          postMessage: (m) => {
            log.native.push(`${m.method}@${m.origin}`);
            setTimeout(() => l.message.forEach((f) => f(hive(m))), 1);
          },
        };
      },
    },
    tabs: {
      onRemoved: { addListener: (f) => listeners.removed.push(f) },
      sendMessage: (tabId, message, opts, cb) => {
        log.tabMessages.push(message);
        cb && cb();
      },
      get: async () => ({}),
      update: async () => ({}),
      create: async () => ({ id: 9 }),
    },
    windows: {
      create: (opts, cb) => {
        log.windows.push(opts.url);
        setTimeout(() => cb({ id: 100 + log.windows.length }), 1);
      },
      update: (id, opts, cb) => {
        log.updates.push(id);
        cb && cb();
      },
    },
  };
  const fetch = async (url) => {
    log.fetches.push(url);
    return { ok: true, status: 200, text: async () => JSON.stringify({ chain_tip: 1050000, network: "qsdm-mainnet" }) };
  };
  const context = vm.createContext({ chrome, fetch, crypto: webcrypto, setTimeout, clearTimeout, URL, AbortController, console });
  vm.runInContext(SRC, context);
  const fromSite = (origin, method, params) => {
    const id = `req-${Math.random().toString(16).slice(2)}`;
    listeners.message.forEach((f) =>
      f({ source: "qsdm-hive-content", id, method, params }, { tab: { id: 7, url: `${origin}/page` }, url: `${origin}/page`, frameId: 0 }, () => {})
    );
    return id;
  };
  // Objects made inside the vm have its own prototypes: compare plain JSON copies.
  const plain = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));
  const response = (id) => plain(log.tabMessages.find((m) => m.id === id)?.response);
  const openPort = (approvalUrl, senderUrl = approvalUrl) => {
    const id = new URL(approvalUrl).searchParams.get("id");
    const posted = [];
    const l = { message: [], disconnect: [] };
    const port = {
      name: `qsdm-approval:${id}`,
      sender: { id: EXT_ID, url: senderUrl },
      postMessage: (m) => posted.push(plain(m)),
      disconnect: () => posted.push({ type: "(disconnected by worker)" }),
      onMessage: { addListener: (f) => l.message.push(f) },
      onDisconnect: { addListener: (f) => l.disconnect.push(f) },
    };
    listeners.connect.forEach((f) => f(port));
    return { posted, send: (m) => l.message.forEach((f) => f(m)), close: () => l.disconnect.forEach((f) => f()) };
  };
  const apiMessage = (pathName, senderUrl) =>
    new Promise((resolve) => {
      let kept = false;
      listeners.message.forEach((f) => {
        kept = f({ source: "qsdm-wallet-api", path: pathName }, { id: EXT_ID, url: senderUrl }, (r) => resolve(plain(r))) || kept;
      });
      if (!kept) setTimeout(() => resolve("(answered synchronously)"), 0);
    });
  return { log, fromSite, response, openPort, apiMessage };
};

// Hive stand-in: ready wallet; `connected` sites answer qsdm_accounts with the address.
const hiveWith = (connected = new Set()) => (m) => {
  switch (m.method) {
    case "qsdm_ping":
      return { ok: true, result: { hive: true } };
    case "qsdm_getWalletInfo":
      return { ok: true, result: { address: ME, ready: true, connectedSites: connected.size } };
    case "qsdm_accounts":
      return { ok: true, result: connected.has(m.origin) ? [ME] : [] };
    case "qsdm_requestAccounts":
      connected.add(m.origin);
      return { ok: true, result: [ME] };
    case "qsdm_sendTransaction":
      return connected.has(m.origin) ? { ok: true, result: { transaction_id: "tx-123" } } : { ok: false, error: "site not connected" };
    case "qsdm_signMessage":
      return { ok: true, result: { signature: "sig" } };
    default:
      return { ok: false, error: `unexpected ${m.method}` };
  }
};

(async () => {
  const SITE = "https://blackbeard.finance";
  // 1. Connect: window, request details, approve, Hive asked, site answered.
  {
    const h = load(hiveWith());
    const id = h.fromSite(SITE, "qsdm_requestAccounts");
    await tick(30);
    assert.equal(h.log.windows.length, 1, "one approval window");
    assert.ok(h.log.windows[0].startsWith(`${BASE}approve.html?id=`));
    assert.deepEqual(h.log.native, [`qsdm_ping@qsdm-extension://wallet-popup`, `qsdm_getWalletInfo@qsdm-extension://wallet-popup`, `qsdm_accounts@${SITE}`]);
    assert.ok(h.log.fetches[0].endsWith("/api/v1/status"));
    const p = h.openPort(h.log.windows[0]);
    assert.equal(p.posted[0].type, "request");
    assert.equal(p.posted[0].origin, SITE);
    assert.equal(p.posted[0].address, ME);
    assert.equal(p.posted[0].network, "qsdm-mainnet");
    assert.equal(h.response(id), undefined, "no answer before the decision");
    p.send({ type: "ping" });
    p.send({ type: "approve" });
    await tick(20);
    assert.equal(h.log.native.at(-1), `qsdm_requestAccounts@${SITE}`);
    assert.deepEqual(h.response(id), { ok: true, result: [ME] });
    assert.deepEqual(p.posted.map((m) => m.type), ["request", "hive", "done"]);
    assert.equal(p.posted.at(-1).ok, true);
    // 2. Already connected: no window the second time.
    const again = h.fromSite(SITE, "qsdm_requestAccounts");
    await tick(30);
    assert.equal(h.log.windows.length, 1, "no window for a connected site");
    assert.deepEqual(h.response(again), { ok: true, result: [ME] });
    // 3. A transfer from the connected site: window, approve, tx id to the window.
    const send = h.fromSite(SITE, "qsdm_sendTransaction", { recipient: "a".repeat(64), amount: 1.25 });
    await tick(30);
    assert.equal(h.log.windows.length, 2);
    const ps = h.openPort(h.log.windows[1]);
    assert.deepEqual(ps.posted[0].params, { recipient: "a".repeat(64), amount: 1.25 });
    ps.send({ type: "approve" });
    await tick(20);
    assert.deepEqual(h.response(send), { ok: true, result: { transaction_id: "tx-123" } });
    assert.equal(ps.posted.at(-1).txId, "tx-123");
    console.log("connect, remembered site, transfer: ok");
  }
  // 4. Cancel, and closing the window, refuse with code 4001 and never ask Hive.
  {
    const h = load(hiveWith());
    const a = h.fromSite(SITE, "qsdm_requestAccounts");
    await tick(30);
    const p = h.openPort(h.log.windows[0]);
    p.send({ type: "cancel" });
    await tick(5);
    assert.equal(h.response(a).code, 4001);
    assert.equal(h.response(a).ok, false);
    const b = h.fromSite("https://other.example", "qsdm_requestAccounts");
    await tick(30);
    const q = h.openPort(h.log.windows[1]);
    q.close();
    await tick(5);
    assert.equal(h.response(b).code, 4001);
    assert.ok(!h.log.native.some((n) => n.startsWith("qsdm_requestAccounts")), "Hive never asked");
    console.log("cancel and close: ok (4001, Hive not asked)");
  }
  // 5. One waiting request per site; a second one is refused and the window focused.
  {
    const h = load(hiveWith());
    h.fromSite(SITE, "qsdm_requestAccounts");
    await tick(30);
    const second = h.fromSite(SITE, "qsdm_requestAccounts");
    await tick(30);
    assert.equal(h.log.windows.length, 1);
    assert.match(h.response(second).error, /waiting in the QSDM Wallet window/);
    assert.equal(h.log.updates.length, 1, "the waiting window is focused");
    console.log("one request per site: ok");
  }
  // 6. A site that is not connected asking to send or sign: Hive answers, no window.
  {
    const h = load(hiveWith());
    const s = h.fromSite(SITE, "qsdm_sendTransaction", { recipient: "a".repeat(64), amount: 1 });
    await tick(30);
    assert.equal(h.log.windows.length, 0);
    assert.deepEqual(h.response(s), { ok: false, error: "site not connected" });
    console.log("unconnected site send: passes to Hive, no window: ok");
  }
  // 7. Hive closed: no window, Hive's own error.
  {
    const h = load(() => ({ ok: false, error: "Hive closed" }));
    const s = h.fromSite(SITE, "qsdm_requestAccounts");
    await tick(30);
    assert.equal(h.log.windows.length, 0);
    assert.equal(h.response(s).ok, false);
    console.log("hive closed: no window: ok");
  }
  // 8. A port that is not an extension page, or an unknown id: refused.
  {
    const h = load(hiveWith());
    h.fromSite(SITE, "qsdm_requestAccounts");
    await tick(30);
    const evil = h.openPort(h.log.windows[0], "https://evil.example/approve.html");
    assert.deepEqual(evil.posted.map((m) => m.type), ["gone", "(disconnected by worker)"]);
    const unknown = h.openPort(`${BASE}approve.html?id=nope`);
    assert.equal(unknown.posted[0].type, "gone");
    console.log("foreign and unknown approval ports refused: ok");
  }
  // 9. The read-only API relay: extension pages only, fixed paths only.
  {
    const h = load(hiveWith());
    const ok = await h.apiMessage("/status", `${BASE}popup.html`);
    assert.equal(ok.ok, true);
    assert.equal(ok.json.chain_tip, 1050000);
    const rec = await h.apiMessage("/receipts?from=1&to=160&limit=200", `${BASE}popup.html?full=1`);
    assert.equal(rec.ok, true);
    const fromWeb = await h.apiMessage("/status", "https://evil.example/");
    assert.equal(fromWeb.ok, false);
    const badPath = await h.apiMessage("/wallet/send", `${BASE}popup.html`);
    assert.equal(badPath.ok, false);
    const traversal = await h.apiMessage("/receipts/../auth/login", `${BASE}popup.html`);
    assert.equal(traversal.ok, false);
    assert.equal(h.log.fetches.length, 2, "only the two allowed reads were fetched");
    console.log("api relay: ok");
  }
  console.log("background approvals: all passed");
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
