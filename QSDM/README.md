This project needs a lot of time and hardware requirements. Help me accomplish my goal of serving humanity new technology for better life. Buy me a coffee! >>
https://ko-fi.com/joedeldalioan

or send me some love via paypal or crypto >>

- **Paypal:** paypal.me/Dalioan
- **Bitcoin (Unisat):** bc1qu8pknsrwvssspq4c6a5j4p3x835phq8n3ajcgw
- **Solana:** FNEdD3PWMLwbNKxtaHy3W2NVfRJ7wqDNx4M9je8Xc6Mw
- **Tron:** TGVrpryTJAmWEPWTKYB3tGRv5gWhE2g9Lo
- **Metamask:** 0x9F32B8346bD728DF3AB7775971705D02fb86dD9c

# Quantum-Secure Dynamic Mesh Ledger (QSDM)

> **Rebrand notice (Major Update):** the platform is migrating from the transitional name **QSDM+** back to **QSDM**, and introducing the native coin **Cell (CELL)**. Configuration files, environment variables, and HTTP headers that used the `qsdmplus` / `QSDMPLUS_*` / `X-QSDMPLUS-*` names continue to work during the deprecation window. See [`docs/docs/REBRAND_NOTES.md`](docs/docs/REBRAND_NOTES.md) for the full migration table and [`docs/docs/CELL_TOKENOMICS.md`](docs/docs/CELL_TOKENOMICS.md) for the coin specification.

**QSDM** is the public product name (previously transitionally **QSDM+**). Quantum-Secure Dynamic Mesh Ledger (QSDM) is a non-AI, decentralized electronic cash system designed for quantum resistance and hardware-agnostic operation. The native coin is **Cell (CELL)** — see `docs/docs/CELL_TOKENOMICS.md`.

## Overview
QSDM supports both **Windows 10+** and **Linux (Ubuntu 24.04+)**. macOS support is in development.

QSDM is developed in phases:

- **Phase 1: 2D Mesh Launch**  
  Focus on stability and manual bootstrapping using libp2p for networking, Proof-of-Entanglement consensus, SQLite with Zstandard compression for storage, and ML-DSA-87 (NIST FIPS 204) for quantum-safe cryptography with optimized performance.

- **Phase 2: Scalability & Optimization**  
  Introduces dynamic submeshes, priority-based routing, WASM SDK integration, and ScyllaDB for high throughput.

- **Phase 3: 3D Mesh & Self-Healing**  
  Adds 3D mesh validation, rule-based quarantines, reputation system, and CUDA acceleration.

## Repository layout (QSDM monorepo)

This directory (**`QSDM/`**) is the **QSDM ledger node** (electronic cash / token APIs, consensus, storage). Sibling folders under the workspace root live in **`apps/`** (marketing site, NGC sidecar, game-integration notes). See the root **`README.md`** one level up.

### Optional: NGC sidecar and NVIDIA-lock (HTTP API)

The **`apps/qsdmplus-nvidia-ngc`** sidecar can push GPU proof bundles to the node. Enable **`[api] nvidia_lock`** in config (and **`QSDMPLUS_NGC_INGEST_SECRET`**) so selected ledger HTTP routes require a recent GPU-attested proof. Optional **`nvidia_lock_expected_node_id`** / **`QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID`** must match **`QSDMPLUS_NGC_PROOF_NODE_ID`** on the sidecar when you use proof binding. Optional **`nvidia_lock_proof_hmac_secret`** / **`QSDMPLUS_NVIDIA_LOCK_PROOF_HMAC_SECRET`** pairs with **`QSDMPLUS_NGC_PROOF_HMAC_SECRET`** on the sidecar for **`qsdmplus_proof_hmac`**. For non-CGO builds, set **`QSDMPLUS_JWT_HMAC_SECRET`** (or **`jwt_hmac_secret`** in config) for JWT HMAC. Production: **`strict_secrets`** / **`QSDMPLUS_STRICT_SECRETS`**, optional **`nvidia_lock_gate_p2p`** / **`QSDMPLUS_NVIDIA_LOCK_GATE_P2P`** (libp2p tx drops when no proof), dashboard **`/api/metrics/prometheus`** for scraping (**JWT** or **`metrics_scrape_secret`** / **`QSDMPLUS_DASHBOARD_METRICS_SCRAPE_SECRET`** with **`X-QSDMPLUS-Metrics-Scrape-Secret`** or **Bearer**), and **15/min** on **`GET .../ngc-challenge`** (sidecar honors **429** **`Retry-After`**; stagger validators if many share one NAT IP). Operator notes: [deploy/README.md](deploy/README.md); roadmap scope: [docs/docs/ROADMAP.md](docs/docs/ROADMAP.md); sidecar: [../apps/qsdmplus-nvidia-ngc/README.md](../apps/qsdmplus-nvidia-ngc/README.md).

