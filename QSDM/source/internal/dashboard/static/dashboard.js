// Dashboard JavaScript
let updateInterval;

const dashFetchOpts = { credentials: 'include' };

// Cleared when the main dashboard HTML/JS loads; if login bounced us back first, login.js shows a message.
try {
    sessionStorage.removeItem('qsdm_expect_dashboard');
} catch (e) {}

function formatNumber(num) {
    if (num >= 1000000) {
        return (num / 1000000).toFixed(2) + 'M';
    }
    if (num >= 1000) {
        return (num / 1000).toFixed(2) + 'K';
    }
    return num.toString();
}

function formatDuration(seconds) {
    if (seconds < 60) {
        return Math.floor(seconds) + 's';
    }
    if (seconds < 3600) {
        return Math.floor(seconds / 60) + 'm';
    }
    if (seconds < 86400) {
        return Math.floor(seconds / 3600) + 'h';
    }
    return Math.floor(seconds / 86400) + 'd';
}

function updateMetrics() {
    fetch('/api/metrics', dashFetchOpts)
        .then(response => response.json())
        .then(data => {
            // Transaction metrics
            document.getElementById('tx-processed').textContent = formatNumber(data.transactions_processed || 0);
            document.getElementById('tx-valid').textContent = formatNumber(data.transactions_valid || 0);
            document.getElementById('tx-invalid').textContent = formatNumber(data.transactions_invalid || 0);
            document.getElementById('tx-stored').textContent = formatNumber(data.transactions_stored || 0);
            
            const validityRate = (data.validity_rate_percent || 0).toFixed(1);
            document.getElementById('validity-rate').textContent = validityRate + '%';

            // Network metrics
            document.getElementById('msg-sent').textContent = formatNumber(data.network_messages_sent || 0);
            document.getElementById('msg-recv').textContent = formatNumber(data.network_messages_received || 0);

            // Governance metrics
            document.getElementById('proposals').textContent = formatNumber(data.proposals_created || 0);
            document.getElementById('votes').textContent = formatNumber(data.votes_cast || 0);

            // System metrics
            document.getElementById('uptime').textContent = formatDuration(data.uptime_seconds || 0);
            document.getElementById('quarantines').textContent = formatNumber(data.quarantines_triggered || 0);
            document.getElementById('reputation').textContent = formatNumber(data.reputation_updates || 0);

            // Last updated
            document.getElementById('last-updated').textContent = 
                'Last updated: ' + new Date().toLocaleTimeString();
        })
        .catch(error => {
            console.error('Error fetching metrics:', error);
        });
}

function updateNGCProofs() {
    fetch('/api/ngc-proofs', dashFetchOpts)
        .then(response => response.json())
        .then(data => {
            const statusEl = document.getElementById('ngc-status');
            const table = document.getElementById('ngc-table');
            const tbody = document.getElementById('ngc-tbody');
            if (!statusEl || !tbody || !table) return;

            const configured = data.ingest_configured === true;
            const count = data.count || 0;
            if (!configured) {
                statusEl.innerHTML = '<strong>Ingest disabled.</strong> Set <code>QSDM_NGC_INGEST_SECRET</code> on the node and push from <code>apps/qsdm-nvidia-ngc</code> (see README).';
            } else if (count === 0) {
                statusEl.innerHTML = '<strong>Ingest enabled.</strong> No proof bundles yet — run the NGC validator with <code>QSDM_NGC_REPORT_URL</code> + matching secret.';
            } else {
                statusEl.innerHTML = `<strong>Ingest enabled.</strong> ${count} bundle(s) in ring buffer.`;
            }

            const lockEl = document.getElementById('nvidia-lock-ngc-summary');
            const nl = data.nvidia_lock;
            if (lockEl) {
                if (!nl || nl.enabled !== true) {
                    const pi = data.ngc_proof_ingest || {};
                    const piOk = pi.accepted_total != null ? String(pi.accepted_total) : '0';
                    const piBad = pi.rejected_total != null ? String(pi.rejected_total) : '0';
                    lockEl.innerHTML = `<span class="metric-label">NVIDIA-lock:</span> off (API node). · NGC POST ok: <code>${piOk}</code> / reject: <code>${piBad}</code>`;
                } else {
                    const ok = nl.proof_ok === true;
                    const okLabel = ok ? '<span style="color:#4caf50;">proof OK</span>' : '<span style="color:#ff9800;">proof not OK</span>';
                    const bind = nl.node_id_binding_enabled ? 'node ID binding on' : 'no node ID binding';
                    const hm = nl.hmac_required ? 'HMAC required' : 'HMAC off';
                    const ch = nl.ingest_nonce_required ? 'ingest nonce on' : 'ingest nonce off';
                    const blocks = nl.http_blocks_total != null ? String(nl.http_blocks_total) : '0';
                    const chIss = nl.ngc_challenge_issued_total != null ? String(nl.ngc_challenge_issued_total) : '0';
                    const ch429 = nl.ngc_challenge_rate_limited_total != null ? String(nl.ngc_challenge_rate_limited_total) : '0';
                    const pool = nl.ngc_ingest_nonce_pool_size != null ? String(nl.ngc_ingest_nonce_pool_size) : '0';
                    const p2pOn = nl.p2p_gate_enabled === true ? 'P2P gate on' : 'P2P gate off';
                    const p2pN = nl.p2p_rejects_total != null ? String(nl.p2p_rejects_total) : '0';
                    const pi = data.ngc_proof_ingest || {};
                    const piOk = pi.accepted_total != null ? String(pi.accepted_total) : '0';
                    const piBad = pi.rejected_total != null ? String(pi.rejected_total) : '0';
                    lockEl.innerHTML = `<strong>NVIDIA-lock</strong> on — ${okLabel} · ${bind} · ${hm} · ${ch} · ${p2pOn} (drops: <code>${p2pN}</code>) · API 403s: <code>${blocks}</code> · challenges: <code>${chIss}</code> (429: <code>${ch429}</code>) · nonce pool: <code>${pool}</code> · NGC POST ok: <code>${piOk}</code> / reject: <code>${piBad}</code>`;
                }
            }

            const proofs = data.proofs || [];
            tbody.innerHTML = '';
            if (proofs.length === 0) {
                table.style.display = 'none';
                return;
            }
            table.style.display = 'table';
            proofs.slice().reverse().forEach((p) => {
                const tr = document.createElement('tr');
                const recv = p.received_at || '—';
                const cuda = p.cuda_proof_hash || '—';
                const ai = p.ai_computation_hash || '—';
                const ten = p.tensor_operation_proof || '—';
                const ex = p.execution_seconds != null ? String(p.execution_seconds) : '—';
                tr.innerHTML = `
                    <td>${recv}</td>
                    <td class="ngc-hash" title="${cuda}">${cuda}</td>
                    <td class="ngc-hash" title="${ai}">${ai}</td>
                    <td class="ngc-hash" title="${ten}">${ten}</td>
                    <td>${ex}</td>
                `;
                tbody.appendChild(tr);
            });
        })
        .catch(() => {
            const statusEl = document.getElementById('ngc-status');
            if (statusEl) {
                statusEl.textContent = 'Could not load NGC proofs (check dashboard auth / network).';
            }
            const lockEl = document.getElementById('nvidia-lock-ngc-summary');
            if (lockEl) {
                lockEl.textContent = '';
            }
        });
}

