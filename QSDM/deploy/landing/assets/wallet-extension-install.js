(() => {
  "use strict";

  const extensionSection = document.querySelector(
    "[data-wallet-extension-version]"
  );
  if (!extensionSection) return;
  const extensionVersion = extensionSection.dataset.walletExtensionVersion;
  const cards = Array.from(
    extensionSection.querySelectorAll("[data-wallet-browser-card]")
  );
  if (!extensionVersion || !cards.length) return;

  const browserFromUserAgent = () => {
    const agent = navigator.userAgent;
    if (/Firefox\//i.test(agent)) return "firefox";
    if (/Edg\//i.test(agent)) return "edge";
    if (/Brave\//i.test(agent) || navigator.brave) return "brave";
    if (/Chrome\//i.test(agent)) return "chrome";
    return "";
  };

  const allowedStoreHosts = {
    chrome: new Set(["chromewebstore.google.com"]),
    edge: new Set(["microsoftedge.microsoft.com"]),
    brave: new Set(["chromewebstore.google.com"]),
    firefox: new Set(["addons.mozilla.org"]),
  };

  const recommendedBrowser = browserFromUserAgent();
  cards.forEach((card) => {
    if (card.dataset.walletBrowserCard !== recommendedBrowser) return;
    card.classList.add("is-recommended");
    const marker = card.querySelector("[data-browser-recommended]");
    if (marker) marker.hidden = false;
  });

  const publishChannel = (browser, channel) => {
    if (!channel || channel.state !== "published") return;
    if (typeof channel.install_url !== "string") return;

    let installUrl;
    try {
      installUrl = new URL(channel.install_url);
    } catch {
      return;
    }
    if (
      installUrl.protocol !== "https:" ||
      !allowedStoreHosts[browser]?.has(installUrl.hostname)
    ) {
      return;
    }

    const card = cards.find(
      (candidate) => candidate.dataset.walletBrowserCard === browser
    );
    if (!card) return;

    const action = card.querySelector("[data-browser-install]");
    const pending = card.querySelector("[data-browser-pending]");
    if (action) {
      action.href = installUrl.href;
      action.hidden = false;
    }
    if (pending) pending.textContent = `Available from ${channel.store}`;
  };

  fetch("/assets/browser-extension-distribution.json", {
    credentials: "same-origin",
    cache: "no-store",
  })
    .then((response) => {
      if (!response.ok) throw new Error("distribution metadata unavailable");
      return response.json();
    })
    .then((distribution) => {
      if (
        distribution?.schema !== "qsdm.wallet-extension-distribution.v1" ||
        distribution?.version !== extensionVersion
      ) {
        return;
      }
      Object.entries(distribution.channels || {}).forEach(
        ([browser, channel]) => publishChannel(browser, channel)
      );
    })
    .catch(() => {
      // The static pending state is the secure fallback.
    });
})();
