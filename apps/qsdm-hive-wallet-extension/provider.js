(() => {
  "use strict";

  if (window.qsdm?.isQsdmHive) return;

  const REQUEST_SOURCE = "qsdm-hive-provider-request";
  const RESPONSE_SOURCE = "qsdm-hive-provider-response";
  const pending = new Map();
  const listeners = new Map();

  const emit = (event, value) => {
    const callbacks = listeners.get(event) || [];
    callbacks.forEach((callback) => callback(value));
  };

  window.addEventListener("message", (event) => {
    if (
      event.source !== window ||
      event.origin !== window.location.origin ||
      event.data?.source !== RESPONSE_SOURCE ||
      typeof event.data?.id !== "string"
    ) {
      return;
    }
    const request = pending.get(event.data.id);
    if (!request) return;
    pending.delete(event.data.id);
    clearTimeout(request.timeout);
    if (event.data.ok) {
      if (request.method === "qsdm_requestAccounts") {
        emit("accountsChanged", event.data.result);
      } else if (request.method === "qsdm_disconnect") {
        emit("accountsChanged", []);
      }
      request.resolve(event.data.result);
    } else {
      request.reject(
        new Error(event.data.error || "QSDM wallet request failed")
      );
    }
  });

  const provider = Object.freeze({
    isQsdmHive: true,
    version: "qsdm-provider/v1",
    request({ method, params } = {}) {
      if (typeof method !== "string" || !method.startsWith("qsdm_")) {
        return Promise.reject(
          new Error("A valid QSDM provider method is required")
        );
      }
      const id = crypto.randomUUID();
      return new Promise((resolve, reject) => {
        const timeout = setTimeout(() => {
          pending.delete(id);
          reject(new Error("QSDM Hive did not answer the wallet request"));
        }, 125000);
        pending.set(id, { resolve, reject, timeout, method });
        window.postMessage(
          { source: REQUEST_SOURCE, id, method, params },
          window.location.origin
        );
      });
    },
    on(event, callback) {
      if (typeof callback !== "function") return provider;
      listeners.set(event, [...(listeners.get(event) || []), callback]);
      return provider;
    },
    removeListener(event, callback) {
      listeners.set(
        event,
        (listeners.get(event) || []).filter((entry) => entry !== callback)
      );
      return provider;
    },
  });

  Object.defineProperty(window, "qsdm", {
    value: provider,
    configurable: false,
    enumerable: false,
    writable: false,
  });
  window.dispatchEvent(new Event("qsdm#initialized"));
})();