// attestRejectionsState backs the triage control bar above the
// rejection tile. Shape:
//
//   kind      : closed-enum filter ("" = no filter; values
//               match api.IsKnownRecentRejectionKind).
//   windowSec : rolling time-window in seconds (0 = since boot).
//               The absolute since-timestamp is computed from
//               Date.now() on every fetch so the window rolls
//               forward as time passes — matches operator
//               intuition ("last 1h" = last 1h from now,
//               recomputed each tick).
//   paused    : when true, the recurring polling loop skips
//               the tile so an operator reading the table is
//               not page-rolled out from under by the next tick.
//   lastRecords : last successfully-fetched records; used for
//                 the CSV-export href without re-fetching.
const attestRejectionsState = {
    kind: '',
    windowSec: 0,
    paused: false,
    lastRecords: [],
};

// csvEscape wraps a single cell value for safe CSV emission.
// Doubles internal quotes (the CSV escape rule) and wraps in
// quotes whenever the cell contains comma / quote / newline /
// CR. textContent-source data only — never trusted-string-as-
// CSV, which would let a hostile miner_addr inject formula
// payloads (=cmd|...) when the file opens in Excel. We
// defend against that here too: a cell starting with =, +, -,
// @, or tab gets a leading apostrophe so spreadsheet apps
// treat it as a literal string, not a formula.
function csvEscape(v) {
    if (v == null) return '';
    let s = String(v);
    // Excel/LibreOffice formula-injection defence. The leading
    // apostrophe is the standard CSV-as-spreadsheet escape and
    // is dropped on display by both apps.
    if (s.length > 0 && '=+-@\t\r'.indexOf(s[0]) !== -1) {
        s = "'" + s;
    }
    if (s.indexOf(',') !== -1 || s.indexOf('"') !== -1 ||
        s.indexOf('\n') !== -1 || s.indexOf('\r') !== -1) {
        s = '"' + s.replace(/"/g, '""') + '"';
    }
    return s;
}

// buildAttestRejectionsCSV serialises the records array into a
// CSV blob for the export link. Header row matches the table's
// rendered columns plus a few fields the table truncates
// (full miner_addr, gpu_name, cert_subject, detail) so
// operators have the full record without an extra API roundtrip.
function buildAttestRejectionsCSV(records) {
    const header = [
        'seq', 'recorded_at', 'kind', 'reason', 'arch',
        'height', 'miner_addr', 'gpu_name', 'cert_subject',
        'detail',
    ];
    const lines = [header.join(',')];
    for (const r of records) {
        lines.push([
            r.seq != null ? r.seq : '',
            r.recorded_at || '',
            r.kind || '',
            r.reason || '',
            r.arch || '',
            r.height != null ? r.height : '',
            r.miner_addr || '',
            r.gpu_name || '',
            r.cert_subject || '',
            r.detail || '',
        ].map(csvEscape).join(','));
    }
    return lines.join('\n');
}

// updateAttestRejectionsExport refreshes the CSV-export link's
// data: URL from the current lastRecords. Called after every
// successful fetch so the link is always one click away from
// the freshest snapshot.
function updateAttestRejectionsExport() {
    const link = document.getElementById('attest-rejections-export');
    if (!link) return;
    const csv = buildAttestRejectionsCSV(attestRejectionsState.lastRecords);
    // encodeURIComponent on the body keeps newlines / commas /
    // quotes intact in the data: payload — the alternative
    // (Blob URL) would require a click handler and revoke
    // dance; data: URLs are zero-overhead and inert.
    link.href = 'data:text/csv;charset=utf-8,' + encodeURIComponent(csv);
}

// renderAttestRejectionsTopMiners populates the top-3
// offenders strip from the current page. Aggregates by
// miner_addr (records with no miner are skipped; a hostile
// miner that omits the address still shows up in the table
// itself, just not in this strip). Hidden when no record has
// a populated miner_addr.
function renderAttestRejectionsTopMiners(records) {
    const wrap = document.getElementById('attest-rejections-top-miners');
    const list = document.getElementById('attest-rejections-top-miners-list');
    if (!wrap || !list) return;

    const counts = new Map();
    for (const r of records) {
        const m = r.miner_addr || '';
        if (!m) continue;
        counts.set(m, (counts.get(m) || 0) + 1);
    }
    if (counts.size === 0) {
        wrap.style.display = 'none';
        return;
    }
    const ranked = Array.from(counts.entries())
        .sort((a, b) => b[1] - a[1])
        .slice(0, 3);
    list.innerHTML = '';
    for (const [miner, count] of ranked) {
        const li = document.createElement('li');
        const minerSpan = document.createElement('span');
        const short = miner.length > 24 ? miner.slice(0, 24) + '…' : miner;
        minerSpan.textContent = short;
        minerSpan.title = miner;
        const countSpan = document.createElement('span');
        countSpan.style.color = '#f5a623';
        countSpan.textContent = ' — ' + count + ' rejection' + (count === 1 ? '' : 's');
        li.appendChild(minerSpan);
        li.appendChild(countSpan);
        list.appendChild(li);
    }
    wrap.style.display = 'block';
}

// initAttestRejectionsControls hooks the kind/window/pause/CSV
// controls to attestRejectionsState. Idempotent — safe to
// call multiple times if the DOM is rebuilt for any reason.
// Called once from startUpdates(); the controls fire one
// updateAttestRejections() on change so operators see the
// filtered tile immediately, not on the next 2 s poll.
function initAttestRejectionsControls() {
    const kindSel = document.getElementById('attest-rejections-filter-kind');
    const winSel = document.getElementById('attest-rejections-filter-window');
    const pauseBtn = document.getElementById('attest-rejections-pause');
    if (kindSel && !kindSel.dataset.wired) {
        kindSel.dataset.wired = '1';
        kindSel.addEventListener('change', () => {
            attestRejectionsState.kind = kindSel.value || '';
            updateAttestRejections();
        });
    }
    if (winSel && !winSel.dataset.wired) {
        winSel.dataset.wired = '1';
        winSel.addEventListener('change', () => {
            const sec = parseInt(winSel.value, 10);
            attestRejectionsState.windowSec =
                (isNaN(sec) || sec <= 0) ? 0 : sec;
            updateAttestRejections();
        });
    }
    if (pauseBtn && !pauseBtn.dataset.wired) {
        pauseBtn.dataset.wired = '1';
        pauseBtn.addEventListener('click', () => {
            attestRejectionsState.paused = !attestRejectionsState.paused;
            if (attestRejectionsState.paused) {
                pauseBtn.textContent = '▶ resume polling';
                pauseBtn.style.background = '#3a2a1a';
                pauseBtn.style.color = '#f5a623';
            } else {
                pauseBtn.textContent = '⏸ pause polling';
                pauseBtn.style.background = '#1a3a5a';
                pauseBtn.style.color = '#7bd3ff';
                // Resume → fetch immediately so the operator
                // sees fresh data without waiting for the
                // next 2 s tick.
                updateAttestRejections();
            }
        });
    }
}

// updateAttestRejections renders the recent-attestation-rejection tile.
// Wire shape comes from internal/dashboard/attest_rejections.go and
// pkg/api.RecentRejectionView. Records arrive newest-first (server
// reverses the lister's ascending-Seq output before encoding).
//
// SECURITY: kind/reason/arch/recorded_at are server-derived (closed
// allowlists or RFC 3339 timestamps) and safe to render as text. The
// miner address comes from rejected miners, so we always populate the
// rendered cells via textContent rather than innerHTML to avoid an XSS
// vector if a malicious miner ever submits HTML in their address bytes.
function updateAttestRejections() {
    const params = new URLSearchParams({ limit: '50' });
    if (attestRejectionsState.kind) {
        params.set('kind', attestRejectionsState.kind);
    }
    if (attestRejectionsState.windowSec > 0) {
        // Rolling-from-now: recomputed every tick so the
        // window slides forward in lockstep with the clock.
        const since = Math.floor(Date.now() / 1000)
            - attestRejectionsState.windowSec;
        params.set('since', String(since));
    }
    fetch('/api/attest/rejections?' + params.toString(), dashFetchOpts)
        .then(response => response.json())
        .then(data => {
            const statusEl = document.getElementById('attest-rejections-status');
            const countersEl = document.getElementById('attest-rejections-counters');
            const table = document.getElementById('attest-rejections-table');
            const tbody = document.getElementById('attest-rejections-tbody');
            if (!statusEl || !countersEl || !table || !tbody) return;

            const records = data.records || [];
            if (data.available !== true) {
                statusEl.innerHTML = '<strong>Store not wired.</strong> v1-only deployment, or v2 store disabled at boot. Counter and history rows below remain empty until <code>v2wiring.Wire</code> attaches the recent-rejections store.';
            } else {
                const total = data.total_matches || 0;
                statusEl.textContent = 'Store wired. ' + total + ' rejection(s) tracked · showing ' + records.length + ' newest.';
            }

            // Counter grid: per-field observed/truncated/max + persist errors.
            const metrics = data.metrics || { fields: [], persist_errors_total: 0 };
            countersEl.innerHTML = '';
            (metrics.fields || []).forEach(row => {
                const obs = row.observed_total || 0;
                const trunc = row.truncated_total || 0;
                const max = row.runes_max || 0;
                const truncRate = obs > 0 ? ((trunc / obs) * 100).toFixed(1) + '%' : '0.0%';
                const truncColor = trunc > 0 ? '#f5a623' : '#7ed321';

                const cell = document.createElement('div');
                cell.style.cssText = 'background:#0f1419;border:1px solid #2a3441;border-radius:4px;padding:10px;';

                const head = document.createElement('div');
                head.style.cssText = 'display:flex;justify-content:space-between;align-items:baseline;';
                const label = document.createElement('span');
                label.className = 'metric-label';
                label.style.fontFamily = 'ui-monospace, monospace';
                label.textContent = row.field || '?';
                const value = document.createElement('span');
                value.className = 'metric-value';
                value.style.fontSize = '14px';
                value.textContent = obs + ' obs';
                head.appendChild(label);
                head.appendChild(value);

                const detail = document.createElement('div');
                detail.style.cssText = 'font-size:11px;color:#a0a0a0;margin-top:6px;';
                const truncSpan = document.createElement('span');
                truncSpan.style.color = truncColor;
                truncSpan.textContent = String(trunc);
                detail.append('truncated: ', truncSpan, ' (' + truncRate + ') · max ' + max + ' runes');

                cell.appendChild(head);
                cell.appendChild(detail);
                countersEl.appendChild(cell);
            });

            // Persistence-lifecycle counters: errors / compactions / on-disk size.
            // Local helper to mint one cell with the same visual shape as the
            // per-field grid above.
            const buildPersistCell = (label, valueText, valueColor, detailText) => {
                const cell = document.createElement('div');
                cell.style.cssText = 'background:#0f1419;border:1px solid #2a3441;border-radius:4px;padding:10px;';
                const head = document.createElement('div');
                head.style.cssText = 'display:flex;justify-content:space-between;align-items:baseline;';
                const labelEl = document.createElement('span');
                labelEl.className = 'metric-label';
                labelEl.textContent = label;
                const valueEl = document.createElement('span');
                valueEl.className = 'metric-value';
                valueEl.style.cssText = 'font-size:14px;color:' + valueColor + ';';
                valueEl.textContent = valueText;
                head.appendChild(labelEl);
                head.appendChild(valueEl);
                const detail = document.createElement('div');
                detail.style.cssText = 'font-size:11px;color:#a0a0a0;margin-top:6px;';
                detail.textContent = detailText;
                cell.appendChild(head);
                cell.appendChild(detail);
                return cell;
            };

            const persistErrs = metrics.persist_errors_total || 0;
            countersEl.appendChild(buildPersistCell(
                'persist errors',
                String(persistErrs),
                persistErrs > 0 ? '#d0021b' : '#7ed321',
                'on-disk Append() failures (in-memory ring unaffected)'
            ));

            const compactions = metrics.persist_compactions_total || 0;
            countersEl.appendChild(buildPersistCell(
                'compactions',
                String(compactions),
                '#7bd3ff',
                'soft-cap rewrites since boot (sustained rate ⇒ ring flooding)'
            ));

            const recordsOnDisk = metrics.persist_records_on_disk || 0;
            countersEl.appendChild(buildPersistCell(
                'records on disk',
                String(recordsOnDisk),
                '#e6e6e6',
                'live JSONL record count (approximate, ±softCap during reads)'
            ));

            // Stash for the CSV-export link + the top-miners
            // strip; both are derived from the current page so
            // a click is always one render away from fresh data.
            attestRejectionsState.lastRecords = records;
            updateAttestRejectionsExport();
            renderAttestRejectionsTopMiners(records);

            // Records table.
            tbody.innerHTML = '';
            if (records.length === 0) {
                table.style.display = 'none';
                return;
            }
            table.style.display = 'table';
            records.forEach(rec => {
                const tr = document.createElement('tr');
                const minerFull = rec.miner_addr || '';
                const minerShort = minerFull
                    ? (minerFull.length > 18 ? minerFull.slice(0, 18) + '…' : minerFull)
                    : '—';
                const cells = [
                    { text: rec.recorded_at || '—' },
                    { text: rec.kind || '—' },
                    { text: rec.reason || rec.arch || rec.detail || '—' },
                    { text: minerShort, title: minerFull, cls: 'ngc-hash' },
                    { text: rec.height != null ? String(rec.height) : '—' },
                ];
                cells.forEach(c => {
                    const td = document.createElement('td');
                    td.textContent = c.text;
                    if (c.cls) td.className = c.cls;
                    if (c.title) td.title = c.title;
                    tr.appendChild(td);
                });
                tbody.appendChild(tr);
            });
        })
        .catch(() => {
            const statusEl = document.getElementById('attest-rejections-status');
            if (statusEl) {
                statusEl.textContent = 'Could not load attestation rejections (check dashboard auth / network).';
            }
        });
}

function updateHealth() {
    fetch('/api/health', dashFetchOpts)
        .then(response => response.json())
        .then(data => {
            // Overall status
            const statusBadge = document.getElementById('overall-status');
            const overallStatus = data.overall_status || 'unknown';
            statusBadge.className = 'status-badge status-' + overallStatus;
            statusBadge.textContent = overallStatus.toUpperCase();

            // Component health
            const componentsDiv = document.getElementById('components');
            componentsDiv.innerHTML = '';

            if (data.components) {
                for (const [name, component] of Object.entries(data.components)) {
                    const div = document.createElement('div');
                    div.className = 'component-status';
                    
                    const statusClass = 'status-' + component.status;
                    div.innerHTML = `
                        <span class="component-name">${name}</span>
                        <span class="status-badge ${statusClass}">${component.status}</span>
                    `;
                    componentsDiv.appendChild(div);
                }
            }

            // Errors
            const errorsDiv = document.getElementById('errors');
            if (data.metrics && data.metrics.last_error) {
                const errorTime = data.metrics.last_error_time ? 
                    new Date(data.metrics.last_error_time).toLocaleString() : 'Unknown';
                errorsDiv.innerHTML = `
                    <div class="error-box">
                        <h3>Last Error (${errorTime})</h3>
                        <div class="error-message">${data.metrics.last_error}</div>
                    </div>
                `;
            } else {
                errorsDiv.innerHTML = `
                    <div style="color: #666; text-align: center; padding: 20px;">
                        No errors recorded
                    </div>
                `;
            }
        })
        .catch(error => {
            console.error('Error fetching health:', error);
        });
}

let topologyCanvas, topologyCtx;
let topologyData = null;

function initTopologyCanvas() {
    topologyCanvas = document.getElementById('topology-canvas');
    if (!topologyCanvas) return;
    
    topologyCtx = topologyCanvas.getContext('2d');
    const container = document.getElementById('topology-container');
    
    function resizeCanvas() {
        topologyCanvas.width = container.clientWidth;
        topologyCanvas.height = container.clientHeight;
        if (topologyData) {
            renderTopology(topologyData);
        }
    }
    
    resizeCanvas();
    window.addEventListener('resize', resizeCanvas);
}

function updateTopology() {
    fetch('/api/topology', dashFetchOpts)
        .then(response => response.json())
        .then(data => {
            if (data.error) {
                document.getElementById('topology-loading').style.display = 'none';
                document.getElementById('topology-error').style.display = 'block';
                document.getElementById('topology-error').textContent = data.error;
                return;
            }
            
            document.getElementById('topology-loading').style.display = 'none';
            document.getElementById('topology-error').style.display = 'none';
            
            document.getElementById('topology-peer-count').textContent = data.peerCount || 0;
            document.getElementById('topology-connected-count').textContent = data.connectedCount || 0;
            
            topologyData = data;
            renderTopology(data);
        })
        .catch(error => {
            console.error('Error fetching topology:', error);
            document.getElementById('topology-loading').style.display = 'none';
            document.getElementById('topology-error').style.display = 'block';
            document.getElementById('topology-error').textContent = 'Failed to load topology: ' + error.message;
        });
}

function renderTopology(data) {
    if (!topologyCanvas || !topologyCtx || !data.nodes || !data.edges) return;
    
    const width = topologyCanvas.width;
    const height = topologyCanvas.height;
    
    // Clear canvas
    topologyCtx.clearRect(0, 0, width, height);
    
    // Find self node
    const selfNode = data.nodes.find(n => n.type === 'self');
    if (!selfNode) return;
    
    // Position self in center
    const centerX = width / 2;
    const centerY = height / 2;
    const nodePositions = {};
    nodePositions[selfNode.id] = { x: centerX, y: centerY };
    
    // Position peer nodes in a circle around self
    const peerNodes = data.nodes.filter(n => n.type !== 'self');
    const radius = Math.min(width, height) * 0.3;
    const angleStep = (2 * Math.PI) / Math.max(peerNodes.length, 1);
    
    peerNodes.forEach((node, index) => {
        const angle = index * angleStep;
        nodePositions[node.id] = {
            x: centerX + radius * Math.cos(angle),
            y: centerY + radius * Math.sin(angle)
        };
    });
    
    // Draw edges
    data.edges.forEach(edge => {
        const from = nodePositions[edge.from];
        const to = nodePositions[edge.to];
        if (!from || !to) return;
        
        topologyCtx.strokeStyle = edge.status === 'connected' ? '#7ed321' : '#f5a623';
        topologyCtx.lineWidth = 2;
        topologyCtx.globalAlpha = 0.6;
        topologyCtx.beginPath();
        topologyCtx.moveTo(from.x, from.y);
        topologyCtx.lineTo(to.x, to.y);
        topologyCtx.stroke();
        topologyCtx.globalAlpha = 1.0;
    });
    
    // Draw nodes
    data.nodes.forEach(node => {
        const pos = nodePositions[node.id];
        if (!pos) return;
        
        let color, borderColor;
        if (node.type === 'self') {
            color = '#4a9eff';
            borderColor = '#6bb3ff';
        } else if (node.type === 'peer') {
            color = '#7ed321';
            borderColor = '#9ee342';
        } else {
            color = '#f5a623';
            borderColor = '#ffb84d';
        }
        
        // Draw node circle
        topologyCtx.fillStyle = color;
        topologyCtx.strokeStyle = borderColor;
        topologyCtx.lineWidth = 2;
        topologyCtx.beginPath();
        topologyCtx.arc(pos.x, pos.y, 20, 0, 2 * Math.PI);
        topologyCtx.fill();
        topologyCtx.stroke();
        
        // Draw node label
        topologyCtx.fillStyle = 'white';
        topologyCtx.font = '10px sans-serif';
        topologyCtx.textAlign = 'center';
        topologyCtx.textBaseline = 'middle';
        const label = node.label || node.id.substring(0, 4);
        topologyCtx.fillText(label, pos.x, pos.y);
    });
}

// ---------- Contracts ----------

function updateContracts() {
    fetch('/api/contracts/list', dashFetchOpts)
        .then(r => r.json())
        .then(data => {
            const countEl = document.getElementById('contracts-count');
            const listEl = document.getElementById('contracts-list');
            if (!countEl || !listEl) return;
            const contracts = data.contracts || [];
            countEl.textContent = contracts.length;
            if (contracts.length === 0) {
                listEl.innerHTML = '<div style="color:#666;text-align:center;padding:8px;">No contracts deployed</div>';
                return;
            }
            listEl.innerHTML = contracts.map(c => `
                <div style="padding:6px 0;border-bottom:1px solid #2a3441;display:flex;justify-content:space-between;">
                    <span style="color:#4a9eff;font-family:monospace;font-size:12px;">${c.contract_id}</span>
                    <span style="color:#a0a0a0;font-size:11px;">${c.functions} fn</span>
                </div>
            `).join('');
        })
        .catch(() => {});
}

function loadContractTemplates() {
    fetch('/api/contracts/templates', dashFetchOpts)
        .then(r => r.json())
        .then(data => {
            const sel = document.getElementById('contract-template');
            if (!sel) return;
            (data.templates || []).forEach(t => {
                const opt = document.createElement('option');
                opt.value = t.name;
                opt.textContent = `${t.name} — ${t.description || ''}`;
                sel.appendChild(opt);
            });
        })
        .catch(() => {});
}

function setupContractDeploy() {
    const btn = document.getElementById('deploy-contract-btn');
    if (!btn) return;
    btn.addEventListener('click', () => {
        const template = document.getElementById('contract-template').value;
        const contractId = document.getElementById('contract-id-input').value.trim();
        const statusEl = document.getElementById('contract-deploy-status');
        if (!template || !contractId) {
            statusEl.textContent = 'Select a template and enter a contract ID.';
            statusEl.style.color = '#f5a623';
            return;
        }
        statusEl.textContent = 'Deploying...';
        statusEl.style.color = '#a0a0a0';
        btn.disabled = true;
        fetch('/api/contracts/deploy', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ contract_id: contractId, template: template })
        })
        .then(r => r.json().then(d => ({ status: r.status, body: d })))
        .then(({ status, body }) => {
            if (status === 201) {
                statusEl.textContent = `Deployed ${body.contract_id} (${body.functions} functions)`;
                statusEl.style.color = '#7ed321';
                document.getElementById('contract-id-input').value = '';
                updateContracts();
            } else {
                statusEl.textContent = body.message || 'Deploy failed';
                statusEl.style.color = '#d0021b';
            }
        })
        .catch(err => {
            statusEl.textContent = 'Error: ' + err.message;
            statusEl.style.color = '#d0021b';
        })
        .finally(() => { btn.disabled = false; });
    });
}

