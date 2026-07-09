"use strict";

let currentState = null;
let toastTimer = null;
let editingRole = "agent";

const byId = (id) => document.getElementById(id);

async function api(path, options = {}) {
  const request = { method: "GET", ...options, headers: { ...(options.headers || {}) } };
  if (request.body && typeof request.body !== "string") {
    request.headers["Content-Type"] = "application/json";
    request.body = JSON.stringify(request.body);
  }
  const response = await fetch(path, request);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `Request failed (${response.status})`);
  }
  return payload;
}

async function loadState(showErrors = true) {
  try {
    currentState = await api("/api/state");
    if (!document.activeElement || !document.activeElement.matches("input, textarea")) {
      editingRole = currentState.settings.role;
      populateForm(currentState);
    }
    renderState(currentState);
  } catch (error) {
    if (showErrors) showToast(error.message, true);
  }
}

function populateForm(state) {
  const { settings } = state;
  byId("worker-id").value = settings.agent.worker_id;
  byId("agent-cpu-enabled").checked = settings.agent.cpu;
  byId("agent-gpu-enabled").checked = settings.agent.gpu;
  byId("agent-ram-enabled").checked = settings.agent.ram;
  byId("agent-cpu-share").value = settings.agent.cpu_share;
  byId("agent-gpu-share").value = settings.agent.gpu_share;
  byId("agent-ram-share").value = settings.agent.ram_share;
  byId("relay-lan-enabled").checked = settings.relay.allow_lan;
  byId("relay-port").value = settings.relay.port;
  byId("relay-address").value = settings.relay.advertised_url;
  byId("relay-cpu-share").value = settings.relay.cpu_share;
  byId("relay-gpu-share").value = settings.relay.gpu_share;
  byId("relay-ram-share").value = settings.relay.ram_share;
  byId("auto-start").checked = settings.auto_start;
  updateSliderOutputs();
  updateLANFields();
}

function renderState(state) {
  const running = state.running;
  const role = editingRole;
  byId("version-label").textContent = `Version ${state.version}`;
  byId("global-status").textContent = running ? `${friendlyRole(state.active_role)} running` : "Stopped";
  byId("global-status").classList.toggle("running", running);
  byId("summary-role").textContent = friendlyRole(running ? state.active_role : role);
  byId("agent-panel").classList.toggle("hidden", role !== "agent");
  byId("relay-panel").classList.toggle("hidden", role !== "relay");
  document.querySelectorAll(".role-button").forEach((button) => {
    button.classList.toggle("active", button.dataset.role === role);
    button.disabled = running;
  });

  const paired = state.connections.agent_paired;
  byId("agent-paired-state").textContent = paired ? "Paired" : "Not paired";
  byId("agent-paired-state").classList.toggle("ready", paired);
  byId("agent-relay-address").textContent = state.settings.agent.relay_url || "Not connected";
  byId("relay-token-state").textContent = state.connections.relay_tokens_ready ? "Secure keys ready" : "Keys created when needed";
  byId("relay-token-state").classList.toggle("ready", state.connections.relay_tokens_ready);
  byId("mother-state").textContent = state.connections.mother_configured
    ? "QSDM Hive Mother role connected"
    : "QSDM Hive not connected";

  const gpuNames = state.system.gpus.map((gpu) => gpu.name).join(", ");
  byId("cpu-detail").textContent = `${state.system.cpu_threads} logical processors`;
  byId("ram-detail").textContent = `${formatNumber(state.system.total_ram_mib)} MiB installed`;
  byId("gpu-detail").textContent = state.system.gpu_ready ? gpuNames : state.system.gpu_message;
  byId("agent-system-note").textContent = state.system.hostname;
  byId("agent-gpu-enabled").disabled = running || !state.system.gpu_ready;
  byId("agent-gpu-share").disabled = running || !state.system.gpu_ready || !byId("agent-gpu-enabled").checked;
  byId("agent-gpu-row").classList.toggle("resource-unavailable", !state.system.gpu_ready);

  byId("summary-detail").textContent = summaryText(state, role);
  renderMetrics(state, role);
  renderWorkers(state.relay);
  renderActivity(state.activity);

  const errorNotice = byId("error-notice");
  errorNotice.textContent = state.last_error || "";
  errorNotice.classList.toggle("hidden", !state.last_error);

  document.querySelectorAll(".workspace input, .workspace textarea").forEach((element) => {
    if (element.id === "agent-gpu-enabled" || element.id === "agent-gpu-share") return;
    element.disabled = running;
  });
  byId("pair-agent-button").disabled = running;
  byId("save-button").disabled = running;
  byId("start-button").classList.toggle("hidden", running);
  byId("stop-button").classList.toggle("hidden", !running);
  byId("start-button").textContent = `Start ${friendlyRole(role)}`;
  byId("auto-start").disabled = running;
}

