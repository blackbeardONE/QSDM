package telemetrycheck

// Baseline returns the static, vendor-known catalog of GPU
// SKUs the validator hard-codes as a floor of plausibility.
// Used by Catalog.LoadBaseline at boot so the checker has
// SOMETHING to compare against even with zero connected
// peer attesters.
//
// Sourced from public NVIDIA spec sheets and the CUDA
// compute-capability table:
//
//   https://developer.nvidia.com/cuda-gpus
//   https://www.nvidia.com/en-us/geforce/graphics-cards/...
//
// Maintenance policy:
//
//   - Add new SKUs as they enter common operator use
//     (e.g. RTX 50-series Blackwell once they're common).
//   - Never delete entries — old SKUs continue to exist
//     in the wild for years, and dropping a baseline
//     entry would flip every legitimate proof from that
//     SKU into "unknown_sku" until someone publishes a
//     peer profile.
//   - Field values reflect the SKU's nominal vendor
//     specs, not what nvidia-smi reports on every
//     operator's box. Real-world drift (e.g. memory
//     reported as 8188 MiB rather than the marketing 8 GB
//     = 8192 MiB) is captured by peer attester profiles,
//     which override the baseline at lookup time.
//
// The baseline is INTENTIONALLY conservative on optional
// fields (PowerMaxW, ClockGraphicsBoostMHz). Better to
// emit "no opinion" than to flag legitimate proofs based
// on a marketing number that doesn't match the actual
// device. Peer profiles fill in the precise values.

import "github.com/blackbeardONE/QSDM/pkg/telemetry"

// Baseline is the canonical built-in SKU catalog. Returns
// fresh slices per call so the caller can mutate without
// poisoning the package-level state.
func Baseline() []telemetry.GPUObservation {
	out := make([]telemetry.GPUObservation, 0, 24)
	out = append(out, baselineRTX30Series()...)
	out = append(out, baselineRTX40Series()...)
	out = append(out, baselineDataCenter()...)
	return out
}

// baselineRTX30Series — Ampere consumer cards (CC 8.6).
// Memory + power values are vendor nominals; operator-
// observed values from peer profiles take precedence.
func baselineRTX30Series() []telemetry.GPUObservation {
	const arch = "ampere"
	const cc = "8.6"
	return []telemetry.GPUObservation{
		{
			Name: "NVIDIA GeForce RTX 3050", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 8192, PCIeGen: 4, PCIeWidth: 8, PowerMaxW: 130,
		},
		{
			Name: "NVIDIA GeForce RTX 3060", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 12288, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 170,
		},
		{
			Name: "NVIDIA GeForce RTX 3060 Ti", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 8192, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 200,
		},
		{
			Name: "NVIDIA GeForce RTX 3070", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 8192, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 220,
		},
		{
			Name: "NVIDIA GeForce RTX 3070 Ti", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 8192, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 290,
		},
		{
			Name: "NVIDIA GeForce RTX 3080", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 10240, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 320,
		},
		{
			Name: "NVIDIA GeForce RTX 3080 Ti", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 12288, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 350,
		},
		{
			Name: "NVIDIA GeForce RTX 3090", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 24576, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 350,
		},
		{
			Name: "NVIDIA GeForce RTX 3090 Ti", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 24576, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 450,
		},
	}
}

// baselineRTX40Series — Ada Lovelace consumer cards (CC 8.9).
func baselineRTX40Series() []telemetry.GPUObservation {
	const arch = "ada-lovelace"
	const cc = "8.9"
	return []telemetry.GPUObservation{
		{
			Name: "NVIDIA GeForce RTX 4060", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 8192, PCIeGen: 4, PCIeWidth: 8, PowerMaxW: 115,
		},
		{
			Name: "NVIDIA GeForce RTX 4060 Ti", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 16384, PCIeGen: 4, PCIeWidth: 8, PowerMaxW: 165,
		},
		{
			Name: "NVIDIA GeForce RTX 4070", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 12288, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 200,
		},
		{
			Name: "NVIDIA GeForce RTX 4070 Ti", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 12288, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 285,
		},
		{
			Name: "NVIDIA GeForce RTX 4070 Ti SUPER", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 16384, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 285,
		},
		{
			Name: "NVIDIA GeForce RTX 4080", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 16384, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 320,
		},
		{
			Name: "NVIDIA GeForce RTX 4080 SUPER", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 16384, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 320,
		},
		{
			Name: "NVIDIA GeForce RTX 4090", Vendor: "NVIDIA",
			Architecture: arch, ComputeCapability: cc,
			MemoryTotalMB: 24576, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 450,
		},
	}
}

// baselineDataCenter — Hopper + Ampere datacenter (A100,
// H100, etc.). Important because tier-aware reward logic
// will eventually treat these very differently from
// consumer cards (CC-v1 attestation lives here).
func baselineDataCenter() []telemetry.GPUObservation {
	return []telemetry.GPUObservation{
		{
			Name: "NVIDIA A100 40GB PCIe", Vendor: "NVIDIA",
			Architecture: "ampere", ComputeCapability: "8.0",
			MemoryTotalMB: 40960, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 250,
			ECCSupported: true,
		},
		{
			Name: "NVIDIA A100 80GB PCIe", Vendor: "NVIDIA",
			Architecture: "ampere", ComputeCapability: "8.0",
			MemoryTotalMB: 81920, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 300,
			ECCSupported: true,
		},
		{
			Name: "NVIDIA A100-SXM4-80GB", Vendor: "NVIDIA",
			Architecture: "ampere", ComputeCapability: "8.0",
			MemoryTotalMB: 81920, PCIeGen: 4, PCIeWidth: 16, PowerMaxW: 400,
			ECCSupported: true,
		},
		{
			Name: "NVIDIA H100 80GB HBM3", Vendor: "NVIDIA",
			Architecture: "hopper", ComputeCapability: "9.0",
			MemoryTotalMB: 81920, PCIeGen: 5, PCIeWidth: 16, PowerMaxW: 350,
			ECCSupported: true,
		},
		{
			Name: "NVIDIA H100 PCIe", Vendor: "NVIDIA",
			Architecture: "hopper", ComputeCapability: "9.0",
			MemoryTotalMB: 81920, PCIeGen: 5, PCIeWidth: 16, PowerMaxW: 350,
			ECCSupported: true,
		},
		{
			Name: "NVIDIA H100", Vendor: "NVIDIA",
			Architecture: "hopper", ComputeCapability: "9.0",
			MemoryTotalMB: 81920, PCIeGen: 5, PCIeWidth: 16, PowerMaxW: 700,
			ECCSupported: true,
		},
	}
}