// ---------- Bridge ----------

function updateBridge() {
    fetch('/api/bridge/locks', dashFetchOpts)
        .then(r => r.json())
        .then(data => {
            const countEl = document.getElementById('bridge-locks-count');
            const listEl = document.getElementById('bridge-locks-list');
            if (!countEl || !listEl) return;
            const locks = data.locks || [];
            countEl.textContent = locks.length;
            if (locks.length === 0) {
                listEl.innerHTML = '<div style="color:#666;text-align:center;padding:4px;">No active locks</div>';
            } else {
                listEl.innerHTML = locks.map(l => `
                    <div style="padding:4px 0;border-bottom:1px solid #2a3441;">
                        <span style="color:#4a9eff;font-family:monospace;">${l.lock_id.substring(0,16)}…</span>
                        <span class="status-badge status-${l.status === 'locked' ? 'healthy' : l.status === 'redeemed' ? 'degraded' : 'unhealthy'}" style="font-size:10px;padding:2px 8px;">${l.status}</span>
                        <span style="color:#a0a0a0;">${l.amount} ${l.asset} (${l.source_chain}→${l.target_chain})</span>
                    </div>
                `).join('');
            }
        })
        .catch(() => {
            const msgEl = document.getElementById('bridge-status-msg');
            if (msgEl) msgEl.textContent = 'Bridge not available (requires CGO/Dilithium)';
        });

    fetch('/api/bridge/swaps', dashFetchOpts)
        .then(r => r.json())
        .then(data => {
            const countEl = document.getElementById('bridge-swaps-count');
            const listEl = document.getElementById('bridge-swaps-list');
            if (!countEl || !listEl) return;
            const swaps = data.swaps || [];
            countEl.textContent = swaps.length;
            if (swaps.length === 0) {
                listEl.innerHTML = '';
            } else {
                listEl.innerHTML = swaps.map(s => `
                    <div style="padding:4px 0;border-bottom:1px solid #2a3441;">
                        <span style="color:#4a9eff;font-family:monospace;">${s.swap_id.substring(0,16)}…</span>
                        <span class="status-badge status-${s.status === 'completed' ? 'healthy' : s.status === 'initiated' || s.status === 'participated' ? 'degraded' : 'unhealthy'}" style="font-size:10px;padding:2px 8px;">${s.status}</span>
                        <span style="color:#a0a0a0;">${s.initiator_amount} (${s.initiator_chain}↔${s.participant_chain})</span>
                    </div>
                `).join('');
            }
        })
        .catch(() => {});
}

