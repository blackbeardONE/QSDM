# QSDM Roadmap

> Historical note: this document was authored during the transitional **QSDM+** naming window. The platform has reverted to **QSDM** and introduced the native coin **Cell (CELL)** per the Major Update plan; see `REBRAND_NOTES.md` and `CELL_TOKENOMICS.md`. References to "QSDM+" below are historical and remain in place to keep roadmap milestones verifiable against the commits that delivered them.

**Last Updated:** April 2026 (session 70)
**Current Status:** Phase 1-3 Core Features Complete; in-repo scope ~99% complete. Remaining work requires external environments (auditor, real clusters, platform packaging).

---

## 🎯 Current Status

### ✅ Completed Phases

**Phase 1: 2D Mesh Launch** ✅
- libp2p networking
- Proof-of-Entanglement consensus
- SQLite storage with Zstandard compression
- ML-DSA-87 quantum-safe cryptography
- Optimized performance (memory pooling, batch signing)

**Phase 2: Scalability & Optimization** ✅
- Dynamic submesh management
- Priority-based routing
- WASM SDK integration
- Governance voting system
- CLI tools for submesh and governance

**Phase 3: 3D Mesh & Self-Healing** ✅
- 3D mesh validation (3-5 parent cells)
- Rule-based quarantine system
- Reputation management
- Monitoring and alerting

**Production Readiness** ✅
- Configuration file support (TOML/YAML)
- Enhanced logging (structured, log levels, request IDs)
- Storage metrics
- Error message improvements
- Ubuntu deployment support
- Project organization and cleanup
- Configurable API rate limiting (health routes exempt); liveness/readiness HTTP endpoints; storage `Ready()` for probes
- Deploy assets under **`QSDM/deploy/`** (Compose, Kubernetes, scripts); **`QSDM/Dockerfile`**; CI **`docker-image`** job builds the production image
- NGC proof ingest metrics and dashboard hooks (see NGC table below)

---

## NVIDIA NGC sidecar & GPU attestation (architecture bridge)

**Scope today:** NVIDIA-lock **primarily gates state-changing ledger HTTP APIs** (`mint` / `send` / token create, etc.). Optional **`nvidia_lock_gate_p2p`** (`QSDMPLUS_NVIDIA_LOCK_GATE_P2P`) additionally drops **libp2p-received** transactions after PoE validation when no qualifying ingested proof is present (same proof criteria as HTTP, **non-consuming** ring check so HTTP single-use nonce paths stay independent). **Full consensus / block-header attestation** (network-wide agreement on GPU proofs) is **not** implemented—treat P2P gate as a **node-local policy** on what this process stores/forwards. See **`NVIDIA_LOCK_CONSENSUS_SCOPE.md`** for the same distinction in short form.

Aligned with `nvidia_locked_qsdmplus_blockchain_architecture.md` (CUDA/AI/tensor proofs, gossip, NGC containers) and **Priority 2** (enhanced validation / monitoring):

| Deliverable | Location | Notes |
|-------------|----------|--------|
| Phase 1–3 proof prototype | `apps/qsdmplus-nvidia-ngc/validator_phase1.py` | Simulated PoW hash, deterministic PyTorch AI proof, FP16 CUDA matmul when GPU image is used, replay digest, `nvidia-smi` fingerprint |
| UDP gossip | `apps/qsdmplus-nvidia-ngc/gossip_daemon.py` + `docker-compose.yml` | Optional mesh summaries |
| NGC PyTorch image | `apps/qsdmplus-nvidia-ngc/Dockerfile.ngc` | Requires `docker login nvcr.io` (use env file; never commit keys) |
| Ingest into QSDM node | `QSDM/source/pkg/monitoring/ngc_proofs.go`, API `POST /api/v1/monitoring/ngc-proof` | Enable with env `QSDM_NGC_INGEST_SECRET`; sidecar sets `QSDM_NGC_REPORT_URL` + same secret |
| Metrics (JSON + Prometheus text) | Dashboard `GET /api/metrics`, `GET /api/metrics/prometheus` (JWT) | `qsdmplus_*` series for NVIDIA/NGC counters and tx/network totals |
| Optional P2P drop | `cmd/qsdmplus/transaction`, `pkg/monitoring/nvidia_p2p_gate.go` | `[api] nvidia_lock_gate_p2p` + `nvidia_lock` |

