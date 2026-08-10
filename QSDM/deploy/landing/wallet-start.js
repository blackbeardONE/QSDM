(() => {
  "use strict";

  const DOWNLOAD_URL = "/download.html#wallet-extension-title";
  const status = document.getElementById("wallet-handoff-status");
  const openButton = document.getElementById("wallet-handoff-open");
  let opening = false;

  const provider = () =>
    window.qsdm?.isQsdmHive === true &&
    typeof window.qsdm.request === "function"
      ? window.qsdm
      : null;

  const openOnboarding = async () => {
    if (opening) return;
    const qsdm = provider();
    if (!qsdm) {
      status.textContent = "QSDM Wallet is not installed. Opening the official download...";
      window.setTimeout(() => window.location.replace(DOWNLOAD_URL), 500);
      return;
    }

    opening = true;
    openButton.disabled = true;
    status.textContent = "QSDM Wallet detected. Opening onboarding...";
    try {
      const result = await qsdm.request({
        method: "qsdm_openOnboarding",
        params: { login: "new" },
      });
      if (!result?.opened) throw new Error("The extension did not open onboarding");
      status.textContent = "QSDM Wallet opened in a new tab. You can close this page.";
    } catch (error) {
      status.textContent =
        error instanceof Error ? error.message : "QSDM Wallet could not be opened.";
      openButton.disabled = false;
      opening = false;
    }
  };

  const detect = () => {
    if (provider()) {
      void openOnboarding();
      return true;
    }
    return false;
  };

  openButton.addEventListener("click", () => void openOnboarding());
  window.addEventListener("qsdm#initialized", detect, { once: true });

  if (!detect()) {
    let attempts = 0;
    const timer = window.setInterval(() => {
      attempts += 1;
      if (detect()) {
        window.clearInterval(timer);
      } else if (attempts >= 6) {
        window.clearInterval(timer);
        status.textContent = "QSDM Wallet is not installed. Opening the official download...";
        window.setTimeout(() => window.location.replace(DOWNLOAD_URL), 700);
      }
    }, 250);
  }
})();