// ───── mTLS Certificate Management ─────

function initMTLS() {
    fetch('/api/mtls/status', dashFetchOpts)
        .then(r => r.json())
        .then(data => {
            const el = document.getElementById('mtls-status');
            if (el && data.mtls_available) {
                el.innerHTML = '<span style="color:#7ed321;">Available</span> — ' + (data.features || []).join(', ');
            }
        })
        .catch(() => {
            const el = document.getElementById('mtls-status');
            if (el) el.textContent = 'mTLS API not available';
        });

    const genBtn = document.getElementById('mtls-generate-btn');
    if (genBtn) {
        genBtn.addEventListener('click', () => {
            const nodeId = (document.getElementById('mtls-node-id') || {}).value || 'qsdm-node';
            const hostsStr = (document.getElementById('mtls-hosts') || {}).value || 'localhost';
            const hosts = hostsStr.split(',').map(h => h.trim()).filter(Boolean);

            genBtn.disabled = true;
            genBtn.textContent = 'Generating...';

            fetch('/api/mtls/generate', {
                ...dashFetchOpts,
                method: 'POST',
                headers: { ...dashFetchOpts.headers, 'Content-Type': 'application/json' },
                body: JSON.stringify({ node_id: nodeId, hosts: hosts })
            })
            .then(r => { if (!r.ok) throw new Error('Generation failed'); return r.json(); })
            .then(data => {
                document.getElementById('mtls-ca-cert').value = data.ca_cert || '';
                document.getElementById('mtls-node-cert').value = data.node_cert || '';
                document.getElementById('mtls-node-key').value = data.node_key || '';
                document.getElementById('mtls-result').style.display = 'block';

                const dlBtn = document.getElementById('mtls-download-btn');
                if (dlBtn) {
                    dlBtn.onclick = () => {
                        const bundle = [
                            '# CA Certificate\n' + data.ca_cert,
                            '\n# Node Certificate\n' + data.node_cert,
                            '\n# Node Private Key\n' + data.node_key
                        ].join('\n');
                        const blob = new Blob([bundle], { type: 'text/plain' });
                        const a = document.createElement('a');
                        a.href = URL.createObjectURL(blob);
                        a.download = nodeId + '-mtls-bundle.pem';
                        a.click();
                        URL.revokeObjectURL(a.href);
                    };
                }
            })
            .catch(err => {
                alert('Certificate generation failed: ' + err.message);
            })
            .finally(() => {
                genBtn.disabled = false;
                genBtn.textContent = 'Generate Certificates';
            });
        });
    }
}