The main Go ledger remains **PoE + quantum-safe**; the sidecar is an **optional attestation and monitoring path** until native CUDA kernels in `pkg/mesh3d` are completed.

---

## 🚀 Next Steps: Priority Roadmap

### Priority 1: Production Hardening (1-2 weeks) 🔒

**Critical for production deployment:**

#### 1. Security Audit (1 week)
- **Code review** - Comprehensive security review
- **Vulnerability scanning** - Automated security scanning
- **Penetration testing** - External security testing
- **Input validation audit** - Review all input handling
- **Rate limiting** - **Baseline done:** configurable global limits; optional follow-up: per-route, authenticated quotas, WAF integration
- **Access control review** - Review authentication/authorization

**Impact:** Essential for production deployment  
**Files:** All source code, focus on `pkg/`, `cmd/`, `internal/`

---

#### 2. Deployment Automation (ongoing)
- **Docker Compose** - **`QSDM/deploy/docker-compose.cluster.yml`** (multi-node); validate in real environments
- **Kubernetes manifests** - **`QSDM/deploy/kubernetes/`**; ConfigMap-driven ports and rate limits
- **Deployment scripts** - **`QSDM/deploy/scripts/`**; extend for your registry and environments
- **Health monitoring** - **`/api/v1/health/live`** and **`/api/v1/health/ready`**; align orchestration probes
- **CI/CD** - **`.github/workflows/qsdmplus-go.yml`** (build, tests, **`docker-image`**); add publish/sign/deploy stages as needed

**Impact:** Easier deployment and scaling  
**Files:** **`QSDM/deploy/`**, **`QSDM/Dockerfile`**, **`.github/workflows/qsdmplus-go.yml`**

---

#### 3. Comprehensive Testing (1 week)
- **Integration testing** - Multi-node network tests
- **Load testing** - High transaction volume testing
- **Network partition testing** - Resilience testing
- **Resource exhaustion testing** - Stress testing
- **Security testing** - Attack scenario testing

**Impact:** Ensures reliability and stability  
**Files:** `QSDM/source/tests/` (Go module integration tests; run from `QSDM/source`)

---

### Priority 2: Feature Enhancements (Ongoing) 🔧

**Improve existing features:**

#### 1. ScyllaDB Integration — **Feature complete in repo**
- **Status:** Core integration in tree (`pkg/storage/scylla.go`): LWT dedupe, MVs (`transactions_by_tx_id`, `transactions_by_sender`, `transactions_by_recipient`), `cmd/scyllasmoke`, `cmd/migrate` (with `-stats-only` + per-phase timing), TLS + CQL password auth via `ScyllaClusterConfig` (env + TOML/YAML), `SCYLLA_AUTO_CREATE_KEYSPACE` for dev/CI, `SCYLLA_MIGRATION.md`, **`SCYLLA_CAPACITY.md`** operator runbook (session 70), `scylla-staging-verify{,-with-docker}.{ps1,sh}`, and the `qsdmplus-scylla-staging` CI workflow.
- **Remaining (external):** run the full `cmd/migrate` dry-run against a copy of production SQLite + multi-node Scylla staging cluster following `SCYLLA_CAPACITY.md` §2; verify TLS/auth end-to-end; run the rollback rehearsal in §6.

**Impact:** High throughput for production workloads
**Files:** `pkg/storage/scylla.go`, `pkg/storage/storage.go`, `cmd/migrate`, `cmd/scyllasmoke`, `docs/docs/SCYLLA_MIGRATION.md`, `docs/docs/SCYLLA_CAPACITY.md`

---

