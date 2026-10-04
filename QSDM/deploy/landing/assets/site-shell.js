/* QSDM site shell: renders the one shared header/nav, footer and status
 * banner, and exposes small helpers (window.QSDM) for API calls, number
 * formatting and copy buttons. No dependencies, no trackers, no CDNs.
 * Contract and usage: /assets/SITE-SHELL.md
 */
(function () {
  "use strict";

  // ---- status banner: edit the text here; it is shown on every shell page.
  var BANNER_TEXT =
    "Mining is open for enrolled QSDM Hive miners. Hive 1.4.21 is available — Hive 1.4.20 users must install it manually from the Download page before 19 December 2026. Paid resource work is still paused.";
  var BANNER_ENABLED = true;

  // ---- primary nav (keep <= 7). `pages` = data-page values that light it up.
  var NAV = [
    { href: "/", label: "Home", pages: ["home"] },
    { href: "/explorer.html", label: "Explorer", pages: ["explorer"] },
    { href: "/wallet.html", label: "Wallet", pages: ["wallet"] },
    { href: "/mining.html", label: "Mining", pages: ["mining"] },
    { href: "/download.html", label: "Download", pages: ["download"] },
    { href: "/api.html", label: "Developers", pages: ["api", "docs"] },
    { href: "/network.html", label: "Network", pages: ["network", "validators", "trust", "audit"] }
  ];

  // ---- footer columns. ext: true marks an off-site link (rendered with an arrow).
  var FOOTER = [
    {
      title: "Network",
      links: [
        { href: "/explorer.html", label: "Explorer" },
        { href: "/network.html", label: "Network status" },
        { href: "/mining.html", label: "Mining" },
        { href: "/validators.html", label: "Validators" },
        { href: "/trust.html", label: "Attestations" }
      ]
    },
    {
      title: "Products",
      links: [
        { href: "/download.html", label: "Download Hive" },
        { href: "/wallet.html", label: "Web wallet" },
        { href: "/account/", label: "QSDM Account" },
        { href: "https://qsdm.online/", label: "QSDM VPN", ext: true }
      ]
    },
    {
      title: "Developers",
      links: [
        { href: "/api.html", label: "API reference" },
        { href: "/docs/", label: "Docs" },
        { href: "https://github.com/blackbeardONE/QSDM", label: "GitHub", ext: true }
      ]
    },
    {
      title: "Trust",
      links: [
        { href: "/audit.html", label: "Public audit" },
        { href: "/.well-known/security.txt", label: "Security" },
        { href: "/privacy.html", label: "Privacy" },
        { href: "/support.html", label: "Support" }
      ]
    }
  ];

  // ---- brand assets (sources and renders: brand/final/ in the website repo).
  // `mark` is the small-size QSDM mark for the 28 px header/footer slot.
  var BRAND = {
    mark: "/assets/brand/qsdm-logo-small.svg",
    icons: [
      { rel: "icon", href: "/favicon.ico", sizes: "16x16 32x32 48x48" },
      { rel: "icon", href: "/assets/brand/favicon-32.png", sizes: "32x32", type: "image/png" },
      { rel: "icon", href: "/assets/brand/favicon-16.png", sizes: "16x16", type: "image/png" },
      { rel: "apple-touch-icon", href: "/assets/brand/apple-touch-icon.png" },
      { rel: "manifest", href: "/site.webmanifest" }
    ]
  };

  var PUBLIC_API = "https://api.qsdm.tech/api/v1";
  var PUBLIC_ATTEST = "https://api.qsdm.tech/attest/home-validator/api/v1";

  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function link(l, extra) {
    var attrs = ' href="' + esc(l.href) + '"';
    if (l.ext) attrs += ' class="ext" rel="noopener"';
    return "<a" + attrs + (extra || "") + ">" + esc(l.label) +
      (l.ext ? '<span class="visually-hidden"> (external site)</span>' : "") + "</a>";
  }

  var page = (document.body && document.body.getAttribute("data-page")) || "";

  function navLinks() {
    return NAV.map(function (l) {
      var on = l.pages.indexOf(page) !== -1;
      return link(l, on ? ' aria-current="page"' : "");
    }).join("");
  }

  function renderBanner() {
    if (!BANNER_ENABLED || document.getElementById("qsdm-migration-notice")) return;
    var b = document.createElement("div");
    b.id = "qsdm-migration-notice";
    b.setAttribute("role", "status");
    b.textContent = BANNER_TEXT;
    document.body.insertBefore(b, document.body.firstChild);
  }

  function renderSkipLink() {
    if (document.querySelector(".skip-link") || !document.getElementById("main")) return;
    var a = document.createElement("a");
    a.className = "skip-link";
    a.href = "#main";
    a.textContent = "Skip to content";
    document.body.insertBefore(a, document.body.firstChild);
  }

  // Pages should carry the static <link>s in their <head> (works without JS);
  // this only fills in whichever of them a page is missing.
  function ensureIcons() {
    var head = document.head;
    if (!head) return;
    var hasIcon = !!head.querySelector('link[rel~="icon"]');
    BRAND.icons.forEach(function (i) {
      if (i.rel === "icon" ? hasIcon : head.querySelector('link[rel="' + i.rel + '"]')) return;
      var l = document.createElement("link");
      l.rel = i.rel;
      l.href = i.href;
      if (i.sizes) l.setAttribute("sizes", i.sizes);
      if (i.type) l.type = i.type;
      head.appendChild(l);
    });
  }

  function renderHeader() {
    var h = document.getElementById("site-header");
    if (!h || h.getAttribute("data-rendered")) return;
    h.setAttribute("data-rendered", "1");
    h.innerHTML =
      '<div class="shell-bar">' +
        '<a class="site-brand" href="/" aria-label="QSDM home">' +
          '<img src="' + BRAND.mark + '" alt="" width="28" height="28" />' +
          "<span>QSDM</span>" +
        "</a>" +
        '<nav class="shell-nav" aria-label="Primary">' + navLinks() + "</nav>" +
        '<button class="shell-toggle" type="button" aria-controls="shell-mobile" aria-expanded="false" aria-label="Open menu">' +
          '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M20 7H4V5H20V7ZM20 13H4V11H20V13ZM20 19H4V17H20V19Z"/></svg>' +
        "</button>" +
      "</div>" +
      '<nav class="shell-mobile" id="shell-mobile" aria-label="Primary (mobile)">' + navLinks() + "</nav>";

    var toggle = h.querySelector(".shell-toggle");
    var mobile = h.querySelector(".shell-mobile");
    toggle.addEventListener("click", function () {
      var open = mobile.classList.toggle("is-open");
      toggle.setAttribute("aria-expanded", String(open));
      toggle.setAttribute("aria-label", open ? "Close menu" : "Open menu");
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && mobile.classList.contains("is-open")) {
        mobile.classList.remove("is-open");
        toggle.setAttribute("aria-expanded", "false");
        toggle.setAttribute("aria-label", "Open menu");
        toggle.focus();
      }
    });
  }

  function renderFooter() {
    var f = document.getElementById("site-footer");
    if (!f || f.getAttribute("data-rendered")) return;
    f.setAttribute("data-rendered", "1");
    var cols = FOOTER.map(function (c) {
      return '<div class="shell-foot-col"><h2>' + esc(c.title) + "</h2>" +
        c.links.map(function (l) { return link(l); }).join("") + "</div>";
    }).join("");
    f.innerHTML =
      '<div class="q-wrap">' +
        '<div class="shell-foot-grid">' +
          '<div class="shell-foot-brand">' +
            '<a class="site-brand" href="/"><img src="' + BRAND.mark + '" alt="" width="28" height="28" /><span>QSDM</span></a>' +
            "<p>Quantum-Secure Dynamic Mesh Ledger. Native coin CELL; wallet transactions signed with ML-DSA-87. Pilot network (pre-mainnet).</p>" +
            '<p class="mono">API ' + esc(PUBLIC_API) + "</p>" +
          "</div>" + cols +
        "</div>" +
        '<div class="shell-foot-bottom">' +
          '<span>&copy; 2024&ndash;2026 QSDM &middot; <a class="ext" rel="noopener" href="https://github.com/blackbeardONE/QSDM/blob/main/LICENSE">MIT License<span class="visually-hidden"> (external site)</span></a></span>' +
          "<span>No trackers. No third-party scripts.</span>" +
        "</div>" +
      "</div>";
  }

  // ---------------------------------------------------------------- helpers
  // API base: window.QSDM_API_BASE (string) pins one base, e.g. "/api/v1" or
  // "https://api.qsdm.tech/api/v1". Unset = production behaviour: same-origin
  // /api/v1 first, then the public API host.
  function apiBases() {
    var b = window.QSDM_API_BASE;
    if (typeof b === "string" && b) return [b.replace(/\/+$/, "")];
    return ["/api/v1", PUBLIC_API];
  }
  function attestBases() {
    var b = window.QSDM_ATTEST_BASE;
    if (typeof b === "string" && b) return [b.replace(/\/+$/, "")];
    return ["/attest/home-validator/api/v1", PUBLIC_ATTEST];
  }

  var preferred = {};
  function getJSON(bases, path, key) {
    var list = bases.slice();
    if (preferred[key] && list.indexOf(preferred[key]) > 0) {
      list.splice(list.indexOf(preferred[key]), 1);
      list.unshift(preferred[key]);
    }
    var i = 0;
    function next(lastErr) {
      if (i >= list.length) return Promise.reject(lastErr || new Error("unavailable"));
      var base = list[i++];
      return fetch(base + path, { cache: "no-store", headers: { Accept: "application/json" } })
        .then(function (r) {
          var ct = r.headers.get("content-type") || "";
          if (!r.ok || ct.indexOf("json") === -1) throw new Error("HTTP " + r.status);
          return r.json().then(function (j) { preferred[key] = base; return j; });
        })
        .catch(function (e) { return next(e); });
    }
    return next();
  }

  var NUM = typeof Intl !== "undefined" ? new Intl.NumberFormat("en-US") : null;
  function fmtInt(n) {
    var v = Number(n);
    if (n === null || n === undefined || n === "" || !isFinite(v)) return "—";
    return NUM ? NUM.format(Math.round(v)) : String(Math.round(v));
  }
  // Format a decimal string such as "2670028.36988325" without float loss.
  function fmtDec(s, places) {
    if (s === null || s === undefined || s === "") return "—";
    var str = String(s);
    if (!/^-?\d+(\.\d+)?$/.test(str)) return "—";
    var parts = str.split(".");
    var whole = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    var frac = parts[1] || "";
    if (places === undefined) return frac ? whole + "." + frac : whole;
    if (places === 0) return whole;
    frac = (frac + "00000000").slice(0, places);
    return whole + "." + frac;
  }
  function fmtDuration(sec) {
    var s = Number(sec);
    if (!isFinite(s) || s < 0) return "—";
    s = Math.round(s);
    if (s < 60) return s + " s";
    if (s < 3600) return Math.floor(s / 60) + " min " + (s % 60) + " s";
    if (s < 86400) return Math.floor(s / 3600) + " h " + Math.floor((s % 3600) / 60) + " min";
    if (s < 86400 * 400) return Math.floor(s / 86400) + " d " + Math.floor((s % 86400) / 3600) + " h";
    return (s / (86400 * 365.25)).toFixed(1) + " years";
  }
  function ageOf(iso) {
    var t = Date.parse(iso);
    if (!isFinite(t)) return null;
    return Math.max(0, (Date.now() - t) / 1000);
  }
  function setText(id, text) {
    var el = typeof id === "string" ? document.getElementById(id) : id;
    if (el) el.textContent = text;
  }
  function short(h, n) {
    if (!h) return "—";
    n = n || 10;
    return h.length > n * 2 + 1 ? h.slice(0, n) + "…" + h.slice(-6) : h;
  }

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
    return new Promise(function (resolve, reject) {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand("copy") ? resolve() : reject(new Error("copy failed")); }
      catch (e) { reject(e); }
      document.body.removeChild(ta);
    });
  }
  // <button data-copy="text"> or <button data-copy-target="elementId">
  document.addEventListener("click", function (e) {
    var btn = e.target.closest && e.target.closest("[data-copy],[data-copy-target]");
    if (!btn) return;
    var text = btn.getAttribute("data-copy");
    if (text === null) {
      var t = document.getElementById(btn.getAttribute("data-copy-target"));
      text = t ? t.textContent : "";
    }
    var label = btn.getAttribute("data-label") || btn.textContent;
    btn.setAttribute("data-label", label);
    copyText(text).then(function () {
      btn.textContent = "Copied";
      btn.classList.add("is-copied");
    }, function () {
      btn.textContent = "Copy failed";
    }).then(function () {
      setTimeout(function () { btn.textContent = label; btn.classList.remove("is-copied"); }, 1600);
    });
  });

  window.QSDM = {
    PUBLIC_API: PUBLIC_API,
    apiBases: apiBases,
    api: function (path) { return getJSON(apiBases(), path, "api"); },
    attest: function (path) { return getJSON(attestBases(), path, "attest"); },
    fmtInt: fmtInt,
    fmtDec: fmtDec,
    fmtDuration: fmtDuration,
    ageOf: ageOf,
    setText: setText,
    short: short,
    copyText: copyText
  };

  // Render now if the placeholders already exist (script placed right after
  // the header), and again on DOMContentLoaded for anything parsed later.
  function renderAll() { ensureIcons(); renderHeader(); renderFooter(); renderBanner(); renderSkipLink(); }
  if (document.body) renderAll();
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", renderAll);
})();