// ───── User role display ─────

function loadUserRole() {
    fetch('/api/whoami', dashFetchOpts)
        .then(r => r.json())
        .then(data => {
            const el = document.getElementById('user-role-badge');
            if (!el) return;
            if (data.authenticated) {
                const roleColor = data.role === 'admin' ? '#ff6b6b' : '#7ed321';
                el.innerHTML = `<span style="color:${roleColor};font-weight:bold;text-transform:uppercase;font-size:11px;letter-spacing:1px;">${data.role}</span>`;
                el.title = `${data.address || data.user_id}`;
            } else {
                el.textContent = '';
            }

            // Hide admin-only controls for non-admin users
            const adminEls = document.querySelectorAll('[data-role="admin"]');
            adminEls.forEach(e => {
                e.style.display = data.role === 'admin' ? '' : 'none';
            });
        })
        .catch(() => {});
}

let ws = null;
let wsRetryDelay = 1000;

function connectWebSocket() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = proto + '//' + location.host + '/ws';
    ws = new WebSocket(url);

    ws.onopen = function() {
        wsRetryDelay = 1000;
        if (updateInterval) { clearInterval(updateInterval); updateInterval = null; }
    };

    ws.onmessage = function(evt) {
        try {
            const msg = JSON.parse(evt.data);
            if (msg.type === 'metrics') applyMetrics(msg.data);
            else if (msg.type === 'health') applyHealth(msg.data);
            else if (msg.type === 'topology') { topologyData = msg.data; renderTopology(msg.data); applyTopologyCounts(msg.data); }
        } catch (e) {}
    };

    ws.onclose = function() {
        ws = null;
        setTimeout(connectWebSocket, wsRetryDelay);
        wsRetryDelay = Math.min(wsRetryDelay * 2, 30000);
        if (!updateInterval) startPolling();
    };

    ws.onerror = function() { if (ws) ws.close(); };
}

