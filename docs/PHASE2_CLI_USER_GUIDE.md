# Phase 2 CLI User Guide - QSDM

## Overview
This guide provides detailed instructions and examples for using the Phase 2 CLI tools in the Quantum-Secure Dynamic Mesh Ledger (QSDM) project. Phase 2 focuses on scalability and manual governance features, including dynamic submesh management and governance voting.

---

## Dynamic Submesh CLI (submeshCLI)

### Purpose
Manage dynamic submeshes at runtime by adding, updating, removing, and listing submesh configurations.

### Commands

- **Add or Update Submesh**

```bash
submeshCLI add --name <submesh_name> --fee <fee_threshold> --priority <priority_level> --geotags <comma_separated_tags>
```

Example:

```bash
submeshCLI add --name fastlane --fee 0.01 --priority 10 --geotags US,EU
```

- **Remove Submesh**

```bash
submeshCLI remove --name <submesh_name>
```

Example:

```bash
submeshCLI remove --name slowlane
```

- **List Submeshes**

```bash
submeshCLI list
```

### Notes
- Fee threshold is a decimal value representing the minimum fee for the submesh.
- Priority level is an integer where higher values indicate higher priority.
- GeoTags are comma-separated geographic tags for routing.

---

## Governance CLI (governanceCLI)

### Purpose
Participate in governance voting by casting votes, viewing results, and managing voting sessions.

### Commands

- **Create Voting Snapshot**

```bash
governanceCLI create --name <snapshot_name> --duration <duration_in_minutes>
```

Example:

```bash
governanceCLI create --name test1 --duration 60
```

- **Cast Vote**

```bash
governanceCLI vote --snapshot <snapshot_name> --token <token_id> --weight <vote_weight>
```

Example:

```bash
governanceCLI vote --snapshot test1 --token token123 --weight 10
```

- **View Results**

```bash
governanceCLI results --snapshot <snapshot_name>
```

Example:

```bash
governanceCLI results --snapshot test1
```

### Notes
- Votes are weighted by token holdings to ensure fair governance.
- Voting sessions expire after the specified duration.

---

## WASM SDK Usage

### Overview
Phase 2 integrates WASM runtime using Wasmer Go SDK to load and execute WASM modules for wallet and validator logic.

### Loading WASM Modules

- Load modules dynamically for wallet and validator operations.
- Call exported functions such as `validate` for transaction validation.

### Example

```go
// Load WASM module
module, err := wasmer.NewModule(store, wasmBytes)
if err != nil {
    log.Fatal(err)
}

// Instantiate module
instance, err := wasmer.NewInstance(module, importObject)
if err != nil {
    log.Fatal(err)
}

// Call exported function
result, err := instance.Exports.GetFunction("validate")
if err != nil {
    log.Fatal(err)
}
```

---

## Performance Tips

- Use SSD storage for faster I/O operations.
- Enable SQLite WAL mode and performance pragmas as configured.
- Utilize CUDA acceleration for 3D mesh validation if available.
- Monitor system resources during high throughput periods.

---

## Troubleshooting

- Ensure CLI tools are run with appropriate permissions.
- Check logs for errors and warnings.
- Verify WASM modules are compatible with the runtime.

---

*Document maintained by QSDM Development Team*
