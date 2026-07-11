# Apps (non-core)

Everything here is **optional** relative to the **`QSDM/`** node (the QSDM ledger). The node builds and runs without these folders.

| App | Role |
|-----|------|
| **`qsdm-hive/`** | Desktop client (Windows/Linux): CELL wallets, Task Studio, NVIDIA mining, Mother Hive edge pools, Sky Fang linking. Public downloads at [qsdm.tech/download.html](https://qsdm.tech/download.html). |
| **`qsdm-edge-agent/`** | Edge Agent, Relay, and Edge Control utilities for pooled CPU/GPU/RAM work. |
| **`qsdm-tray-monitor/`** | Windows tray health monitor for the local home validator stack. |
| **`qsdm-nvidia-ngc/`** | Optional Docker NGC GPU attestation sidecar; pairs with `QSDM_NGC_INGEST_SECRET` on the node. |
| **`qsdm-landing/`** | Legacy marketing stub. **Production site is `QSDM/deploy/landing/`** (served at qsdm.tech). |

To promote an app to its own repository later, copy one folder and add a client SDK from `QSDM/source/sdk/`.