function applyMetrics(data) {
    document.getElementById('tx-processed').textContent = formatNumber(data.transactions_processed || 0);
    document.getElementById('tx-valid').textContent = formatNumber(data.transactions_valid || 0);
    document.getElementById('tx-invalid').textContent = formatNumber(data.transactions_invalid || 0);
    document.getElementById('tx-stored').textContent = formatNumber(data.transactions_stored || 0);
    const validityRate = (data.validity_rate_percent || 0).toFixed(1);
    document.getElementById('validity-rate').textContent = validityRate + '%';
    document.getElementById('msg-sent').textContent = formatNumber(data.network_messages_sent || 0);
    document.getElementById('msg-recv').textContent = formatNumber(data.network_messages_received || 0);
    document.getElementById('proposals').textContent = formatNumber(data.proposals_created || 0);
    document.getElementById('votes').textContent = formatNumber(data.votes_cast || 0);
    document.getElementById('uptime').textContent = formatDuration(data.uptime_seconds || 0);
    document.getElementById('quarantines').textContent = formatNumber(data.quarantines_triggered || 0);
    document.getElementById('reputation').textContent = formatNumber(data.reputation_updates || 0);
    document.getElementById('last-updated').textContent = 'Last updated: ' + new Date().toLocaleTimeString();
}

