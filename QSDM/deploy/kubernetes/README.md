# QSDM Kubernetes manifests

This directory contains the reference Kubernetes manifests for deploying a
QSDM cluster. As of the Major Update (see
[`REBRAND_NOTES.md`](../../docs/docs/REBRAND_NOTES.md)), the deployment model
is **explicitly split into two node roles**:

| Role      | Manifest                          | Hardware | Runs                                    |
|-----------|-----------------------------------|----------|-----------------------------------------|
| Validator | [`validator-statefulset.yaml`](./validator-statefulset.yaml) | CPU-only | BFT + PoE consensus, transaction fees   |
| Miner     | [`miner-daemonset.yaml`](./miner-daemonset.yaml)             | GPU      | Additive Proof-of-Work emission for Cell |

> The original `statefulset.yaml` / `deployment.yaml` predate the Major Update
> and deploy the combined legacy image. They are retained for operators who
> have not yet split their fleet, but new deployments SHOULD use the two
> role-scoped manifests above.

## Scheduling contract

The validator manifest requires nodes labelled `qsdm.tech/node-class=cpu` and
explicitly refuses to schedule on any node advertising an NVIDIA GPU product.
The miner manifest requires nodes labelled `qsdm.tech/node-class=gpu` and
tolerates the standard `nvidia.com/gpu=true:NoSchedule` taint.

Cluster administrators are responsible for labelling their node pools
appropriately. A typical setup:

```
kubectl label node vps-validator-01 qsdm.tech/node-class=cpu
kubectl label node gpu-worker-01    qsdm.tech/node-class=gpu
kubectl taint node  gpu-worker-01   nvidia.com/gpu=true:NoSchedule
```

## Images

- `qsdm/validator:latest` — built from `QSDM/Dockerfile.validator`
  (Alpine, validator-only profile, no CUDA).
- `qsdm/miner:latest` — built from `QSDM/Dockerfile.miner` (CUDA runtime,
  full build profile).

Both images default the `QSDM_NODE_ROLE` and `QSDM_MINING_ENABLED`
environment variables so the startup guard in
`pkg/mining/roleguard` matches the declared role.
