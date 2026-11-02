/* Minimal header/footer fallback for pages that use the shared site shell
 * placeholders (<header id="site-header">, <footer id="site-footer">).
 * If /assets/site-shell.js is present it fills them and this does nothing.
 * If it is missing (pre-merge) or fails, a plain header with a link back
 * home is rendered so the page is still navigable. */
(function () {
  "use strict";

  function fill() {
    var header = document.getElementById("site-header");
    if (header && !header.children.length && !header.textContent.trim()) {
      header.classList.add("shell-fallback");
      header.innerHTML =
        '<div class="shell-fallback-inner">' +
        '<a class="shell-fallback-brand" href="/"><img src="/assets/brand/qsdm-logo-small.svg" alt="" width="26" height="26" /><span>QSDM</span></a>' +
        '<nav aria-label="Primary">' +
        '<a href="/">Home</a><a href="/explorer.html">Explorer</a><a href="/wallet.html">Wallet</a>' +
        '<a href="/network.html">Network</a><a href="/docs/">Docs</a>' +
        "</nav></div>";
    }
    var footer = document.getElementById("site-footer");
    if (footer && !footer.children.length && !footer.textContent.trim()) {
      footer.classList.add("shell-fallback");
      footer.innerHTML =
        '<div class="shell-fallback-inner"><span>QSDM · CELL</span>' +
        '<nav aria-label="Footer"><a href="/docs/">Docs</a><a href="/api.html">API</a><a href="/audit.html">Audit</a>' +
        '<a href="/.well-known/security.txt">Security</a><a href="https://github.com/blackbeardONE/QSDM" rel="noopener noreferrer">GitHub ↗</a></nav></div>';
    }
  }

  // site-shell.js may render asynchronously; give it a moment after load.
  if (document.readyState === "complete") setTimeout(fill, 400);
  else window.addEventListener("load", function () { setTimeout(fill, 400); }, { once: true });
})();