function applyHealth(data) {
    const statusBadge = document.getElementById('overall-status');
    const overallStatus = data.overall_status || 'unknown';
    statusBadge.className = 'status-badge status-' + overallStatus;
    statusBadge.textContent = overallStatus.toUpperCase();
    const componentsDiv = document.getElementById('components');
    componentsDiv.innerHTML = '';
    if (data.components) {
        for (const [name, component] of Object.entries(data.components)) {
            const div = document.createElement('div');
            div.className = 'component-status';
            const statusClass = 'status-' + component.status;
            div.innerHTML = '<span class="component-name">' + name + '</span><span class="status-badge ' + statusClass + '">' + component.status + '</span>';
            componentsDiv.appendChild(div);
        }
    }
}

function applyTopologyCounts(data) {
    const pc = document.getElementById('topology-peer-count');
    const cc = document.getElementById('topology-connected-count');
    if (pc) pc.textContent = data.peerCount || 0;
    if (cc) cc.textContent = data.connectedCount || 0;
}

function formatEta(seconds) {
    if (!seconds || seconds <= 0) return '—';
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    if (d > 0) return d + 'd ' + h + 'h';
    const m = Math.floor((seconds % 3600) / 60);
    if (h > 0) return h + 'h ' + m + 'm';
    return m + 'm';
}