### Submesh profiles (optional)

Set **`[network] submesh_config`** (or **`QSDMPLUS_SUBMESH_CONFIG`**) to a **`config/micropayments.toml`**-style file (see **`config/qsdmplus.toml.example`**). Relative paths resolve next to your main config file. **Multiple profiles** use **`[[submeshes]]`** in TOML or **`submeshes:`** in YAML. When **any** submesh is loaded, **HTTP** **`POST /api/v1/wallet/send`** and **libp2p** ingress use the same rules: **fee + geotag** must match a submesh, and **max_tx_size** applies to the serialized payload. **Mint / token-create** HTTP routes use the **strictest** **max_tx_size** across loaded submeshes. Failures return **422** on the API and **drop** P2P messages before validation. **Interactive submesh CLI** (when stdin is a TTY) **adds or updates** entries in the same in-memory manager as the file seed—both apply together at runtime. **Prometheus** (dashboard **`/api/metrics/prometheus`**) exposes **`qsdmplus_submesh_*`** counters for P2P and API reject reasons.

## Getting Started

> **New to QSDM?** Read the end-to-end operator wiki first:
> [`docs/docs/OPERATOR_GUIDE.md`](docs/docs/OPERATOR_GUIDE.md). It
> answers the three questions every new operator asks — *do I need an
> NVIDIA GPU, do I need a paid NGC plan, and do I have to sync to your
> VPS?* — before you touch a build command.

### Prerequisites

- Go 1.20 or higher
- SQLite3
- Git

### Build and Run

**Windows (PowerShell):**
```powershell
# Build with CGO enabled (full features)
.\scripts\build.ps1

# Run the node
.\qsdmplus.exe
```

**Linux (Ubuntu 24.04+):**
```bash
# Install dependencies
sudo apt install -y build-essential cmake git libssl-dev libsqlite3-dev golang-go

# Clone repository
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM

# Build liboqs (quantum-safe library)
chmod +x scripts/rebuild_liboqs.sh scripts/build.sh scripts/run.sh
./scripts/rebuild_liboqs.sh

# Build QSDM
./scripts/build.sh

# Run the node
./scripts/run.sh
```
<｜tool▁calls▁begin｜><｜tool▁call▁begin｜>
grep

**For production deployment on Ubuntu VPS, see:** [docs/UBUNTU_DEPLOYMENT.md](docs/UBUNTU_DEPLOYMENT.md)

The node will start and initialize libp2p networking. Logs will be written to `qsdmplus.log`.

**Note**: The main `build.ps1` script automatically enables CGO and liboqs for full feature support, including quantum-safe cryptography (ML-DSA-87), SQLite storage, and API server.

## Performance Optimizations

QSDM includes several performance optimizations:

- **Memory Pool Optimization**: 5-10% faster signing through reduced allocations
- **Signature Compression**: 50% size reduction using zstd compression
- **Storage Compression**: 60-70% compression ratio for transaction data
- **Batch Signing**: 10-100x faster for multiple transactions
- **Fast Verification**: 1.76x faster than ECDSA (0.19 ms vs 0.33 ms)

See [docs/PERFORMANCE_BENCHMARK_REPORT.md](docs/PERFORMANCE_BENCHMARK_REPORT.md) for detailed performance metrics.

## Project Structure

- `cmd/qsdmplus/` - Main application entry point
- `pkg/networking/` - libp2p networking setup
- `pkg/consensus/` - Proof-of-Entanglement consensus implementation
- `pkg/storage/` - SQLite storage with Zstandard compression
- `pkg/crypto/` - Quantum-safe cryptography (ML-DSA-87, optimized with memory pooling and compression)
- `config/` - YAML configuration for submesh templates
- `internal/logging/` - Logging setup with rotation and levels

## Comparative Analysis

See [docs/COMPARATIVE_ANALYSIS.md](docs/COMPARATIVE_ANALYSIS.md) for a detailed comparison of QSDM with Blockchain and DAG technologies.

## Visualization of QSDM

