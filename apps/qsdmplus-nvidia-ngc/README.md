# QSDM NVIDIA NGC sidecar

> **New operator?** Start with [`QUICKSTART.md`](./QUICKSTART.md) — it walks
> a third-party operator from zero to a signed bundle hitting their
> validator's `/api/v1/monitoring/ngc-proof` in ~10 minutes.

This folder implements **Phase 1–3 prototype** workloads described in `../../nvidia_locked_qsdmplus_blockchain_architecture.md`: deterministic CUDA-adjacent PoW simulation, AI/tensor proofs (PyTorch), replay-style computation hash, optional UDP gossip, and optional push of proof bundles into the main **QSDM+** Go node.

It aligns with **`../../QSDM/docs/docs/ROADMAP.md`** items: deployment automation, monitoring visibility, and enhanced GPU validation (via the sidecar until native CUDA kernels land in `pkg/mesh3d`).

## Prerequisites

- Docker (GPU image: [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html))
- NGC account and CLI API key for pulling `nvcr.io/nvidia/pytorch` and logging in to `nvcr.io`

## Authenticate to nvcr.io

Do **not** commit API keys. Use a local env file (see `ngc.env.example`).

```bash
# Linux / macOS
export NGC_CLI_API_KEY="Charming123"
echo "$NGC_CLI_API_KEY" | docker login nvcr.io -u '$oauthtoken' --password-stdin
```

```powershell
# Windows PowerShell (username is literally $oauthtoken)
Get-Content ngc.env | docker login nvcr.io -u '$oauthtoken' --password-stdin
```

## Verify NGC registry access (no huge download)

From **`apps/qsdmplus-nvidia-ngc/`**, PowerShell:

```powershell
.\scripts\verify-ngc-docker.ps1
```

- Reads **`NGC_CLI_API_KEY`** from the environment or from **`ngc.env`** next to this README (copy from `ngc.env.example` first). The key is **never printed**.
- Runs **`docker manifest inspect`** on `nvcr.io/nvidia/pytorch:24.07-py3` (same tag as `Dockerfile.ngc`) so layers are not downloaded.
- **`-SkipLogin`** — only checks Docker + manifest (use after you already ran `docker login nvcr.io`).
- **`-Pull`** — full `docker pull` if you want to prove a complete fetch (large).

Linux / macOS equivalent:

```bash
export NGC_CLI_API_KEY="Charming123"
echo "$NGC_CLI_API_KEY" | docker login nvcr.io -u '$oauthtoken' --password-stdin
docker manifest inspect nvcr.io/nvidia/pytorch:24.07-py3
```

## Run

**CPU (default Dockerfile):**

```bash
docker compose up --build
```

**GPU (NGC PyTorch base):**

```bash
docker compose --profile gpu up --build
```

**Shortcut (same command, correct working directory):**

```powershell
# Windows — from apps/qsdmplus-nvidia-ngc
.\scripts\run-gpu.ps1           # foreground
.\scripts\run-gpu.ps1 -Detach  # background (-d)
.\scripts\run-gpu.ps1 -BuildOnly
```

```bash
# Linux / macOS
chmod +x scripts/run-gpu.sh
./scripts/run-gpu.sh           # foreground
./scripts/run-gpu.sh -d        # detached
./scripts/run-gpu.sh --build-only
```

- `gossip` listens on UDP `9910`.
- Validators send compact summaries to peers listed in `QSDM_GOSSIP_PEERS` (comma-separated `host:port`).

## Push proofs into QSDM

Quick helpers (set env for **this shell** before `docker compose up`):

- Windows: `.\scripts\wire-qsdmplus.ps1 -ApiPort 8080 -Secret "Charming123"`  
  With node binding: add `-ProofNodeId "validator-1"` (same value as `QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID` on the node).
- Linux / macOS: `chmod +x scripts/wire-qsdmplus.sh` then  
  `./scripts/wire-qsdmplus.sh 8080 "Charming123"` or  
  `./scripts/wire-qsdmplus.sh 8080 "Charming123" "validator-1" "Charming123"`