#### 2. Enhanced 3D Mesh Validation (1 week)
- **Expand cryptographic validation** - More validation rules
- **Performance optimization** - Improve validation speed
- **CUDA acceleration** - GPU-accelerated validation
- **Better error reporting** - Detailed validation errors

**Impact:** Better security and performance  
**Files:** `pkg/mesh3d/mesh3d.go`

---

#### 3. Improved Quarantine System (3-5 days)
- **Better alerting** - Enhanced alert mechanisms
- **Automated responses** - Auto-quarantine rules
- **Enhanced monitoring** - Better visibility
- **Recovery mechanisms** - Auto-recovery from quarantine

**Impact:** Better security and network health  
**Files:** `pkg/quarantine/`

---

#### 4. Network Topology Visualization (1 week)
- **Visual network map** - Interactive network visualization
- **Connection quality metrics** - Peer connection stats
- **Peer status dashboard** - Real-time peer monitoring
- **Submesh visualization** - Visual submesh representation

**Impact:** Better network monitoring and debugging  
**Files:** `pkg/dashboard/`, `pkg/monitoring/`

---

### Priority 3: Platform Expansion (1-2 months) 🌐

**Expand platform capabilities:**

#### 1. macOS Support — **Scripts in tree**
- **Status (session 70):** `QSDM/scripts/build_macos.sh` + `rebuild_liboqs_macos.sh` land in repo — universal2 / arm64 / x86_64 liboqs build via Homebrew OpenSSL@3, `DYLD_LIBRARY_PATH` wiring, and a `QSDM_NO_CGO=1` fallback.
- **Remaining (external):** run both scripts on an actual macOS host (Intel + Apple Silicon), publish a notarized build, and add a macOS CI runner step.

**Impact:** Broader platform support
**Files:** `QSDM/scripts/build_macos.sh`, `QSDM/scripts/rebuild_liboqs_macos.sh`

---

#### 2. Smart Contract Support (2-4 weeks)
- **WASM-based smart contracts** - Extend WASM SDK
- **Contract execution engine** - WASM runtime for contracts
- **Contract templates** - Example contracts
- **Contract testing framework** - Testing tools

**Impact:** Enable DApp development  
**Files:** `pkg/contracts/`, `wasm_modules/contracts/`

---

#### 3. Cross-Chain Interoperability (1-2 months)
- **Bridge protocols** - Connect to other blockchains
- **Atomic swaps** - Cross-chain transactions
- **Oracle integration** - External data feeds
- **Multi-chain wallet** - Support multiple chains

**Impact:** Broader ecosystem integration  
**Files:** `pkg/bridge/`, `pkg/oracle/`

---

### Priority 4: Developer Experience (Ongoing) 👨‍💻

**Improve developer tools and documentation:**

#### 1. Enhanced Documentation (1 week)
- **API documentation** - Complete API reference
- **Tutorial series** - Step-by-step guides
- **Video tutorials** - Visual learning resources
- **Best practices guide** - Development guidelines

**Impact:** Easier onboarding and development  
**Files:** `docs/` directory

---

#### 2. Developer Tools (2-3 weeks)
- **CLI improvements** - Better command-line tools
- **Debugging tools** - Enhanced debugging capabilities
- **Testing framework** - Comprehensive test utilities
- **Code generators** - Scaffolding tools

**Impact:** Faster development  
**Files:** `tools/` directory

---

#### 3. SDK Improvements — **Go SDK complete in repo**
- **Status (session 70):** `sdk/go/qsdmplus.go` now exposes a context-aware, typed API — `GetBalance`, `SendTransaction`, `GetTransaction`, `GetLiveness`, `GetReadiness`, `GetNodeStatus`, `GetPeers`, `GetMetricsJSON`, `GetMetricsPrometheus`, `ErrAPI`, `IsNotFound`, `IsUnauthorized`; covered by `httptest`-backed tests in `sdk/go/qsdmplus_test.go`.
- **Remaining:** JS / Python SDKs at feature-parity with Go, WASM SDK enhancements, and packaged module publication.

**Impact:** Easier integration
**Files:** `sdk/` directory