Below is a flowchart summarizing how the Quantum-Secure Dynamic Mesh Ledger (QSDM) works across its three development phases:

```mermaid
flowchart TD
    subgraph Phase1["Phase 1: 2D Mesh Launch (Stability, Manual Bootstrapping)"]
        P1_Networking["Networking: libp2p (Go)"]
        P1_Consensus["Consensus: Proof-of-Entanglement (PoE)"]
        P1_Storage["Storage: SQLite + Zstandard compression"]
        P1_Cryptography["Cryptography: CRYSTALS-Dilithium"]
        P1_Submesh["Submesh Templates: YAML configs"]
        P1_Workflow["Key Workflow: Manual submesh rules, 2 parent cell validation, compressed storage"]
    end

    subgraph Phase2["Phase 2: Scalability & Optimization (Throughput, Manual Governance)"]
        P2_DynamicSubmeshes["Dynamic Submeshes: Priority-based routing (Go)"]
        P2_WASM["WASM SDK: TinyGo + WASMEdge"]
        P2_Database["Database: ScyllaDB"]
        P2_Governance["Governance: Snapshot-based voting"]
        P2_Workflow["Key Workflow: Voting on submesh rules, priority routing, WASM wallet integration"]
    end

    subgraph Phase3["Phase 3: 3D Mesh & Self-Healing (Autonomy, Security)"]
        P3_3DMesh["3D Mesh: Rust + CUDA (GTX 3050)"]
        P3_Quarantines["Quarantines: Rule-based isolation (Rust)"]
        P3_Reputation["Reputation System: Staked deposits + penalties"]
        P3_Workflow["Key Workflow: Detection of malicious submeshes, CUDA-accelerated validation, reputation penalties"]
    end

    Phase1 --> Phase2 --> Phase3

    Phase1 --> P1_Networking
    Phase1 --> P1_Consensus
    Phase1 --> P1_Storage
    Phase1 --> P1_Cryptography
    Phase1 --> P1_Submesh
    Phase1 --> P1_Workflow

    Phase2 --> P2_DynamicSubmeshes
    Phase2 --> P2_WASM
    Phase2 --> P2_Database
    Phase2 --> P2_Governance
    Phase2 --> P2_Workflow

    Phase3 --> P3_3DMesh
    Phase3 --> P3_Quarantines
    Phase3 --> P3_Reputation
    Phase3 --> P3_Workflow

    subgraph HardwareOptimization["Hardware Optimization"]
        RAM["RAM: 8GB (Phase 1) → 16GB (Phase 2) → 24GB (Phase 3)"]
        GPU["GPU: Parallel hashing → None → CUDA validation"]
        Storage["Storage: SQLite → ScyllaDB → ScyllaDB + archival"]
    end

    Phase1 --> HardwareOptimization
    Phase2 --> HardwareOptimization
    Phase3 --> HardwareOptimization
```

## Hardware Optimization

| Resource | Phase 1           | Phase 2                 | Phase 3                  |
|----------|-------------------|-------------------------|--------------------------|
| RAM      | 8GB (nodes)       | 16GB (WASM + ScyllaDB)  | 24GB (3D mesh)           |
| GPU      | Parallel hashing  | —                       | CUDA validation          |
| Storage  | SQLite (500GB HDD)| ScyllaDB (800GB HDD)    | ScyllaDB + archival      |

## Key Differences from AI-Driven QSDM

| Feature           | AI Version           | Non-AI Version          |
|-------------------|----------------------|------------------------|
| Submesh Balancing | AI predicts traffic  | Manual routing tables   |
| Attack Detection  | DeepSeek-R1 flags threats | Rule-based thresholds |
| Governance        | AI drafts proposals  | Community Snapshot voting |
| Complexity        | High (ML models)     | Moderate (YAML/configs) |

## Example Use Case

- Phase 1: A developer creates a "micropayments" submesh via YAML, sets low fees.
- Phase 2: Nodes vote to increase block size for this submesh during peak hours.
- Phase 3: Malicious nodes spamming the submesh are isolated via manual voting.

## Advantages

- Simpler Debugging: No black-box AI logic.
- Lower Resource Use: Eliminates GPU-heavy ML workloads.
- Transparency: Rules and thresholds are manually defined.

## Developer 👨‍💻

Developed by [Blackbeard](https://blackbeard.one) | [Ten Titanics](https://tentitanics.com) | [GitHub](https://github.com/blackbeardONE)

© 2023-2025 Blackbeard. All rights reserved.