function renderMetrics(state, role) {
  const container = byId("summary-metrics");
  container.replaceChildren();
  const metrics = [];
  if (role === "relay") {
    const relay = state.relay;
    metrics.push(["Agents", relay ? relay.workers.length : 0]);
    metrics.push(["Active jobs", relay ? relay.active_leases : 0]);
    const receipts = relay ? Object.values(relay.receipt_counts || {}).reduce((sum, value) => sum + value, 0) : 0;
    metrics.push(["Receipts", receipts]);
  } else {
    metrics.push(["CPU", `${state.settings.agent.cpu_share}%`]);
    metrics.push(["Memory", `${state.settings.agent.ram_share}%`]);
    metrics.push(["NVIDIA", state.settings.agent.gpu && state.system.gpu_ready ? `${state.settings.agent.gpu_share}%` : "Off"]);
  }
  metrics.forEach(([label, value]) => {
    const metric = document.createElement("div");
    metric.className = "metric";
    const labelElement = document.createElement("span");
    labelElement.textContent = label;
    const valueElement = document.createElement("strong");
    valueElement.textContent = String(value);
    metric.append(labelElement, valueElement);
    container.append(metric);
  });
}

function renderWorkers(relay) {
  const body = byId("workers-body");
  body.replaceChildren();
  const workers = relay?.workers || [];
  byId("worker-count").textContent = `${workers.length} ${workers.length === 1 ? "Agent" : "Agents"}`;
  if (!workers.length) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 4;
    cell.className = "empty-row";
    cell.textContent = "No Agents connected";
    row.append(cell);
    body.append(row);
    return;
  }
  workers.forEach((worker) => {
    const row = document.createElement("tr");
    const values = [
      worker.hostname || worker.worker_id,
      (worker.capabilities.resources || []).map((resource) => resource.toUpperCase()).join(", "),
      formatNumber(worker.completed_jobs),
      relativeTime(worker.last_seen_at),
    ];
    values.forEach((value) => {
      const cell = document.createElement("td");
      cell.textContent = value;
      row.append(cell);
    });
    body.append(row);
  });
}

function renderActivity(entries) {
  const log = byId("activity-log");
  const visible = (entries || []).slice(-40);
  log.textContent = visible.length ? visible.join("\n") : "No activity yet.";
  log.scrollTop = log.scrollHeight;
}

function summaryText(state, role) {
  if (state.running) {
    if (state.active_role === "relay") {
      const count = state.relay?.workers.length || 0;
      return `${count} trusted ${count === 1 ? "computer" : "computers"} connected`;
    }
    return state.connections.agent_paired ? "Sharing selected resources" : "Waiting for Relay pairing";
  }
  if (role === "relay") return "Ready to coordinate trusted computers";
  return state.connections.agent_paired ? "Ready to share selected resources" : "Waiting for a Relay pairing code";
}

function collectSettings() {
  const settings = structuredClone(currentState.settings);
  settings.schema_version = 1;
  settings.role = editingRole;
  settings.auto_start = byId("auto-start").checked;
  settings.agent.worker_id = byId("worker-id").value.trim();
  settings.agent.cpu = byId("agent-cpu-enabled").checked;
  settings.agent.gpu = byId("agent-gpu-enabled").checked;
  settings.agent.ram = byId("agent-ram-enabled").checked;
  settings.agent.cpu_share = Number(byId("agent-cpu-share").value);
  settings.agent.gpu_share = Number(byId("agent-gpu-share").value);
  settings.agent.ram_share = Number(byId("agent-ram-share").value);
  settings.relay.allow_lan = byId("relay-lan-enabled").checked;
  settings.relay.port = Number(byId("relay-port").value);
  settings.relay.advertised_url = byId("relay-address").value.trim();
  settings.relay.cpu_share = Number(byId("relay-cpu-share").value);
  settings.relay.gpu_share = Number(byId("relay-gpu-share").value);
  settings.relay.ram_share = Number(byId("relay-ram-share").value);
  return settings;
}

async function saveSettings(showSuccess = true) {
  const settings = collectSettings();
  await api("/api/settings", { method: "PUT", body: settings });
  currentState.settings = settings;
  if (showSuccess) showToast("Settings saved");
  await loadState(false);
}

