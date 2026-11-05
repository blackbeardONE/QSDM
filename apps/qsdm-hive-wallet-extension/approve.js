// QSDM Wallet extension: the approval window a website's request opens (background.js, approveThenSend).
// It shows the real origin and exactly what the site asks for. Cancel (or closing the window) refuses it; the other
// button hands the same request to QSDM Hive, which shows it again and decides. Nothing here signs anything.
(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const id = new URLSearchParams(window.location.search).get("id") || "";
  let request = null;
  let finished = false;

  // The wallet's theme (popup.html saves it here).
  try {
    const prefs = JSON.parse(window.localStorage.getItem("qsdm-wallet:prefs") || "{}");
    const dark =
      prefs.theme === "dark" ||
      (prefs.theme !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.dataset.theme = dark ? "dark" : "light";
  } catch {
    // default theme
  }

  const groupedAddress = (a) => String(a || "").replace(/(.{8})/g, "$1 ").trim();
  const shortAddress = (a) => (a && a.length > 20 ? `${a.slice(0, 8)}…${a.slice(-8)}` : a || "");

  // A CELL amount from a website, shown with 8 decimals; null when it is not one.
  const cellText = (value) => {
    const n = Number(value);
    if (!Number.isFinite(n) || n <= 0) return null;
    const fixed = n.toFixed(8);
    const [whole, frac] = fixed.split(".");
    return `${BigInt(whole).toLocaleString("en-US")}.${frac}`;
  };

  const fact = (label, value, { mono = true, block = false, warn = false } = {}) => {
    const row = document.createElement("div");
    if (block) row.className = "addr";
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = value;
    if (!mono) dd.classList.add("plain");
    if (warn) dd.classList.add("warn");
    row.append(dt, dd);
    return row;
  };

  const showState = (kind, title, text) => {
    const box = $("approve-state");
    box.hidden = false;
    box.dataset.state = kind;
    $("state-icon").setAttribute("href", kind === "ok" ? "#i-check" : kind === "error" ? "#i-x" : "#i-hive");
    $("state-title").textContent = title;
    $("state-text").textContent = text;
    $("approve-note").hidden = true;
  };

  const render = (r) => {
    request = r;
    const host = (() => {
      try {
        return new URL(r.origin).hostname;
      } catch {
        return r.origin;
      }
    })();
    $("origin-text").textContent = r.origin;
    document.querySelector(".origin-chip").title = r.origin;
    $("site-letter").textContent = (host.replace(/^www\./, "")[0] || "?").toUpperCase();
    $("avatar").textContent = r.address.slice(0, 2).toUpperCase();
    $("account-short").textContent = shortAddress(r.address);

    const facts = $("approve-facts");
    facts.replaceChildren();
    const network = r.network || "QSDM";
    const approve = $("approve");

    if (r.method === "qsdm_requestAccounts") {
      document.title = `Connect ${host} - QSDM Wallet`;
      $("approve-title").textContent = "Connect with QSDM Wallet";
      $("approve-sub").textContent = `${host} asks to see your wallet address.`;
      $("approve-trust").hidden = false;
      facts.append(
        fact("Origin", r.origin),
        fact("Account", groupedAddress(r.address), { block: true }),
        fact("Chain", "QSDM")
      );
      if (r.network) facts.append(fact("Network", r.network));
      $("approve-note-text").textContent =
        "The site sees only this public address. Each transfer or signature it asks for later needs your approval again. QSDM Hive asks you to confirm this connection.";
      approve.textContent = "Connect";
    } else if (r.method === "qsdm_sendTransaction") {
      document.title = `Send CELL for ${host} - QSDM Wallet`;
      const amount = cellText(r.params.amount);
      const recipient = String(r.params.recipient || "");
      const validRecipient = /^[0-9a-fA-F]{64}$/.test(recipient);
      $("approve-title").textContent = "Send CELL";
      $("approve-sub").textContent = `${host} asks to send CELL from your wallet.`;
      $("approve-trust").hidden = true;
      facts.append(
        fact("Origin", r.origin),
        fact("To", validRecipient ? groupedAddress(recipient.toLowerCase()) : recipient || "(none)", {
          block: true,
          warn: !validRecipient,
        }),
        fact("Amount", amount ? `${amount} CELL` : "(not a valid amount)", { warn: !amount }),
        fact("Network fee", "0 CELL"),
        fact("Total", amount ? `${amount} CELL` : "-")
      );
      $("approve-note-text").textContent =
        "Check the address and the amount: transfers cannot be undone. QSDM Hive shows this transfer again and signs it only after you approve it there.";
      approve.textContent = "Continue";
      if (!amount || !validRecipient) {
        approve.disabled = true;
        $("approve-note-text").textContent = "This request is not a valid QSDM transfer, so it cannot be approved. Cancel it.";
        $("approve-note").classList.add("warn");
        return;
      }
    } else if (r.method === "qsdm_signMessage") {
      document.title = `Sign for ${host} - QSDM Wallet`;
      $("approve-title").textContent = "Sign a message";
      $("approve-sub").textContent = `${host} asks your wallet to sign this message.`;
      $("approve-trust").hidden = true;
      facts.append(fact("Origin", r.origin), fact("Account", groupedAddress(r.address), { block: true }));
      $("message-box").hidden = false;
      $("message-text").textContent = r.params.message || "";
      $("approve-note-text").textContent =
        "Signing proves you own this wallet. It does not send CELL. Sign only messages you understand; QSDM Hive asks you to confirm.";
      approve.textContent = "Continue";
    } else {
      $("approve-title").textContent = "Unknown request";
      approve.disabled = true;
      return;
    }
    approve.disabled = false;
  };

  let port;
  try {
    port = chrome.runtime.connect({ name: `qsdm-approval:${id}` });
  } catch {
    port = null;
  }

  const gone = () => {
    if (finished) return;
    finished = true;
    $("approve").disabled = true;
    $("cancel").textContent = "Close";
    showState("error", "This request is no longer waiting", "Go back to the website and ask again.");
  };

  if (!port) gone();
  port?.onMessage.addListener((message) => {
    if (message?.type === "request") render(message);
    else if (message?.type === "gone") gone();
    else if (message?.type === "hive") {
      showState("waiting", "Confirm in QSDM Hive", "Hive is showing the same request. Approve it there.");
    } else if (message?.type === "done") {
      finished = true;
      $("cancel").textContent = "Close";
      $("cancel").disabled = false;
      if (message.ok) {
        const what =
          request?.method === "qsdm_requestAccounts"
            ? "Connected. You can go back to the website."
            : request?.method === "qsdm_sendTransaction"
              ? `Sent.${message.txId ? ` Transaction ${message.txId}.` : ""}`
              : "Signed. The website has the signature.";
        showState("ok", "Done", what);
        setTimeout(() => window.close(), 1800);
      } else {
        showState("error", "Not approved", message.error || "QSDM Hive did not approve the request.");
      }
    }
  });
  port?.onDisconnect.addListener(() => {
    void chrome.runtime.lastError;
    if (!finished && request) gone();
  });

  // Keeps the extension's background worker awake while this window waits for an answer.
  setInterval(() => {
    try {
      port?.postMessage({ type: "ping" });
    } catch {
      // disconnected
    }
  }, 20000);

  $("approve").addEventListener("click", () => {
    if (!request || finished) return;
    $("approve").disabled = true;
    $("cancel").disabled = true;
    showState("waiting", "Confirm in QSDM Hive", "Hive is showing the same request. Approve it there.");
    port?.postMessage({ type: "approve" });
  });

  $("cancel").addEventListener("click", () => {
    if (!finished && request) port?.postMessage({ type: "cancel" });
    finished = true;
    setTimeout(() => window.close(), 50);
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !$("cancel").disabled) $("cancel").click();
  });
})();
