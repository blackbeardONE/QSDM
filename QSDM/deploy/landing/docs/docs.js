/* QSDM Docs SPA
 *
 * - Hash-based router (#/slug)
 * - Sidebar TOC is a curated, hand-ordered manifest of the ~70 .md files
 *   under QSDM/docs/docs/ (+ runbooks/).
 * - Markdown is fetched at runtime from raw.githubusercontent.com main, so
 *   docs are always current without a redeploy.
 * - Renders with markdown-it (vendored locally at /docs/lib/markdown-it.min.js).
 *
 * Routing examples:
 *   #/welcome          -> overview (QSDM/README.md)
 *   #/quick-start      -> QSDM/docs/docs/QUICK_START.md
 *   #/runbooks/wallet  -> QSDM/docs/docs/runbooks/WALLET_INCIDENT.md
 */
(function () {
  "use strict";

  // ----- constants -----

  var GH_USER   = "blackbeardONE";
  var GH_REPO   = "QSDM";
  var GH_BRANCH = "main";
  var DOCS_PREFIX_REPO = "QSDM/docs/docs"; // path inside the repo

  var RAW_BASE = "https://raw.githubusercontent.com/"
    + GH_USER + "/" + GH_REPO + "/" + GH_BRANCH + "/";

  var BLOB_BASE = "https://github.com/"
    + GH_USER + "/" + GH_REPO + "/blob/" + GH_BRANCH + "/";

  // ----- curated table of contents -----
  // Each entry: { slug, title, repoPath, badge? }
  // Sections drive the sidebar grouping.

  var SECTIONS = [
    {
      title: "Get started",
      items: [
        { slug: "welcome",            title: "Welcome",                          repoPath: "QSDM/README.md" },
        { slug: "quick-start",        title: "Quick start (5 min)",              repoPath: DOCS_PREFIX_REPO + "/QUICK_START.md" },
        { slug: "use-cases",          title: "Use cases",                        repoPath: DOCS_PREFIX_REPO + "/USE_CASES.md" },
        { slug: "user-guide",         title: "User guide",                       repoPath: DOCS_PREFIX_REPO + "/USER_GUIDE.md" },
        { slug: "architecture",       title: "Architecture explained",           repoPath: DOCS_PREFIX_REPO + "/ARCHITECTURE_EXPLAINED.md" },
        { slug: "node-roles",         title: "Node roles",                       repoPath: DOCS_PREFIX_REPO + "/NODE_ROLES.md" },
        { slug: "feature-summary",    title: "Feature summary",                  repoPath: DOCS_PREFIX_REPO + "/Feature Summary.md" },
      ],
    },
    {
      title: "Hive + Integrations",
      items: [
        {
          slug: "qsdm-hive",
          title: "Hive app guide",
          repoPath: DOCS_PREFIX_REPO + "/QSDM_HIVE.md",
          badge: "new",
          inlineMarkdown: [
            "# QSDM Hive",
            "",
            "QSDM Hive is the Windows and Linux client for CELL wallets, signed QSDM tasks, integrations, NVIDIA-attested protocol mining, and pooled CPU, GPU, or RAM participation.",
            "",
            "## Install path",
            "",
            "Hive is the public release path for most users. It is the recommended way to use CELL wallets, run signed tasks, link integrations, and start eligible mining work. The standalone console miner remains an advanced operator artifact. QSDM does not ship a separate consumer GUI miner.",
            "",
            "1. Install QSDM Hive.",
            "2. Create or import a QSDM wallet.",
            "3. Back up the QSDM keystore JSON and passphrase.",
            "4. Run CELL tasks, integrations, or qualifying mining work.",
            "",
            "### Linux x86-64",
            "",
            "1. Download the AppImage from the [Hive download page](/download.html).",
            "2. Run `chmod +x qsdm-hive-*-linux-x86_64.AppImage`.",
            "3. Start it with `./qsdm-hive-*-linux-x86_64.AppImage`.",
            "",
            "Hive expects desktop Linux with GTK 3. Linux Hive reads chain height, wallet balances, mining accounts, and rewards directly from canonical QSDM Core. Operator task metadata and projected task state use the restricted home-validator gateway, so ordinary users do not install or run a local Core. Version 1.3.89 bundles the native `qsdmcli` signer, `qsdmminer-console`, CUDA protocol solver, Edge Control 1.3.3, Agent 1.3.3, and the CUDA edge helper. QSDM Hive is the only desktop client; Edge Control, Agent, and Relay are support utilities. Hive has a dedicated Mother Hive workspace for secure Relay pairing, Agent discovery, pooled CPU/GPU/RAM visibility, chain-settlement identity, and task controls. The Miner task requires CUDA proof solving, fails closed instead of silently using CPU, and adopts a compatible packaged CUDA miner across Linux AppImage mount changes instead of starting a duplicate process. Signed task actions retry transient gateway failures with the same signed action ID, making a lost response safe without double-staking. Transient route failures show Reconnecting, retain the last confirmed values, and require two failures before a short outage circuit opens. Recently healthy APIs are not quarantined by one slow sibling route, while actual chain mismatches still fail closed. Wallet keys and signatures stay local. Hive verifies pinned genesis and a recent common block before value-bearing actions. Isolated, stale, ahead, divergent, or unverifiable ledgers cannot submit CELL transfers, stakes, rewards, referrals, faucet grants, or miner enrollment. Mining work and proofs use the canonical reward producer. Validator operators can explicitly set `QSDM_CORE_API_URL=http://127.0.0.1:8080/api/v1`; private-network overrides remain available. Mining does not require a Sky Fang account.",
            "",
            "## Wallet backup",
            "",
            "QSDM CELL wallet recovery uses the **QSDM keystore JSON plus its passphrase**. Hive profile phrases, when present, restore only local Hive profile data. They are not CELL wallet recovery phrases.",
            "",
            "## Task Studio",
            "",
            "Task Studio is available under **Add Task**. It publishes signed, versioned task manifests to the QSDM consensus catalog using the active wallet. Compatible catalog updates appear in Hive within about 15 seconds after validator finalization and do not require a Hive reinstall.",
            "",
            "The first safe runtime is the built-in `generic-proof-v1` capability. New executable capabilities require reviewed Hive code and a new Hive release; catalog entries cannot execute arbitrary remote JavaScript.",
            "",
            "## Tasks in Hive",
            "",
            "- **QSDM Miner** requires an NVIDIA Turing-or-newer GPU for v2 identity, attestation, and CUDA SHA3/DAG proof search. The Tensor-Core fork is a separate future consensus activation. The 10 CELL slashable bond can be prepaid or accumulated from mining rewards starting from zero CELL. A Sky Fang account is not required.",
            "- **QSDM Edge Worker CPU** shares bounded CPU capacity locally or through an authenticated Relay.",
            "- **QSDM Edge Worker GPU** shares bounded NVIDIA CUDA capacity. It is pooled compute, not protocol mining.",
            "- **QSDM Edge Worker RAM** shares a configured memory allowance for fixed memory-backed jobs.",
            "- **Mother Hive Task** assigns the coordinator role to this QSDM Hive and displays pooled CPU, GPU, RAM, Agents, jobs, and verified Relay receipts.",
            "- **Sky Fang - MMORPG** verifies that a Sky Fang account is linked to the active QSDM wallet before reward proofs are submitted.",
            "",
            "For a computer laboratory, walletless Agents send fixed work to a policy-controlled Relay. Mother Hive is the role of the active QSDM Hive, not another client. The target gross revenue split is 70% contributors, 15% Mother Hive operator, and 15% ecosystem reserve. The ecosystem share requires a dedicated public pooled-compute reserve wallet; no address is configured on QSDM Core yet. Automatic settlement remains disabled until contributor wallets, chain-verifiable Relay proofs, a published ecosystem reserve address, and funded workload escrow are in place. Agents cannot receive arbitrary scripts or shell commands.",
            "",
            "## Console mining",
            "",
            "Advanced operators can run `qsdmminer-console` directly when they need a terminal-first service workflow. Consumer setups should use Hive. The retired GUI miner is not a public release path.",
            "",
            "## Networking",
            "",
            "Hive uses local services for the desktop app and node monitor. Public reachability should go through the QSDM home gateway or network tunnel unless an operator intentionally exposes validator services.",
            "",
            "## Related pages",
            "",
            "- [Download QSDM Hive](/download.html)",
            "- [CELL tokenomics](#/cell-tokenomics)",
            "- [Sky Fang official website](https://skyfang.xyz/)",
            "- [Sky Fang integration notes](https://skyfang.xyz/docs)",
            "- [Miner quickstart](#/miner-quickstart)",
            "- [Pooled edge-compute guide](#/edge-pool)",
            "- [Wallet explanation](#/wallet-explanation)"
          ].join("\n")
        },
        {
          slug: "edge-pool",
          title: "Pooled edge compute",
          repoPath: DOCS_PREFIX_REPO + "/EDGE_POOL.md",
          badge: "new",
          inlineMarkdown: [
            "# QSDM Hive Mother Mode: Agents and Relay",
            "",
            "The edge path is `Agent computers -> QSDM Relay -> QSDM Hive (Mother Hive role) -> QSDM Core`. QSDM Hive is the only desktop client. Agents are outbound-only and walletless, and Edge Control is only a local setup utility. The Relay controls how much CPU, NVIDIA GPU, or RAM work reaches Hive.",
            "",
            "- Agents and Mother Hive use separate HMAC-SHA256 credentials. Requests also carry timestamps, nonces, and signed short-lived jobs.",
            "- Agents execute fixed resource algorithms only. There is no remote shell, script runner, or arbitrary command endpoint.",
            "- The Relay applies CPU/GPU/RAM work percentages, stores verified receipts, and exposes signed aggregate proofs to QSDM Hive.",
            "- The gross workload-revenue split is enforced by QSDM Core: 70% contributor-owner, 15% Mother Hive operator, and the remaining 15% ecosystem reserve. The fixed reserve address is `651a79b2b1790820dd73bda81be24057e1bc27377c1f1117c6db2ab79dc038ea`. Settlement fails closed unless the task pool is funded and the signed Relay fingerprint is authorized by the task manager on-chain.",
            "- This model is for a trusted private LAN, not anonymous internet enrollment.",
            "",
            "Hive 1.3.89 bundles Edge Control 1.3.3, Agent 1.3.3, and the CUDA helper. Additional computers can use [Edge Control 1.3.3 for Windows](/downloads/qsdm-edge-agent-1.3.3-windows-x86_64.zip) or [Edge Control 1.3.3 for Linux](/downloads/qsdm-edge-agent-1.3.3-linux-x86_64.tar.gz). Open Edge Control, choose Relay on the coordinating computer, copy its Agent pairing code, then paste that code on each Agent computer. Copy the separate Mother Hive pairing code into Hive's **Mother Hive** page; Agent credentials are rejected there. New Relay setups default to 50% CPU, 40% GPU, and 25% RAM. Existing policies remain unchanged, and Hive warns when any paired Relay policy reaches 90% because 100% limits can make an interactive workstation unresponsive. CPU, NVIDIA GPU, and RAM limits are sliders; CLI commands remain available for automation. Linux also includes supervised Agent and Relay services, automatic re-registration after Relay restarts, and exactly-once durable receipts.",
            "",
            "See the repository guide for setup commands, resource limits, firewall guidance, and status checks."
          ].join("\n")
        },
        {
          slug: "sky-fang-online",
          title: "Sky Fang - MMORPG",
          repoPath: DOCS_PREFIX_REPO + "/SKY_FANG_ONLINE.md",
          badge: "new",
          inlineMarkdown: [
            "# Sky Fang - MMORPG",
            "",
            "Sky Fang Online is a play-to-earn MMORPG integration powered by QSDM and CELL.",
            "",
            "## User flow",
            "",
            "1. Open Sky Fang at <https://skyfang.xyz/>.",
            "2. Link the active QSDM wallet from QSDM Hive.",
            "3. Return to Hive and run the **QSDM Sky Fang Link** task.",
            "",
            "Hive should verify the active wallet against Sky Fang before submitting the one-time reward proof. If the wallet is not linked, the task must stay blocked and show the wallet address that needs linking.",
            "",
            "## What this proves",
            "",
            "- A game account can bind to a QSDM wallet.",
            "- A Hive task can verify that binding before reward submission.",
            "- CELL can be used as the reward asset for integrations.",
            "",
            "## Operational notes",
            "",
            "Sky Fang link status is served by the Sky Fang site. If the site returns 503, Hive should treat the proof as not verifiable instead of granting rewards.",
            "",
            "## Related pages",
            "",
            "- [QSDM Hive guide](#/qsdm-hive)",
            "- [Sky Fang official website](https://skyfang.xyz/)",
            "- [Sky Fang integration notes](https://skyfang.xyz/docs)",
            "- [CELL tokenomics](#/cell-tokenomics)"
          ].join("\n")
        },
        {
          slug: "referral-reward-security",
          title: "Referral reward security",
          repoPath: DOCS_PREFIX_REPO + "/REFERRAL_REWARD_POOL_SECURITY.md",
          badge: "new"
        },
      ],
    },
    {
      title: "Wallet (self-custody)",
      items: [
        { slug: "web-wallet",         title: "Web wallet",                       repoPath: DOCS_PREFIX_REPO + "/WEB_WALLET.md" },
        { slug: "wallet-explanation", title: "How the wallet works",             repoPath: DOCS_PREFIX_REPO + "/WALLET_EXPLANATION.md" },
        { slug: "wallet-send",        title: "Send transaction (v0.4)",          repoPath: DOCS_PREFIX_REPO + "/V040_WALLET_SEND_DESIGN.md", badge: "new" },
        { slug: "p2p-wallet-ingress", title: "P2P wallet tx ingress",            repoPath: DOCS_PREFIX_REPO + "/P2P_WALLET_TX_INGRESS.md" },
        { slug: "replay-protection",  title: "Replay protection (v0.4.1)",       repoPath: DOCS_PREFIX_REPO + "/V041_REPLAY_PROTECTION_DESIGN.md", badge: "beta" },
      ],
    },
    {
      title: "Mining",
      items: [
        { slug: "miner-quickstart",   title: "Miner quickstart",                 repoPath: DOCS_PREFIX_REPO + "/MINER_QUICKSTART.md" },
        { slug: "miner-3050",         title: "RTX 3050 cookbook",                repoPath: DOCS_PREFIX_REPO + "/MINER_RTX_3050_COOKBOOK.md" },
        { slug: "mining-protocol-v2", title: "Mining protocol v2",               repoPath: DOCS_PREFIX_REPO + "/MINING_PROTOCOL_V2.md" },
        { slug: "mining-nvidia-lock", title: "NVIDIA-locked mining",             repoPath: DOCS_PREFIX_REPO + "/MINING_PROTOCOL_V2_NVIDIA_LOCKED.md" },
        { slug: "mining-tier3",       title: "Tier 3 scope",                     repoPath: DOCS_PREFIX_REPO + "/MINING_PROTOCOL_V2_TIER3_SCOPE.md" },
        { slug: "mining-ratification",title: "Protocol v2 ratification",         repoPath: DOCS_PREFIX_REPO + "/MINING_PROTOCOL_V2_RATIFICATION.md" },
        { slug: "audit-packet",       title: "Audit-packet mining",              repoPath: DOCS_PREFIX_REPO + "/AUDIT_PACKET_MINING.md" },
        { slug: "nvidia-lock-scope",  title: "NVIDIA-lock consensus scope",      repoPath: DOCS_PREFIX_REPO + "/NVIDIA_LOCK_CONSENSUS_SCOPE.md" },
        { slug: "mining-protocol",    title: "Mining protocol (legacy)",         repoPath: DOCS_PREFIX_REPO + "/MINING_PROTOCOL.md" },
      ],
    },
    {
      title: "Validators & operators",
      items: [
        { slug: "validator-quickstart", title: "Validator quickstart",           repoPath: DOCS_PREFIX_REPO + "/VALIDATOR_QUICKSTART.md" },
        { slug: "attester-quickstart",  title: "Attester quickstart",            repoPath: DOCS_PREFIX_REPO + "/ATTESTER_QUICKSTART.md" },
        { slug: "operator-guide",       title: "Operator guide",                 repoPath: DOCS_PREFIX_REPO + "/OPERATOR_GUIDE.md" },
        { slug: "production-deploy",    title: "Production deployment",          repoPath: DOCS_PREFIX_REPO + "/PRODUCTION_DEPLOYMENT.md" },
        { slug: "production-readiness", title: "Production readiness",           repoPath: DOCS_PREFIX_REPO + "/PRODUCTION_READINESS.md" },
        { slug: "ubuntu-deploy",        title: "Ubuntu deployment",              repoPath: DOCS_PREFIX_REPO + "/UBUNTU_DEPLOYMENT.md" },
        { slug: "stage-b-blr1",         title: "Stage B deploy (BLR1)",          repoPath: DOCS_PREFIX_REPO + "/STAGE_B_DEPLOY_BLR1.md" },
        { slug: "dashboard-access",     title: "Dashboard access",               repoPath: DOCS_PREFIX_REPO + "/DASHBOARD_ACCESS.md" },
      ],
    },
    {
      title: "Protocol & design",
      items: [
        { slug: "cell-tokenomics",       title: "CELL tokenomics",               repoPath: DOCS_PREFIX_REPO + "/CELL_TOKENOMICS.md" },
        { slug: "treasury-policy",       title: "Treasury policy",               repoPath: DOCS_PREFIX_REPO + "/TREASURY_POLICY.md" },
        { slug: "cryptography",          title: "Cryptography comparison",       repoPath: DOCS_PREFIX_REPO + "/CRYPTOGRAPHY_COMPARISON.md" },
        { slug: "attestation-sidecars",  title: "Attestation sidecars",          repoPath: DOCS_PREFIX_REPO + "/ATTESTATION_SIDECARS.md" },
        { slug: "wasm-interfaces",       title: "WASM module interfaces",        repoPath: DOCS_PREFIX_REPO + "/WASM_MODULE_INTERFACES.md" },
        { slug: "wasm-integration",      title: "WASM integration testing",      repoPath: DOCS_PREFIX_REPO + "/WASM_INTEGRATION_TESTING.md" },
        { slug: "roadmap",               title: "Roadmap",                       repoPath: DOCS_PREFIX_REPO + "/ROADMAP.md" },
      ],
    },
    {
      title: "Performance",
      items: [
        { slug: "perf-analysis",         title: "Performance analysis",          repoPath: DOCS_PREFIX_REPO + "/PERFORMANCE_ANALYSIS.md" },
        { slug: "perf-benchmark",        title: "Benchmark report",              repoPath: DOCS_PREFIX_REPO + "/PERFORMANCE_BENCHMARK_REPORT.md" },
        { slug: "mesh3d-gpu",            title: "Mesh3D GPU benchmark",          repoPath: DOCS_PREFIX_REPO + "/MESH3D_GPU_BENCHMARK.md" },
        { slug: "signing-reality",       title: "Signing-speed reality",         repoPath: DOCS_PREFIX_REPO + "/SIGNING_SPEED_REALITY.md" },
        { slug: "signing-optim",         title: "Signing optimization",          repoPath: DOCS_PREFIX_REPO + "/SIGNING_OPTIMIZATION.md" },
        { slug: "signing-optim-guide",   title: "Signing optimization guide",    repoPath: DOCS_PREFIX_REPO + "/SIGNING_OPTIMIZATION_GUIDE.md" },
        { slug: "optim-strategies",      title: "Optimization strategies",       repoPath: DOCS_PREFIX_REPO + "/OPTIMIZATION_STRATEGIES.md" },
        { slug: "quick-optim",           title: "Quick optimization guide",      repoPath: DOCS_PREFIX_REPO + "/QUICK_OPTIMIZATION_GUIDE.md" },
        { slug: "scylla-capacity",       title: "Scylla capacity",               repoPath: DOCS_PREFIX_REPO + "/SCYLLA_CAPACITY.md" },
        { slug: "scylla-migration",      title: "Scylla migration",              repoPath: DOCS_PREFIX_REPO + "/SCYLLA_MIGRATION.md" },
      ],
    },
    {
      title: "Reference",
      items: [
        { slug: "api-reference",         title: "API reference",                 repoPath: DOCS_PREFIX_REPO + "/API_REFERENCE.md" },
        { slug: "api-versioning",        title: "API versioning",                repoPath: DOCS_PREFIX_REPO + "/API_VERSIONING.md", badge: "new" },
        { slug: "api-security",          title: "API security",                  repoPath: DOCS_PREFIX_REPO + "/API_SECURITY.md" },
        { slug: "cli-phase2",            title: "Phase 2 CLI guide",             repoPath: DOCS_PREFIX_REPO + "/PHASE2_CLI_USER_GUIDE.md" },
        { slug: "troubleshooting",       title: "Troubleshooting",               repoPath: DOCS_PREFIX_REPO + "/TROUBLESHOOTING.md" },
        { slug: "security-audit",        title: "Security audit",                repoPath: DOCS_PREFIX_REPO + "/SECURITY_AUDIT.md", badge: "updated" },
        { slug: "comparative",           title: "Comparative analysis",          repoPath: DOCS_PREFIX_REPO + "/COMPARATIVE_ANALYSIS.md" },
        { slug: "final-comparison",      title: "Final comparison",              repoPath: DOCS_PREFIX_REPO + "/FINAL_COMPARISON.md" },
        { slug: "release-evidence-042",  title: "Release evidence v0.4.2",       repoPath: DOCS_PREFIX_REPO + "/RELEASE_EVIDENCE_v0.4.2.md", badge: "new" },
        { slug: "release-evidence-041",  title: "Release evidence v0.4.1",       repoPath: DOCS_PREFIX_REPO + "/RELEASE_EVIDENCE_v0.4.1.md" },
        { slug: "release-evidence-040",  title: "Release evidence v0.4.0",       repoPath: DOCS_PREFIX_REPO + "/RELEASE_EVIDENCE_v0.4.0.md" },
        { slug: "release-evidence-033",  title: "Release evidence v0.3.3",       repoPath: DOCS_PREFIX_REPO + "/RELEASE_EVIDENCE_v0.3.3.md" },
        { slug: "release-evidence",      title: "Release evidence (rollup)",     repoPath: DOCS_PREFIX_REPO + "/RELEASE_EVIDENCE.md" },
        { slug: "docs-portal-evidence",  title: "Docs portal ship log",          repoPath: DOCS_PREFIX_REPO + "/DOCS_PORTAL_EVIDENCE.md", badge: "new" },
        { slug: "v030-postrelease",      title: "v0.3.0 post-release verify",    repoPath: DOCS_PREFIX_REPO + "/V030_POST_RELEASE_VERIFICATION.md" },
      ],
    },
    {
      title: "Runbooks (incident response)",
      items: [
        { slug: "runbooks/index",                title: "Runbook index",                 repoPath: DOCS_PREFIX_REPO + "/runbooks/README.md" },
        { slug: "runbooks/mining-liveness",      title: "Mining liveness",               repoPath: DOCS_PREFIX_REPO + "/runbooks/MINING_LIVENESS.md" },
        { slug: "runbooks/wallet",               title: "Wallet incident",               repoPath: DOCS_PREFIX_REPO + "/runbooks/WALLET_INCIDENT.md" },
        { slug: "runbooks/networking",           title: "Networking incident",           repoPath: DOCS_PREFIX_REPO + "/runbooks/NETWORKING_INCIDENT.md" },
        { slug: "runbooks/storage",              title: "Storage incident",              repoPath: DOCS_PREFIX_REPO + "/runbooks/STORAGE_INCIDENT.md" },
        { slug: "runbooks/enrollment",           title: "Enrollment incident",           repoPath: DOCS_PREFIX_REPO + "/runbooks/ENROLLMENT_INCIDENT.md" },
        { slug: "runbooks/trust",                title: "Trust incident",                repoPath: DOCS_PREFIX_REPO + "/runbooks/TRUST_INCIDENT.md" },
        { slug: "runbooks/reputation",           title: "Reputation incident",           repoPath: DOCS_PREFIX_REPO + "/runbooks/REPUTATION_INCIDENT.md" },
        { slug: "runbooks/slashing",             title: "Slashing incident",             repoPath: DOCS_PREFIX_REPO + "/runbooks/SLASHING_INCIDENT.md" },
        { slug: "runbooks/quarantine",           title: "Quarantine incident",           repoPath: DOCS_PREFIX_REPO + "/runbooks/QUARANTINE_INCIDENT.md" },
        { slug: "runbooks/ngc-submission",       title: "NGC submission incident",       repoPath: DOCS_PREFIX_REPO + "/runbooks/NGC_SUBMISSION_INCIDENT.md" },
        { slug: "runbooks/rejection-flood",      title: "Rejection flood",               repoPath: DOCS_PREFIX_REPO + "/runbooks/REJECTION_FLOOD.md" },
        { slug: "runbooks/submesh-policy",       title: "Submesh policy incident",       repoPath: DOCS_PREFIX_REPO + "/runbooks/SUBMESH_POLICY_INCIDENT.md" },
        { slug: "runbooks/arch-spoof",           title: "Arch spoof incident",           repoPath: DOCS_PREFIX_REPO + "/runbooks/ARCH_SPOOF_INCIDENT.md" },
        { slug: "runbooks/hot-reload",           title: "Hot-reload incident",           repoPath: DOCS_PREFIX_REPO + "/runbooks/HOT_RELOAD_INCIDENT.md" },
        { slug: "runbooks/governance-authority", title: "Governance-authority incident", repoPath: DOCS_PREFIX_REPO + "/runbooks/GOVERNANCE_AUTHORITY_INCIDENT.md" },
        { slug: "runbooks/contracts-bridge",     title: "Contracts/bridge incident",     repoPath: DOCS_PREFIX_REPO + "/runbooks/CONTRACTS_BRIDGE_INCIDENT.md" },
        { slug: "runbooks/stub-deployment",      title: "Stub deployment incident",      repoPath: DOCS_PREFIX_REPO + "/runbooks/STUB_DEPLOYMENT_INCIDENT.md" },
        { slug: "runbooks/operator-hygiene",     title: "Operator hygiene incident",     repoPath: DOCS_PREFIX_REPO + "/runbooks/OPERATOR_HYGIENE_INCIDENT.md" },
      ],
    },
    {
      title: "Project",
      items: [
        { slug: "contributing",  title: "Contributing",   repoPath: DOCS_PREFIX_REPO + "/CONTRIBUTING.md" },
        { slug: "rebrand",       title: "Rebrand notes",  repoPath: DOCS_PREFIX_REPO + "/REBRAND_NOTES.md" },
      ],
    },
  ];

  // Slug → item map (built once)
  var SLUG_INDEX = (function () {
    var idx = Object.create(null);
    SECTIONS.forEach(function (sec) {
      sec.items.forEach(function (it) {
        idx[it.slug] = it;
        // map repoPath basename to slug for cross-doc relative-link rewriting
        idx["__path:" + it.repoPath.toLowerCase()] = it;
      });
    });
    return idx;
  })();

  // ----- markdown-it setup -----

  var md = window.markdownit({
    html: false,
    linkify: true,
    breaks: false,
    typographer: true,
  });

  // Add slug ids to headings so anchor links work.
  function slugifyText(text) {
    return String(text || "")
      .toLowerCase()
      .replace(/[^\w\s-]/g, "")
      .trim()
      .replace(/\s+/g, "-")
      .replace(/-+/g, "-");
  }
  var defaultHeadingOpen = md.renderer.rules.heading_open || function (tokens, idx, options, env, self) {
    return self.renderToken(tokens, idx, options);
  };
  md.renderer.rules.heading_open = function (tokens, idx, options, env, self) {
    var inline = tokens[idx + 1];
    if (inline && inline.children && inline.children.length) {
      var text = inline.children.map(function (c) { return c.content || ""; }).join("").trim();
      var id = slugifyText(text);
      if (id) tokens[idx].attrSet("id", id);
    }
    return defaultHeadingOpen(tokens, idx, options, env, self);
  };

  // Rewrite link targets so:
  //   - relative .md links to known docs become #/<slug>
  //   - other relative paths become absolute GitHub blob URLs (open in new tab)
  //   - anchor (#…) links stay as-is
  var defaultLinkOpen = md.renderer.rules.link_open || function (tokens, idx, options, env, self) {
    return self.renderToken(tokens, idx, options);
  };
  md.renderer.rules.link_open = function (tokens, idx, options, env, self) {
    var token = tokens[idx];
    var hrefIdx = token.attrIndex("href");
    if (hrefIdx >= 0) {
      var href = token.attrs[hrefIdx][1];
      var rewritten = rewriteLink(href, env && env.repoPath);
      if (rewritten.href !== href) token.attrs[hrefIdx][1] = rewritten.href;
      if (rewritten.external) {
        token.attrSet("target", "_blank");
        token.attrSet("rel", "noopener");
      }
    }
    return defaultLinkOpen(tokens, idx, options, env, self);
  };

  // Rewrite <img src> so relative paths resolve against the doc's repo dir.
  var defaultImage = md.renderer.rules.image;
  md.renderer.rules.image = function (tokens, idx, options, env, self) {
    var token = tokens[idx];
    var srcIdx = token.attrIndex("src");
    if (srcIdx >= 0) {
      var src = token.attrs[srcIdx][1];
      if (!/^(https?:|data:|\/)/i.test(src) && env && env.repoPath) {
        token.attrs[srcIdx][1] = RAW_BASE + encRepoPath(resolveRelative(env.repoPath, src));
      }
    }
    if (defaultImage) return defaultImage(tokens, idx, options, env, self);
    return self.renderToken(tokens, idx, options);
  };

  function resolveRelative(basePath, rel) {
    // basePath = "QSDM/docs/docs/QUICK_START.md"; rel = "./runbooks/WALLET_INCIDENT.md"
    var baseDir = basePath.replace(/[^\/]*$/, ""); // strip filename
    var parts = (baseDir + rel).split("/");
    var out = [];
    parts.forEach(function (p) {
      if (p === "" || p === ".") return;
      if (p === "..") { out.pop(); return; }
      out.push(p);
    });
    // Preserve leading "" only if original was absolute (it isn't here).
    return out.join("/");
  }

  // Encode a repo path for use in a URL. Preserves `/` separators (so it
  // can be appended to RAW_BASE / BLOB_BASE directly) but escapes spaces
  // and other reserved characters. Required because a small number of
  // docs in the repo have spaces in their filenames (e.g. the
  // "Feature Summary.md" entry).
  function encRepoPath(p) {
    return String(p).split("/").map(encodeURIComponent).join("/");
  }

  function rewriteLink(href, basePath) {
    if (!href) return { href: href, external: false };

    // Pure anchor — keep
    if (href.charAt(0) === "#") return { href: href, external: false };

    // Site-root local link — keep
    if (href.charAt(0) === "/") return { href: href, external: false };

    // Absolute URL
    if (/^https?:\/\//i.test(href) || /^mailto:/i.test(href)) {
      return { href: href, external: true };
    }

    // Resolve relative against current doc
    if (basePath) {
      var resolved = resolveRelative(basePath, href);
      var splitAnchor = resolved.split("#");
      var pathOnly = splitAnchor[0];
      var anchor = splitAnchor[1] ? "#" + splitAnchor[1] : "";
      var lookup = SLUG_INDEX["__path:" + pathOnly.toLowerCase()];
      if (lookup) {
        return { href: "#/" + lookup.slug + (anchor ? anchor : ""), external: false };
      }
      // Not a known doc — link out to GitHub blob view
      return { href: BLOB_BASE + encRepoPath(pathOnly) + anchor, external: true };
    }
    return { href: href, external: true };
  }

  // ----- sidebar render -----

  function renderSidebar() {
    var nav = document.getElementById("docsNav");
    var html = "";
    SECTIONS.forEach(function (sec) {
      html += '<div class="nav-section">';
      html += '<div class="nav-section-title">' + escapeHtml(sec.title) + "</div>";
      sec.items.forEach(function (it) {
        var b = "";
        if (it.badge === "new")     b = ' <span class="badge new">NEW</span>';
        if (it.badge === "beta")    b = ' <span class="badge beta">BETA</span>';
        if (it.badge === "updated") b = ' <span class="badge updated">UPDATED</span>';
        html += '<a class="nav-item" data-slug="' + escapeAttr(it.slug) + '" href="#/' + escapeAttr(it.slug) + '">'
              + escapeHtml(it.title) + b + "</a>";
      });
      html += "</div>";
    });
    nav.innerHTML = html;
  }

  function setActiveNav(slug) {
    var items = document.querySelectorAll("#docsNav .nav-item");
    items.forEach(function (a) {
      if (a.getAttribute("data-slug") === slug) a.classList.add("active");
      else a.classList.remove("active");
    });
  }

  // ----- search/filter -----

  function applyFilter(q) {
    q = (q || "").trim().toLowerCase();
    var sections = document.querySelectorAll("#docsNav .nav-section");
    sections.forEach(function (sec) {
      var visible = 0;
      var items = sec.querySelectorAll(".nav-item");
      items.forEach(function (a) {
        var text = a.textContent.toLowerCase();
        var slug = (a.getAttribute("data-slug") || "").toLowerCase();
        var match = !q || text.indexOf(q) !== -1 || slug.indexOf(q) !== -1;
        a.classList.toggle("hidden", !match);
        if (match) visible++;
      });
      sec.classList.toggle("hidden", visible === 0);
    });
  }

  // ----- routing & rendering -----

  function getRoute() {
    var hash = window.location.hash || "";
    if (hash.indexOf("#/") === 0) {
      var rest = hash.slice(2);
      var hashIdx = rest.indexOf("#");
      var slug = hashIdx >= 0 ? rest.slice(0, hashIdx) : rest;
      var anchor = hashIdx >= 0 ? rest.slice(hashIdx + 1) : "";
      return { slug: decodeURIComponent(slug), anchor: anchor };
    }
    return { slug: "welcome", anchor: "" };
  }

  function renderWelcome() {
    var html = ''
      + '<div class="doc-welcome">'
      + '<h1>QSDM knowledge base</h1>'
      + '<p>Quickstarts, runbooks, protocol design, and reference for the '
      + '<strong>Quantum-Secure Dynamic Mesh Ledger</strong>. Everything you '
      + 'need to use QSDM Hive, self-custody CELL, run integrations, mine on NVIDIA hardware, join CPU shared edge tasks, or operate a validator.</p>'
      + '<div class="welcome-cards">'
      + cardHtml("quick-start",        "Quick start",        "Get a local node + wallet running in 5 minutes.")
      + cardHtml("qsdm-hive",          "QSDM Hive",          "Windows and Linux client for CELL wallets, tasks, integrations, and mining paths.")
      + cardHtml("sky-fang-online",    "Sky Fang - MMORPG",  "Play-to-earn MMORPG integration powered by QSDM and CELL.")
      + cardHtml("miner-quickstart",   "Mine on NVIDIA",     "Run the optional miner task if your GPU and signer qualify.")
      + cardHtml("validator-quickstart","Run a validator",   "CPU-only validator, attestation sidecars, NGC submission.")
      + cardHtml("web-wallet",         "Web wallet",         "ML-DSA-87 self-custody in the browser, no extension.")
      + cardHtml("api-reference",      "API reference",      "Public HTTP endpoints with auth + replay semantics.")
      + cardHtml("runbooks/index",     "Runbooks",           "Incident response, on-call procedures, recovery.")
      + "</div>"
      + "</div>";
    setContent(html);
    setActiveNav("welcome");
    updateEditLink({ repoPath: "QSDM/README.md" });
    document.title = "QSDM Docs — Knowledge base";
  }
  function cardHtml(slug, title, body) {
    return '<div class="welcome-card">'
      + '<h3>' + escapeHtml(title) + '</h3>'
      + '<p>' + escapeHtml(body) + '</p>'
      + '<a href="#/' + escapeAttr(slug) + '">Read →</a>'
      + '</div>';
  }

  function renderDoc(item, anchor) {
    setContent('<div class="doc-loading">Loading <code>' + escapeHtml(item.repoPath) + '</code>…</div>');
    setActiveNav(item.slug);
    updateEditLink(item);
    document.title = item.title + " — QSDM Docs";

    if (item.inlineMarkdown) {
      var envInline = { repoPath: item.repoPath || "" };
      setContent(md.render(item.inlineMarkdown, envInline));
      enhanceCodeBlocks();
      if (anchor) {
        var inlineTarget = document.getElementById(anchor);
        if (inlineTarget) inlineTarget.scrollIntoView({ behavior: "instant", block: "start" });
      } else {
        window.scrollTo(0, 0);
      }
      return;
    }

    var url = RAW_BASE + encRepoPath(item.repoPath);
    fetch(url, { cache: "no-cache" })
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status + " fetching " + item.repoPath);
        return r.text();
      })
      .then(function (text) {
        var env = { repoPath: item.repoPath };
        var html = md.render(text, env);
        setContent(html);
        enhanceCodeBlocks();
        if (anchor) {
          var target = document.getElementById(anchor);
          if (target) target.scrollIntoView({ behavior: "instant", block: "start" });
        } else {
          window.scrollTo(0, 0);
        }
      })
      .catch(function (err) {
        setContent(''
          + '<div class="doc-error">'
          + '<h2>Could not load this page</h2>'
          + '<p>' + escapeHtml(err.message || String(err)) + '</p>'
          + '<p>You can read it directly on '
          + '<a href="' + BLOB_BASE + encRepoPath(item.repoPath) + '" target="_blank" rel="noopener">GitHub</a>.</p>'
          + '</div>');
      });
  }

  function route() {
    var r = getRoute();
    if (!r.slug || r.slug === "welcome") {
      renderWelcome();
      return;
    }
    var item = SLUG_INDEX[r.slug];
    if (!item) {
      setContent(''
        + '<div class="doc-error">'
        + '<h2>Page not found</h2>'
        + '<p>The slug <code>' + escapeHtml(r.slug) + '</code> is not in the index. '
        + 'Pick a page from the sidebar.</p>'
        + '</div>');
      return;
    }
    renderDoc(item, r.anchor);
  }

  // ----- content helpers -----

  function setContent(html) {
    var el = document.getElementById("docContent");
    el.innerHTML = html;
  }
  function updateEditLink(item) {
    var a = document.getElementById("docEditLink");
    if (a && item && item.repoPath) {
      a.setAttribute("href", BLOB_BASE + encRepoPath(item.repoPath));
    }
  }
  function enhanceCodeBlocks() {
    var pres = document.querySelectorAll("#docContent pre");
    pres.forEach(function (pre) {
      if (pre.querySelector(".copy-btn")) return;
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "copy-btn";
      btn.textContent = "Copy";
      btn.addEventListener("click", function () {
        var code = pre.querySelector("code");
        var text = code ? code.innerText : pre.innerText;
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(function () { flashCopied(btn); });
        } else {
          var ta = document.createElement("textarea");
          ta.value = text;
          document.body.appendChild(ta);
          ta.select();
          try { document.execCommand("copy"); flashCopied(btn); } catch (_) {}
          document.body.removeChild(ta);
        }
      });
      pre.appendChild(btn);
    });
  }
  function flashCopied(btn) {
    btn.classList.add("copied");
    btn.textContent = "Copied";
    setTimeout(function () {
      btn.classList.remove("copied");
      btn.textContent = "Copy";
    }, 1400);
  }

  // ----- mobile sidebar toggle -----

  function wireSidebarToggle() {
    var btn = document.getElementById("sidebarToggle");
    var sb  = document.getElementById("docsSidebar");
    btn.addEventListener("click", function () {
      var open = sb.classList.toggle("open");
      btn.setAttribute("aria-expanded", open ? "true" : "false");
    });
    document.addEventListener("click", function (e) {
      if (window.innerWidth > 980) return;
      if (!sb.classList.contains("open")) return;
      if (sb.contains(e.target) || btn.contains(e.target)) return;
      if (e.target.closest && e.target.closest(".nav-item")) return;
      sb.classList.remove("open");
      btn.setAttribute("aria-expanded", "false");
    });
    document.getElementById("docsNav").addEventListener("click", function (e) {
      if (e.target.closest && e.target.closest(".nav-item") && window.innerWidth <= 980) {
        sb.classList.remove("open");
        btn.setAttribute("aria-expanded", "false");
      }
    });
  }

  // ----- version pill auto-bump (fetches latest release tag) -----

  function refreshVersionPill() {
    fetch("https://api.github.com/repos/" + GH_USER + "/" + GH_REPO + "/releases/latest", { cache: "no-cache" })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (rel) {
        if (!rel || !rel.tag_name) return;
        var pill = document.getElementById("ver-pill");
        var txt  = document.getElementById("ver-pill-text");
        if (pill && txt) {
          txt.textContent = rel.tag_name;
          pill.setAttribute("href", rel.html_url || pill.getAttribute("href"));
          pill.setAttribute("title", "Latest release: " + rel.tag_name);
        }
      })
      .catch(function () { /* offline / rate-limited — keep static value */ });
  }

  // ----- utils -----

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }
  function escapeAttr(s) { return escapeHtml(s); }

  // ----- boot -----

  document.addEventListener("DOMContentLoaded", function () {
    renderSidebar();
    wireSidebarToggle();
    document.getElementById("docsSearch").addEventListener("input", function (e) {
      applyFilter(e.target.value);
    });
    window.addEventListener("hashchange", route);
    refreshVersionPill();
    route();
  });
})();
