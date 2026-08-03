(() => {
  "use strict";

  const API = "/api/account";
  const elements = {
    loading: document.getElementById("loading-view"),
    login: document.getElementById("login-view"),
    dashboard: document.getElementById("dashboard-view"),
    signOut: document.getElementById("sign-out"),
    telegram: document.getElementById("telegram-sign-in"),
    emailForm: document.getElementById("email-form"),
    email: document.getElementById("email"),
    emailSubmit: document.getElementById("email-submit"),
    loginStatus: document.getElementById("login-status"),
    identity: document.getElementById("identity-summary"),
    walletCount: document.getElementById("wallet-count"),
    totalBalance: document.getElementById("total-balance"),
    walletList: document.getElementById("wallet-list"),
    walletEmpty: document.getElementById("wallet-empty"),
    linkWallet: document.getElementById("link-wallet"),
    refreshWallets: document.getElementById("refresh-wallets"),
    dashboardStatus: document.getElementById("dashboard-status"),
    emailIdentityValue: document.getElementById("email-identity-value"),
    telegramIdentityValue: document.getElementById("telegram-identity-value"),
    addEmailIdentity: document.getElementById("add-email-identity"),
    emailIdentityForm: document.getElementById("email-identity-form"),
    identityEmail: document.getElementById("identity-email"),
    identityEmailSubmit: document.getElementById("identity-email-submit"),
    addTelegramIdentity: document.getElementById("add-telegram-identity"),
    identityMethodStatus: document.getElementById("identity-method-status"),
  };

  let config = { login: { email: false, telegram: false } };
  let account = null;
  let csrfToken = "";
  let emailIdentityFormOpen = false;
  let bootNotice = null;

  const setView = (name) => {
    elements.loading.hidden = name !== "loading";
    elements.login.hidden = name !== "login";
    elements.dashboard.hidden = name !== "dashboard";
    elements.signOut.hidden = name !== "dashboard";
  };

  const setStatus = (element, message, state = "info") => {
    element.textContent = message || "";
    element.dataset.state = state;
    element.hidden = !message;
  };

  const api = async (path, options = {}) => {
    const response = await fetch(API + path, {
      credentials: "same-origin",
      ...options,
      headers: {
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...(options.csrf ? { "X-QSDM-CSRF": csrfToken } : {}),
        ...(options.headers || {}),
      },
    });
    let payload = {};
    try {
      payload = await response.json();
    } catch {
      payload = {};
    }
    if (!response.ok) {
      const error = new Error(
        payload?.error?.message || `Request failed (${response.status})`
      );
      error.status = response.status;
      error.code = payload?.error?.code || "request_failed";
      throw error;
    }
    return payload;
  };

  const shorten = (value) =>
    value && value.length > 28
      ? `${value.slice(0, 14)}...${value.slice(-12)}`
      : value || "";

  const formatDate = (value) => {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleString();
  };

  const balanceFor = async (address) => {
    try {
      const response = await fetch(
        `/api/v1/wallet/balance?address=${encodeURIComponent(address)}`,
        {
          credentials: "omit",
          cache: "no-store",
        }
      );
      if (!response.ok) return null;
      const payload = await response.json();
      const value = Number(
        payload.balance ?? payload.available_balance ?? payload.account?.balance
      );
      return Number.isFinite(value) ? value : null;
    } catch {
      return null;
    }
  };

  const renderWallets = async () => {
    const wallets = Array.isArray(account?.wallets) ? account.wallets : [];
    elements.walletList.replaceChildren();
    elements.walletCount.textContent = String(wallets.length);
    elements.walletEmpty.hidden = wallets.length > 0;
    let total = 0;
    let complete = true;

    const rows = wallets.map((wallet) => {
      const row = document.createElement("article");
      row.className = "wallet-row";
      const identity = document.createElement("div");
      const address = document.createElement("div");
      address.className = "wallet-address";
      address.textContent = shorten(wallet.address);
      address.title = wallet.address;
      const meta = document.createElement("div");
      meta.className = "wallet-meta";
      meta.textContent = `Linked ${formatDate(wallet.linked_at)}`;
      identity.append(address, meta);
      const source = document.createElement("div");
      source.className = "wallet-source";
      source.textContent = "ML-DSA ownership verified";
      const balance = document.createElement("div");
      balance.className = "wallet-balance";
      balance.textContent = "Checking...";

      const unlink = document.createElement("button");
      unlink.className = "button quiet wallet-unlink";
      unlink.type = "button";
      unlink.textContent = "Unlink";
      unlink.addEventListener("click", async () => {
        if (
          !window.confirm(
            `Unlink ${wallet.address} from this QSDM Account? This does not move or delete CELL.`
          )
        ) {
          return;
        }
        unlink.disabled = true;
        setStatus(
          elements.dashboardStatus,
          "Removing the public wallet link..."
        );
        try {
          await api("/wallets/unlink", {
            method: "POST",
            csrf: true,
            body: JSON.stringify({ address: wallet.address }),
          });
          setStatus(
            elements.dashboardStatus,
            "Wallet unlinked. Your local wallet was not changed.",
            "success"
          );
          await loadSession();
        } catch (error) {
          setStatus(elements.dashboardStatus, error.message, "error");
          unlink.disabled = false;
        }
      });

      row.append(identity, source, balance, unlink);
      elements.walletList.append(row);
      return { wallet, balance };
    });

    await Promise.all(
      rows.map(async ({ wallet, balance }) => {
        const value = await balanceFor(wallet.address);
        if (value === null) {
          complete = false;
          balance.textContent = "Unavailable";
          return;
        }
        total += value;
        balance.textContent = `${value.toLocaleString(undefined, {
          maximumFractionDigits: 8,
        })} CELL`;
      })
    );
    elements.totalBalance.textContent = complete
      ? `${total.toLocaleString(undefined, { maximumFractionDigits: 8 })} CELL`
      : wallets.length
      ? "Partially available"
      : "0 CELL";
  };

  const renderAccount = async () => {
    const identities = [account.email, account.telegram].filter(Boolean);
    elements.identity.textContent = identities.length
      ? `Signed in as ${identities.join(" / ")}`
      : `Account ${shorten(account.id)}`;
    elements.emailIdentityValue.textContent = account.email || "Not added";
    elements.telegramIdentityValue.textContent =
      account.telegram || "Not added";
    elements.addEmailIdentity.hidden =
      Boolean(account.email) || !config.login.email;
    elements.addTelegramIdentity.hidden =
      Boolean(account.telegram) || !config.login.telegram;
    elements.emailIdentityForm.hidden =
      Boolean(account.email) || !config.login.email || !emailIdentityFormOpen;
    await renderWallets();
  };

  const loadSession = async () => {
    try {
      const payload = await api("/me");
      account = payload.account;
      csrfToken = payload.csrf_token;
      setView("dashboard");
      await renderAccount();
      return true;
    } catch (error) {
      if (error.status !== 401) {
        setStatus(elements.loginStatus, error.message, "error");
      }
      account = null;
      csrfToken = "";
      setView("login");
      return false;
    }
  };

  const consumeEmailToken = async () => {
    if (!location.hash.startsWith("#email_token=")) return false;
    const token = decodeURIComponent(
      location.hash.slice("#email_token=".length)
    );
    history.replaceState(null, "", "/account/");
    try {
      await api("/email/verify", {
        method: "POST",
        body: JSON.stringify({ token }),
      });
      bootNotice = {
        message: "Email verified. It now opens this QSDM Account.",
        state: "success",
      };
      return true;
    } catch (error) {
      bootNotice = { message: error.message, state: "error" };
      return true;
    }
  };

  const configureLogin = () => {
    elements.telegram.hidden = !config.login.telegram;
    elements.emailForm.hidden = !config.login.email;
    if (!config.login.email && !config.login.telegram) {
      setStatus(
        elements.loginStatus,
        "QSDM Account sign-in is not configured on this server yet.",
        "error"
      );
    }
  };

  elements.emailForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    elements.emailSubmit.disabled = true;
    setStatus(elements.loginStatus, "Sending a one-time sign-in link...");
    try {
      const payload = await api("/email/start", {
        method: "POST",
        body: JSON.stringify({ email: elements.email.value }),
      });
      setStatus(elements.loginStatus, payload.message, "success");
    } catch (error) {
      setStatus(elements.loginStatus, error.message, "error");
    } finally {
      elements.emailSubmit.disabled = false;
    }
  });

  elements.telegram.addEventListener("click", () => {
    location.assign(API + "/telegram/start");
  });

  elements.addEmailIdentity.addEventListener("click", () => {
    emailIdentityFormOpen = !emailIdentityFormOpen;
    elements.emailIdentityForm.hidden = !emailIdentityFormOpen;
    if (emailIdentityFormOpen) elements.identityEmail.focus();
  });

  elements.emailIdentityForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    elements.identityEmailSubmit.disabled = true;
    setStatus(elements.identityMethodStatus, "Sending a verification link...");
    try {
      const payload = await api("/identities/email/start", {
        method: "POST",
        csrf: true,
        body: JSON.stringify({ email: elements.identityEmail.value }),
      });
      setStatus(elements.identityMethodStatus, payload.message, "success");
    } catch (error) {
      setStatus(elements.identityMethodStatus, error.message, "error");
    } finally {
      elements.identityEmailSubmit.disabled = false;
    }
  });

  elements.addTelegramIdentity.addEventListener("click", async () => {
    elements.addTelegramIdentity.disabled = true;
    setStatus(
      elements.identityMethodStatus,
      "Opening Telegram authorization..."
    );
    try {
      const payload = await api("/identities/telegram/start", {
        method: "POST",
        csrf: true,
      });
      const destination = new URL(payload.url, location.origin);
      if (
        destination.origin !== "https://oauth.telegram.org" &&
        destination.origin !== location.origin
      ) {
        throw new Error("The Telegram authorization destination was rejected.");
      }
      location.assign(destination.href);
    } catch (error) {
      setStatus(elements.identityMethodStatus, error.message, "error");
      elements.addTelegramIdentity.disabled = false;
    }
  });

  elements.signOut.addEventListener("click", async () => {
    elements.signOut.disabled = true;
    try {
      await api("/logout", { method: "POST", csrf: true });
    } catch {
      // A stale server session is equivalent to being signed out locally.
    }
    account = null;
    csrfToken = "";
    emailIdentityFormOpen = false;
    elements.signOut.disabled = false;
    setView("login");
  });

  elements.linkWallet.addEventListener("click", async () => {
    elements.linkWallet.disabled = true;
    setStatus(elements.dashboardStatus, "Requesting the active QSDM wallet...");
    try {
      if (!window.qsdm?.request) {
        throw new Error(
          "Install the QSDM Wallet extension and start QSDM Hive before linking a wallet."
        );
      }
      const accounts = await window.qsdm.request({
        method: "qsdm_requestAccounts",
      });
      const address = Array.isArray(accounts) ? accounts[0] : "";
      if (!/^[0-9a-f]{64}$/.test(address || "")) {
        throw new Error("QSDM Hive did not provide a valid active wallet.");
      }
      const challengePayload = await api("/wallets/challenge", {
        method: "POST",
        csrf: true,
        body: JSON.stringify({ address }),
      });
      setStatus(
        elements.dashboardStatus,
        "Approve the wallet ownership message in QSDM Hive."
      );
      const signed = await window.qsdm.request({
        method: "qsdm_signMessage",
        params: { message: challengePayload.challenge.message },
      });
      if (
        signed?.address !== address ||
        !signed.public_key ||
        !signed.signature
      ) {
        throw new Error("QSDM Hive returned an incomplete wallet signature.");
      }
      await api("/wallets/confirm", {
        method: "POST",
        csrf: true,
        body: JSON.stringify({
          challenge_id: challengePayload.challenge.id,
          address,
          public_key: signed.public_key,
          signature: signed.signature,
        }),
      });
      setStatus(
        elements.dashboardStatus,
        "Wallet linked successfully.",
        "success"
      );
      await loadSession();
    } catch (error) {
      setStatus(elements.dashboardStatus, error.message, "error");
    } finally {
      elements.linkWallet.disabled = false;
    }
  });

  elements.refreshWallets.addEventListener("click", async () => {
    elements.refreshWallets.disabled = true;
    await loadSession();
    elements.refreshWallets.disabled = false;
  });

  const boot = async () => {
    setView("loading");
    try {
      config = await api("/config");
    } catch (error) {
      setView("login");
      setStatus(
        elements.loginStatus,
        `QSDM Account is unavailable: ${error.message}`,
        "error"
      );
      return;
    }
    configureLogin();
    const verified = await consumeEmailToken();
    await loadSession();
    const search = new URLSearchParams(location.search);
    if (search.get("linked") === "telegram") {
      bootNotice = {
        message: "Telegram now opens this QSDM Account.",
        state: "success",
      };
    }
    if (!verified) {
      const requested = search.get("login");
      if (requested === "telegram" && config.login.telegram && !account) {
        location.assign(API + "/telegram/start");
      } else if (requested === "email" && config.login.email && !account) {
        elements.email.focus();
      }
    }
    const errorCode = search.get("error");
    if (errorCode) {
      const messages = {
        telegram_failed:
          "Telegram sign-in could not be verified. Please try again.",
        telegram_unavailable: "Telegram sign-in is temporarily unavailable.",
        session_unavailable: "The account session could not be created.",
        rate_limited: "Too many sign-in attempts. Please wait and try again.",
        identity_in_use:
          "That sign-in method belongs to another QSDM Account. No accounts were merged.",
        identity_already_set:
          "This QSDM Account already has that sign-in method.",
        identity_session_changed:
          "Return to the browser where you started Telegram linking and try again.",
      };
      bootNotice = {
        message: messages[errorCode] || "Sign-in did not complete.",
        state: "error",
      };
    }
    if (bootNotice) {
      setStatus(
        account ? elements.dashboardStatus : elements.loginStatus,
        bootNotice.message,
        bootNotice.state
      );
    }
    if (errorCode || search.get("linked")) {
      history.replaceState(null, "", "/account/");
    }
  };

  void boot();
})();
