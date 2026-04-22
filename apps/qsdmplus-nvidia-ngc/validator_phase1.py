#!/usr/bin/env python3
"""
NGC validator: Phase 1 PoW sim, Phase 2 AI proof, Phase 3 FP16 (+ optional FP8) tensor work,
replay workload (Phase 2 roadmap), optional UDP gossip (mesh §3.1).
"""
from __future__ import annotations

import hashlib
import hmac
import json
import os
import random
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone


def _run(cmd: list[str]) -> tuple[int, str, str]:
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        return p.returncode, p.stdout or "", p.stderr or ""
    except (FileNotFoundError, subprocess.TimeoutExpired) as e:
        return -1, "", str(e)


def gpu_fingerprint() -> dict:
    code, out, err = _run(
        [
            "nvidia-smi",
            "--query-gpu=index,name,driver_version,compute_cap",
            "--format=csv,noheader",
        ]
    )
    if code != 0:
        return {
            "available": False,
            "error": (err or out or "nvidia-smi not available").strip(),
        }
    lines = [ln.strip() for ln in out.strip().splitlines() if ln.strip()]
    devices = []
    for i, ln in enumerate(lines):
        parts = [p.strip() for p in ln.split(",")]
        if len(parts) >= 4:
            devices.append(
                {
                    "index": parts[0],
                    "name": parts[1],
                    "driver_version": parts[2],
                    "compute_capability": parts[3],
                }
            )
        else:
            devices.append({"raw": ln, "index": str(i)})
    return {"available": True, "devices": devices}


def simulated_cuda_pow(seed: bytes, iterations: int = 50_000) -> str:
    h = hashlib.sha256(seed)
    for _ in range(iterations):
        h = hashlib.sha256(h.digest())
    return h.hexdigest()


def tensor_stub_proof(seed: bytes) -> str:
    x = int.from_bytes(hashlib.sha256(seed).digest()[:8], "little") & 0xFFFFFFFF
    acc = x
    for i in range(10_000):
        acc = (acc * 1103515245 + 12345 + i) & 0xFFFFFFFF
    return hashlib.sha256(acc.to_bytes(8, "little")).hexdigest()


def _seed_u64(seed: bytes) -> int:
    return int.from_bytes(hashlib.sha256(seed).digest()[:8], "little", signed=False)


def replay_computation_hash(seed: bytes, ticks: int = 500) -> str:
    """Deterministic multi-tick simulation digest (architecture Phase 2 replay workloads)."""
    state = bytearray(hashlib.sha256(seed + b"replay").digest())
    for t in range(ticks):
        n = len(state)
        nxt = bytearray(n)
        for i in range(n):
            nxt[i] = (state[i] + state[(i + 1) % n] + (t & 0xFF)) & 0xFF
        state = nxt
    return hashlib.sha256(bytes(state)).hexdigest()


def deterministic_ai_proof(seed: bytes) -> dict:
    try:
        import torch

        s = _seed_u64(seed + b"ai")
        g = torch.Generator()
        g.manual_seed(s % (2**31 - 1))
        x = torch.randn(128, 64, generator=g, dtype=torch.float32)
        w = torch.randn(64, 32, generator=g, dtype=torch.float32)
        h = torch.tanh(x @ w)
        y = torch.softmax(h.sum(dim=0), dim=0)
        blob = y.detach().numpy().tobytes()
        return {
            "mode": "torch_cpu_deterministic",
            "ai_computation_hash": hashlib.sha256(blob).hexdigest(),
        }
    except Exception as e:
        vec = hashlib.sha256(seed + b"ai-fallback").digest()
        return {
            "mode": "hash_fallback",
            "ai_computation_hash": hashlib.sha256(vec).hexdigest(),
            "error": str(e),
        }


def _fp8_cuda_subproof(seed: bytes) -> str | None:
    try:
        import torch

        if not torch.cuda.is_available():
            return None
        dt = getattr(torch, "float8_e4m3fn", None)
        if dt is None:
            return None
        s = _seed_u64(seed + b"fp8")
        dev = torch.device("cuda")
        a = torch.ones(64, 64, device=dev, dtype=torch.float16) * 0.25
        b = torch.roll(a, int(s % 8), dims=1)
        a8 = a.to(dt)
        b8 = b.to(dt)
        c = torch.matmul(a8.float(), b8.float())
        torch.cuda.synchronize()
        sample = c[0:16, 0:16].detach().half().cpu().numpy().tobytes()
        return hashlib.sha256(sample).hexdigest()
    except Exception:
        return None