---

## 📅 Timeline Overview

### Near term (rolling)

**Production hardening**
- Security audit and scanning
- Expand integration/load/partition testing
- CI/CD extensions (image registry, signed releases, environment promotions)

**Feature enhancements**
- ScyllaDB operational hardening (if adopted)
- Enhanced 3D mesh validation and quarantine UX
- Network visualization and developer tooling

**Platform**
- macOS polish, smart contracts/WASM, cross-chain work — as priorities dictate (see sections above)

---

## 🎯 Immediate Next Steps (This Week)

### Option A: Security First 🔒
1. **Security audit** (1 week)
   - Code review
   - Vulnerability scanning
   - Penetration testing

### Option B: Validate delivery 🚀
1. **CI** — Confirm **QSDM+ Go** (including **`docker-image`**) is green
2. **Local or staging** — `docker build` / Compose or K8s smoke using **`QSDM/deploy/README.md`**
3. **CD** — Wire registry push and environment deploy from your pipeline (not one-size-fits-all in-repo)

### Option C: Feature Enhancement 🔧
1. **ScyllaDB integration** (3-5 days)
2. **Enhanced 3D mesh validation** (1 week)

### Option D: Developer Experience 👨‍💻
1. **Enhanced documentation** (1 week)
2. **Developer tools** (2-3 weeks)

---

## 💡 Recommended Focus

**For Production Deployment:**
1. ✅ **Security audit** - Required before production
2. ✅ **Deployment automation** - Makes deployment easy
3. ✅ **Comprehensive testing** - Ensures reliability

**For Feature Development:**
1. ✅ **ScyllaDB integration** - High throughput
2. ✅ **Enhanced 3D mesh validation** - Better security
3. ✅ **Network visualization** - Better monitoring

**For Ecosystem Growth:**
1. ✅ **Smart contract support** - Enable DApps
2. ✅ **SDK improvements** - Easier integration
3. ✅ **Cross-chain interoperability** - Broader reach

---

## 🔮 Long-Term Vision (6-12 Months)

### Phase 4: Advanced Features
- **Zero-knowledge proofs** - Privacy-preserving transactions
- **Advanced governance** - More sophisticated voting mechanisms
- **Self-healing network** - Automatic network recovery
- **Advanced analytics** - Network intelligence and insights

### Phase 5: Enterprise Features
- **Enterprise APIs** - Business-focused APIs
- **Compliance tools** - Regulatory compliance features
- **Enterprise support** - Commercial support offerings
- **SLA guarantees** - Service level agreements

### Phase 6: Ecosystem Growth
- **Developer grants** - Funding for developers
- **Partnerships** - Strategic partnerships
- **Community growth** - Expand community
- **Marketing and outreach** - Increase awareness

---

## 📊 Success Metrics

### Technical Metrics
- **Transaction throughput** - Target: 10,000+ TPS
- **Latency** - Target: <100ms average
- **Uptime** - Target: 99.9% availability
- **Security** - Zero critical vulnerabilities

### Adoption Metrics
- **Active nodes** - Target: 100+ nodes
- **Transactions per day** - Target: 1M+ transactions
- **Developer adoption** - Target: 50+ developers
- **Community growth** - Target: 1,000+ community members

---

## 🤝 Contributing

**Want to contribute?** See:
- `docs/CONTRIBUTING.md` - Contribution guidelines
- `docs/NEXT_STEPS.md` - Detailed next steps
- GitHub Issues - Open issues and feature requests

**Priority areas for contributors:**
- Testing and test coverage
- Documentation improvements
- Bug fixes
- Feature implementations
- Performance optimizations

---

## 📝 Notes

- **Flexibility:** Roadmap is subject to change based on community feedback and priorities
- **Phases:** Some features may be implemented in parallel
- **Dependencies:** Some features depend on others (e.g., smart contracts need WASM SDK)
- **Community:** Community input shapes the roadmap

---

*QSDM+: Building the Future of Quantum-Safe Distributed Ledgers* 🚀