function updateNodeStatus() {
    // /api/v1/status is a public endpoint (see pkg/api/middleware.go
    // isPublicEndpoint allow-list) exposing node_role + coin metadata + live
    // tokenomics snapshot. We use it to populate the header pills
    // (Network/Role/Coin) and the Tokenomics panel (supply, block reward,
    // next halving ETA) added in Major Update Phase 3.4.
    fetch('/api/v1/status', dashFetchOpts)
        .then(r => r.ok ? r.json() : Promise.reject(new Error('status ' + r.status)))
        .then(data => {
            const net = document.getElementById('network-pill');
            const role = document.getElementById('node-role-pill');
            const coin = document.getElementById('coin-pill');
            if (net && data.network) net.textContent = 'Network: ' + data.network;
            if (role && data.node_role) role.textContent = 'Role: ' + data.node_role;
            if (coin && data.coin && data.coin.symbol) {
                const nm = data.coin.name || data.coin.symbol;
                coin.textContent = 'Coin: ' + nm + ' (' + data.coin.symbol + ')';
            }
            const tok = data.tokenomics;
            if (tok) {
                const supply = document.getElementById('tok-supply');
                const reward = document.getElementById('tok-reward');
                const epoch  = document.getElementById('tok-epoch');
                const halv   = document.getElementById('tok-halving');
                const cap    = document.getElementById('tok-cap');
                if (supply) supply.textContent = tok.emitted_cell + ' CELL';
                if (reward) reward.textContent = tok.block_reward_cell + ' CELL / block';
                if (epoch)  epoch.textContent  = '#' + tok.current_epoch;
                if (halv)   halv.textContent   =
                    'in ' + formatEta(tok.next_halving_eta_seconds) +
                    ' (block ' + tok.next_halving_height + ')';
                if (cap)    cap.textContent    = tok.cap_cell + ' CELL';
            }
        })
        .catch(() => {
            // Endpoint unreachable — leave panels in their loading state so
            // the rest of the dashboard remains rendered.
        });
}

function updateTrustPanel() {
    // /api/v1/trust/attestations/summary is public (anti-claim widget per
    // Major Update §8.5). 404 → endpoint disabled on this node; 503 →
    // aggregator still warming up (first 60 s of process lifetime).
    fetch('/api/v1/trust/attestations/summary', dashFetchOpts)
        .then(r => {
            if (r.status === 404) return { __disabled: true };
            if (r.status === 503) return { __warmup: true };
            return r.ok ? r.json() : Promise.reject(new Error('trust ' + r.status));
        })
        .then(data => {
            const ratioEl  = document.getElementById('trust-ratio');
            const statusEl = document.getElementById('trust-status');
            const lastEl   = document.getElementById('trust-last');
            const windowEl = document.getElementById('trust-window');
            if (data.__disabled) {
                if (ratioEl)  ratioEl.textContent  = 'disabled';
                if (statusEl) statusEl.textContent = 'n/a';
                return;
            }
            if (data.__warmup) {
                if (ratioEl)  ratioEl.textContent  = 'warming up…';
                if (statusEl) statusEl.textContent = '—';
                return;
            }
            if (ratioEl)  ratioEl.textContent  = data.attested + ' of ' + data.total_public;
            if (statusEl) statusEl.textContent = data.ngc_service_status || 'healthy';
            if (lastEl)   lastEl.textContent   = data.last_attested_at || 'never';
            if (windowEl) windowEl.textContent = data.fresh_within || '15m0s';
        })
        .catch(() => { /* keep panel in loading state */ });
}

function startPolling() {
    updateInterval = setInterval(() => {
        updateMetrics();
        updateHealth();
        updateNGCProofs();
        // Respect the operator's pause toggle on the
        // attestation-rejections tile — the other tiles keep
        // ticking, but the rejection table stays still while
        // the operator reads a row.
        if (!attestRejectionsState.paused) {
            updateAttestRejections();
        }
        updateContracts();
        updateBridge();
        updateTopology();
        updateNodeStatus();
        updateTrustPanel();
    }, 2000);
}

function startUpdates() {
    updateMetrics();
    updateHealth();
    updateNGCProofs();
    initAttestRejectionsControls();
    updateAttestRejections();
    updateContracts();
    updateBridge();
    loadContractTemplates();
    initTopologyCanvas();
    updateTopology();
    updateNodeStatus();
    updateTrustPanel();
    initMTLS();
    loadUserRole();

    connectWebSocket();
    // Fallback polling starts if WS fails to connect (see ws.onclose)
    startPolling();
}

// Refresh topology button + contract deploy
document.addEventListener('DOMContentLoaded', () => {
    const refreshBtn = document.getElementById('refresh-topology');
    if (refreshBtn) {
        refreshBtn.addEventListener('click', updateTopology);
    }
    setupContractDeploy();
});

// Start updates when page loads
document.addEventListener('DOMContentLoaded', startUpdates);

// Cleanup on page unload
window.addEventListener('beforeunload', () => {
    if (updateInterval) {
        clearInterval(updateInterval);
    }
});