async function pairAgent() {
  const code = byId("agent-code").value.trim();
  if (!code) {
    showToast("Paste the pairing code from your Relay", true);
    return;
  }
  try {
    await api("/api/pair-agent", { method: "POST", body: { code } });
    byId("agent-code").value = "";
    showToast("Agent connected to Relay");
    await loadState(false);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function startControl() {
  try {
    await saveSettings(false);
    await api("/api/start", { method: "POST" });
    showToast(`${friendlyRole(editingRole)} started`);
    await loadState(false);
  } catch (error) {
    showToast(error.message, true);
    await loadState(false);
  }
}

async function stopControl() {
  try {
    await api("/api/stop", { method: "POST" });
    showToast("Edge Control stopped");
    await loadState(false);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function showPairingCode() {
  try {
    await saveSettings(false);
    const codes = await api("/api/pairing-codes");
    byId("pairing-code-output").value = codes.agent_code;
    byId("mother-code-output").value = codes.mother_code;
    byId("federation-code-output").value = codes.federation_code || "Set the Relay address to an HTTPS URL before creating an internet federation invitation.";
    byId("pairing-dialog").showModal();
  } catch (error) {
    showToast(error.message, true);
  }
}

async function connectMother() {
  try {
    await saveSettings(false);
    await api("/api/connect-mother", { method: "POST" });
    showToast("This QSDM Hive is now connected as Mother Hive");
    await loadState(false);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function copyText(value, successMessage) {
  try {
    await navigator.clipboard.writeText(value);
  } catch (_) {
    const temporary = document.createElement("textarea");
    temporary.value = value;
    document.body.append(temporary);
    temporary.select();
    document.execCommand("copy");
    temporary.remove();
  }
  showToast(successMessage);
}

function updateSliderOutputs() {
  [
    ["agent-cpu-share", "agent-cpu-output"],
    ["agent-gpu-share", "agent-gpu-output"],
    ["agent-ram-share", "agent-ram-output"],
    ["relay-cpu-share", "relay-cpu-output"],
    ["relay-gpu-share", "relay-gpu-output"],
    ["relay-ram-share", "relay-ram-output"],
  ].forEach(([input, output]) => {
    byId(output).textContent = `${byId(input).value}%`;
  });
  byId("agent-cpu-share").disabled = currentState?.running || !byId("agent-cpu-enabled").checked;
  byId("agent-ram-share").disabled = currentState?.running || !byId("agent-ram-enabled").checked;
  byId("agent-gpu-share").disabled = currentState?.running || !currentState?.system.gpu_ready || !byId("agent-gpu-enabled").checked;
}

function updateLANFields() {
  const enabled = byId("relay-lan-enabled").checked;
  byId("relay-address").disabled = Boolean(currentState?.running) || !enabled;
}

function setRole(role) {
  if (currentState?.running) return;
  editingRole = role;
  currentState.settings.role = role;
  renderState(currentState);
}

function showToast(message, isError = false) {
  const toast = byId("toast");
  toast.textContent = message;
  toast.classList.remove("hidden");
  toast.classList.toggle("error", isError);
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.add("hidden"), 4200);
}

function friendlyRole(role) {
  return role === "relay" ? "Relay" : "Agent";
}

function formatNumber(value) {
  return Number(value || 0).toLocaleString();
}

function relativeTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  const seconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  return `${Math.round(minutes / 60)}h ago`;
}

document.querySelectorAll(".role-button").forEach((button) => {
  button.addEventListener("click", () => setRole(button.dataset.role));
});
document.querySelectorAll('input[type="range"]').forEach((input) => input.addEventListener("input", updateSliderOutputs));
["agent-cpu-enabled", "agent-ram-enabled", "agent-gpu-enabled"].forEach((id) => byId(id).addEventListener("change", updateSliderOutputs));
byId("relay-lan-enabled").addEventListener("change", updateLANFields);
byId("save-button").addEventListener("click", () => saveSettings().catch((error) => showToast(error.message, true)));
byId("start-button").addEventListener("click", startControl);
byId("stop-button").addEventListener("click", stopControl);
byId("pair-agent-button").addEventListener("click", pairAgent);
byId("show-pairing-button").addEventListener("click", showPairingCode);
byId("connect-mother-button").addEventListener("click", connectMother);
byId("copy-pairing-button").addEventListener("click", () => copyText(byId("pairing-code-output").value, "Agent pairing code copied"));
byId("copy-mother-button").addEventListener("click", () => copyText(byId("mother-code-output").value, "QSDM Hive pairing code copied"));
byId("copy-federation-button").addEventListener("click", () => {
  const value = byId("federation-code-output").value;
  if (!value.startsWith("QSDM-EDGE-1.")) {
    showToast("Configure an HTTPS Relay address before copying a federation invitation", true);
    return;
  }
  copyText(value, "Federation invitation copied");
});
byId("refresh-button").addEventListener("click", () => loadState());
byId("quit-button").addEventListener("click", async () => {
  if (!window.confirm("Quit QSDM Edge Control? Running Agent or Relay work will stop.")) return;
  await api("/api/quit", { method: "POST" }).catch(() => {});
  document.body.textContent = "QSDM Edge Control has closed.";
});
byId("pairing-dialog").addEventListener("close", () => {
  byId("pairing-code-output").value = "";
  byId("mother-code-output").value = "";
  byId("federation-code-output").value = "";
});
byId("relay-port").addEventListener("change", () => {
  try {
    const address = new URL(byId("relay-address").value);
    address.port = byId("relay-port").value;
    byId("relay-address").value = address.toString().replace(/\/$/, "");
  } catch (_) {}
});

loadState();
setInterval(() => loadState(false), 2500);