def tensor_core_proof(seed: bytes) -> dict:
    try:
        import torch

        if not torch.cuda.is_available():
            out = {
                "mode": "stub_no_cuda",
                "tensor_operation_proof": tensor_stub_proof(seed),
            }
            return out
        s = _seed_u64(seed + b"tensor")
        torch.manual_seed(s % (2**31 - 1))
        dev = torch.device("cuda")
        n = 512
        a = torch.arange(n * n, dtype=torch.float32, device=dev).reshape(n, n)
        a = (a / 10000.0).to(torch.float16)
        b = torch.roll(a, shifts=int(s % 64), dims=1)
        c = torch.matmul(a, b)
        torch.cuda.synchronize()
        sample = c[0:128, 0:128].detach().float().cpu().numpy().tobytes()
        out = {
            "mode": "fp16_cuda_matmul",
            "tensor_operation_proof": hashlib.sha256(sample).hexdigest(),
            "device": torch.cuda.get_device_name(0),
        }
        fp8h = _fp8_cuda_subproof(seed)
        if fp8h:
            out["fp8_e4m3fn_subproof"] = fp8h
        return out
    except Exception as e:
        return {
            "mode": "stub_error",
            "tensor_operation_proof": tensor_stub_proof(seed),
            "error": str(e),
        }


def _env_preferred(primary: str, legacy: str) -> str:
    return os.environ.get(primary, "").strip() or os.environ.get(legacy, "").strip()


def _challenge_jitter_sleep() -> None:
    """Spread GET /ngc-challenge calls when many validators share one NAT (15/min per client)."""
    raw = _env_preferred("QSDMPLUS_NGC_CHALLENGE_JITTER_MAX_SEC", "QSDM_NGC_CHALLENGE_JITTER_MAX_SEC")
    if not raw:
        return
    try:
        mx = float(raw)
    except ValueError:
        return
    if mx <= 0:
        return
    time.sleep(random.uniform(0.0, mx))


def _retry_after_seconds(http_err: urllib.error.HTTPError) -> float:
    ra = http_err.headers.get("Retry-After")
    if ra:
        try:
            return min(max(float(ra), 1.0), 120.0)
        except ValueError:
            pass
    return 65.0


def fetch_ingest_nonce() -> str:
    """GET /ngc-challenge when QSDMPLUS_NGC_FETCH_CHALLENGE=true (node must have nvidia_lock_require_ingest_nonce)."""
    if _env_preferred("QSDMPLUS_NGC_FETCH_CHALLENGE", "QSDM_NGC_FETCH_CHALLENGE").lower() not in ("1", "true", "yes"):
        return ""
    url = _env_preferred("QSDMPLUS_NGC_CHALLENGE_URL", "QSDM_NGC_CHALLENGE_URL")
    if not url:
        report = _env_preferred("QSDMPLUS_NGC_REPORT_URL", "QSDM_NGC_REPORT_URL")
        if report and "/ngc-proof" in report:
            url = report.replace("/ngc-proof", "/ngc-challenge", 1)
        else:
            return ""
    secret = _env_preferred("QSDMPLUS_NGC_INGEST_SECRET", "QSDM_NGC_INGEST_SECRET")
    if not secret:
        return ""
    max_retries_raw = _env_preferred("QSDMPLUS_NGC_CHALLENGE_MAX_RETRIES", "QSDM_NGC_CHALLENGE_MAX_RETRIES")
    try:
        max_retries = max(1, min(12, int(max_retries_raw or "4")))
    except ValueError:
        max_retries = 4
    ctx = None
    if _env_preferred("QSDMPLUS_NGC_REPORT_INSECURE_TLS", "QSDM_NGC_REPORT_INSECURE_TLS").lower() in (
        "1",
        "true",
        "yes",
    ):
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    _challenge_jitter_sleep()
    for attempt in range(max_retries):
        req = urllib.request.Request(url, method="GET")
        req.add_header("X-QSDMPLUS-NGC-Secret", secret)
        req.add_header("X-QSDM-NGC-Secret", secret)
        try:
            with urllib.request.urlopen(req, timeout=15, context=ctx) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            return (data.get("qsdmplus_ingest_nonce") or "").strip()
        except urllib.error.HTTPError as e:
            if e.code == 429 and attempt + 1 < max_retries:
                time.sleep(_retry_after_seconds(e))
                continue
            return ""
        except (urllib.error.URLError, OSError, ValueError, json.JSONDecodeError):
            return ""
    return ""