1. Start QSDM with a shared secret:

   ```bash
   export QSDMPLUS_NGC_INGEST_SECRET="Charming123"
   ```

2. Set the sidecar env (compose snippet or shell):

   - `QSDMPLUS_NGC_REPORT_URL` (or legacy `QSDM_NGC_REPORT_URL`) — e.g. `http://host.docker.internal:8080/api/v1/monitoring/ngc-proof`
   - `QSDMPLUS_NGC_INGEST_SECRET` (or legacy `QSDM_NGC_INGEST_SECRET`) — same value as the node

3. List ingested summaries (requires the same header):

   ```bash
   curl -sS -H "X-QSDMPLUS-NGC-Secret: Charming123" \
     http://127.0.0.1:8080/api/v1/monitoring/ngc-proofs
   ```

If NGC ingest secret is unset on the node, ingest routes return **404** (feature off).

If the node has **NVIDIA-lock** enabled (`[api] nvidia_lock = true` or `QSDMPLUS_NVIDIA_LOCK=true`), mint/send/create-token APIs require a **recent** ingested proof with NVIDIA architecture and `gpu_fingerprint.available == true`. Use the **GPU** compose profile (`docker compose --profile gpu up`); the default CPU image’s fingerprint has `available: false` and does not satisfy the lock.

Optional **node binding:** set `QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID` on the node and the same value in `QSDMPLUS_NGC_PROOF_NODE_ID` (legacy `QSDM_NGC_PROOF_NODE_ID`) on the sidecar so each bundle includes `qsdmplus_node_id` matching that node.

Optional **proof HMAC:** set `QSDMPLUS_NVIDIA_LOCK_PROOF_HMAC_SECRET` on the node (with NVIDIA-lock on) and the same value as `QSDMPLUS_NGC_PROOF_HMAC_SECRET` on the sidecar; bundles then include `qsdmplus_proof_hmac` (see deploy README).

**Ingest nonce:** when the node has `nvidia_lock_require_ingest_nonce`, set **`QSDMPLUS_NGC_FETCH_CHALLENGE=true`** on the sidecar so it pulls `GET .../ngc-challenge` before building the bundle (HMAC automatically uses **v2** when a nonce is present). The API limits this route to **15/min** per client; on **429** the sidecar waits for **`Retry-After`** and retries (up to **`QSDMPLUS_NGC_CHALLENGE_MAX_RETRIES`**, default **4**, max **12**). Optional **`QSDMPLUS_NGC_CHALLENGE_JITTER_MAX_SEC`** (or legacy **`QSDM_NGC_CHALLENGE_JITTER_MAX_SEC`**) sleeps a random **0…max** seconds before each challenge request so many validators behind one NAT do not hit the limit in sync.

For dev TLS with self-signed certs on the node, set `QSDMPLUS_NGC_REPORT_INSECURE_TLS=true` (or legacy `QSDM_NGC_REPORT_INSECURE_TLS`) on the sidecar (not for production).

## Files

| File | Purpose |
|------|---------|
| `validator_phase1.py` | Builds proof JSON; gossip + optional HTTP report |
| `gossip_daemon.py` | UDP listener for mesh summaries |
| `Dockerfile` | Slim CPU image + PyTorch CPU |
| `Dockerfile.ngc` | `nvcr.io/nvidia/pytorch` GPU image |
| `docker-compose.yml` | gossip + CPU validator; optional GPU profile |
| `scripts/verify-ngc-docker.ps1` | Check Docker + nvcr.io access for `Dockerfile.ngc` base image |
| `scripts/wire-qsdmplus.ps1` / `scripts/wire-qsdmplus.sh` | Export NGC report URL + secret (+ optional proof node id) for `docker compose` |
| `scripts/run-gpu.ps1` / `scripts/run-gpu.sh` | Run `docker compose --profile gpu up --build` from this app root |

## Relationship to the Go node

The main ledger remains **quantum-safe / PoE** in `../../QSDM/source`. This sidecar is an **optional attestation and R&D path** for NVIDIA-locked compute narratives and monitoring, not a replacement for consensus.