def attach_proof_hmac(block: dict) -> None:
    """Set qsdmplus_proof_hmac when QSDMPLUS_NGC_PROOF_HMAC_SECRET (or legacy) matches node's NVIDIA-lock HMAC secret (v2 if nonce present)."""
    secret = _env_preferred("QSDMPLUS_NGC_PROOF_HMAC_SECRET", "QSDM_NGC_PROOF_HMAC_SECRET")
    if not secret:
        return
    node = block.get("qsdmplus_node_id") or ""
    cuda = block.get("cuda_proof_hash") or ""
    ts = block.get("timestamp_utc") or ""
    nonce = (block.get("qsdmplus_ingest_nonce") or "").strip()
    if nonce:
        msg = f"v2\n{node}\n{cuda}\n{ts}\n{nonce}\n".encode("utf-8")
    else:
        msg = f"v1\n{node}\n{cuda}\n{ts}\n".encode("utf-8")
    block["qsdmplus_proof_hmac"] = hmac.new(secret.encode("utf-8"), msg, hashlib.sha256).hexdigest()


def maybe_report_to_qsdm(block: dict) -> None:
    """POST proof bundle to QSDM+ API (branded env: QSDMPLUS_*; legacy QSDM_* still supported)."""
    url = _env_preferred("QSDMPLUS_NGC_REPORT_URL", "QSDM_NGC_REPORT_URL")
    if not url:
        return
    secret = _env_preferred("QSDMPLUS_NGC_INGEST_SECRET", "QSDM_NGC_INGEST_SECRET")
    if not secret:
        return
    payload = json.dumps(block).encode("utf-8")
    req = urllib.request.Request(url, data=payload, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-QSDMPLUS-NGC-Secret", secret)
    req.add_header("X-QSDM-NGC-Secret", secret)
    ctx = None
    if _env_preferred("QSDMPLUS_NGC_REPORT_INSECURE_TLS", "QSDM_NGC_REPORT_INSECURE_TLS").lower() in (
        "1",
        "true",
        "yes",
    ):
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    try:
        with urllib.request.urlopen(req, timeout=20, context=ctx) as resp:
            _ = resp.read()
    except (urllib.error.URLError, OSError) as e:
        print(json.dumps({"ngc_report_error": str(e)}), file=sys.stderr, flush=True)


def gossip_block_summary(block: dict) -> None:
    peers = os.environ.get("QSDM_GOSSIP_PEERS", "").strip()
    if not peers:
        return
    delay = float(os.environ.get("QSDM_GOSSIP_DELAY_SEC", "0.4"))
    time.sleep(delay)
    summary = {
        "v": 1,
        "cuda_proof_hash": block["cuda_proof_hash"],
        "ai": block["ai_proof"].get("ai_computation_hash"),
        "tensor": block["tensor_proof"].get("tensor_operation_proof"),
        "replay": block.get("replay_computation_hash"),
        "ts": block["timestamp_utc"],
    }
    raw = json.dumps(summary, separators=(",", ":")).encode("utf-8")
    if len(raw) > 1200:
        raw = raw[:1200]
    for peer in peers.split(","):
        peer = peer.strip()
        if not peer:
            continue
        host, _, ps = peer.partition(":")
        port = int(ps or "9910")
        try:
            sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            sock.settimeout(2.0)
            sock.sendto(raw, (host, port))
            sock.close()
        except OSError:
            pass


def build_block() -> dict:
    t0 = time.perf_counter()
    seed = os.environ.get("QSDM_POW_SEED", "qsdm-ngc-phase1").encode()
    cuda_hash = simulated_cuda_pow(seed)
    ai = deterministic_ai_proof(seed)
    tensor = tensor_core_proof(seed)
    replay = replay_computation_hash(seed)

    block = {
        "architecture": "NVIDIA-Locked QSDM+ NGC prototype (phases 1-4 sketch)",
        "timestamp_utc": datetime.now(timezone.utc).isoformat(),
        "cuda_proof_hash": cuda_hash,
        "replay_computation_hash": replay,
        "ai_proof": ai,
        "tensor_proof": tensor,
        "gpu_fingerprint": gpu_fingerprint(),
        "execution_seconds": round(time.perf_counter() - t0, 6),
        "env": {
            "CUDA_VERSION": os.environ.get("CUDA_VERSION"),
            "NVIDIA_VISIBLE_DEVICES": os.environ.get("NVIDIA_VISIBLE_DEVICES"),
        },
    }
    proof_node = _env_preferred("QSDMPLUS_NGC_PROOF_NODE_ID", "QSDM_NGC_PROOF_NODE_ID")
    if proof_node:
        block["qsdmplus_node_id"] = proof_node
    ingest_nonce = fetch_ingest_nonce()
    if ingest_nonce:
        block["qsdmplus_ingest_nonce"] = ingest_nonce
    attach_proof_hmac(block)
    return block


def main() -> int:
    block = build_block()
    gossip_block_summary(block)
    maybe_report_to_qsdm(block)
    print(json.dumps(block, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
