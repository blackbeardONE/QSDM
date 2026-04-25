# QSDM Project Migration Guide

**Date:** December 25, 2024  
**Status:** Ready for Migration ✅

---

## Overview

This document provides instructions for migrating the QSDM project to a new development server. The project has been cleaned up and databases have been exported.

---

## Pre-Migration Checklist

- [x] Databases exported to `database_exports/` directory
- [x] Build artifacts removed (executables, DLLs, build directories)
- [x] Dependencies removed (node_modules)
- [x] Log files removed
- [x] Test files removed
- [x] Production databases preserved (qsdm.db, transactions.db)

---

## Files to Transfer

### 1. Source Code
- All `.go` files in `cmd/`, `pkg/`, `internal/`
- Configuration files in `config/`
- Scripts in `scripts/`
- Documentation in `docs/`

### 2. Configuration Files
- `go.mod` and `go.sum` (Go dependencies)
- `package.json` and `package-lock.json` (Node.js dependencies)
- `Dockerfile` and `docker-compose.yml`
- Configuration examples in `config/`

### 3. Database Exports
- **Location:** `database_exports/` directory
- **Files:**
  - `qsdm_YYYYMMDD_HHMMSS.sql` - Main database export
  - `transactions_YYYYMMDD_HHMMSS.sql` - Transactions database export
  - `qsdm_YYYYMMDD_HHMMSS.db-wal` - WAL file (if present)
  - `qsdm_YYYYMMDD_HHMMSS.db-shm` - SHM file (if present)
  - `README.txt` - Import instructions

### 4. Production Databases (Optional)
- `qsdm.db` - Main production database (if you want to copy directly)
- `transactions.db` - Transactions database (if you want to copy directly)
- `qsdm.db-wal` and `qsdm.db-shm` - WAL/SHM files (if present)

**Note:** You can either use the SQL exports or copy the binary database files directly.

---

## What Was Removed (Don't Transfer)

The following items were removed during cleanup and should NOT be transferred:

- ❌ `qsdm.exe` and other executables
- ❌ DLL files (`.dll`)
- ❌ `node_modules/` directory
- ❌ `liboqs_build/` and `liboqs_install/` directories
- ❌ `wasm_module/target/` and `wasmer-go-patched/target/` directories
- ❌ Log files (`.log`)
- ❌ Test databases
- ❌ Temporary files (`.tmp`, `.bak`)

---

## Migration Steps

### Step 1: Transfer Files

Transfer the following to the new server:
1. All source code directories
2. Configuration files
3. `database_exports/` directory
4. Production database files (if using binary copy method)

### Step 2: Install Dependencies

#### Go Dependencies
```bash
go mod download
```

#### Node.js Dependencies
```bash
npm install
```

### Step 3: Import Databases

#### Option A: Using SQL Dumps (Recommended)

```bash
# Import main database
sqlite3 qsdm.db < database_exports/qsdm_YYYYMMDD_HHMMSS.sql

# Import transactions database
sqlite3 transactions.db < database_exports/transactions_YYYYMMDD_HHMMSS.sql

# Set permissions
chmod 644 qsdm.db transactions.db
```

#### Option B: Using Binary Copy

```bash
# Copy database files
cp database_exports/qsdm_YYYYMMDD_HHMMSS.db qsdm.db
cp database_exports/transactions_YYYYMMDD_HHMMSS.db transactions.db

# Copy WAL/SHM files if present
cp database_exports/qsdm_YYYYMMDD_HHMMSS.db-wal qsdm.db-wal
cp database_exports/qsdm_YYYYMMDD_HHMMSS.db-shm qsdm.db-shm

# Set permissions
chmod 644 qsdm.db transactions.db qsdm.db-wal qsdm.db-shm
```

### Step 4: Verify Database Import

```bash
# Check transaction count
sqlite3 qsdm.db "SELECT COUNT(*) FROM transactions;"

# Check balances
sqlite3 qsdm.db "SELECT COUNT(*) FROM balances;"

# List tables
sqlite3 qsdm.db ".tables"
```

### Step 5: Build the Project

#### Build QSDM
```bash
go build -o qsdm ./cmd/qsdm
```

#### Build WASM Modules (if needed)
```bash
cd wasm_module
cargo build --release --target wasm32-wasi
cd ..
```

### Step 6: Configure Environment

1. Review configuration files in `config/`
2. Copy example config if needed:
   ```bash
   cp config/qsdm.yaml.example config/qsdm.yaml
   ```
3. Update configuration with server-specific settings

### Step 7: Test the Installation

```bash
# Run tests
go test ./...

# Start the service (if configured)
./qsdm
```

---

## Post-Migration Checklist

- [ ] All source files transferred
- [ ] Dependencies installed (`go mod download`, `npm install`)
- [ ] Databases imported and verified
- [ ] Project builds successfully
- [ ] Configuration files updated
- [ ] Service starts without errors
- [ ] Database connections working
- [ ] Tests pass

---

## Troubleshooting

### Database Import Issues

**Problem:** SQLite version mismatch
```bash
# Check SQLite version
sqlite3 --version

# If version is too old, upgrade SQLite or use binary copy method
```

**Problem:** Database locked
```bash
# Make sure QSDM service is stopped
# Check for WAL files and copy them along with the database
```

### Build Issues

**Problem:** Missing CGO dependencies
- Ensure OpenSSL libraries are installed
- Check that CGO is enabled: `CGO_ENABLED=1`

**Problem:** Missing Rust/Cargo
- Install Rust toolchain: `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh`
- Install WASM target: `rustup target add wasm32-wasi`

### Runtime Issues

**Problem:** Missing DLLs (Windows)
- DLLs need to be in PATH or executable directory
- Rebuild the project to generate new DLLs

**Problem:** Permission errors
- Ensure database files have correct permissions (644)
- Check that the service user has read/write access

---

## Database Schema

The main database (`qsdm.db`) contains:

- **transactions** table: Transaction data (encrypted and compressed)
- **balances** table: Address balances

Key indexes:
- `idx_tx_id` on `transactions(tx_id)`
- `idx_sender` on `transactions(sender)`
- `idx_recipient` on `transactions(recipient)`

---

## Additional Resources

- Database export README: `database_exports/README.txt`
- Project documentation: `docs/`
- Configuration examples: `config/`
- Deployment guide: `docs/PRODUCTION_DEPLOYMENT.md`

---

## Support

If you encounter issues during migration:

1. Check the troubleshooting section above
2. Review the logs (if service was started)
3. Verify database integrity: `sqlite3 qsdm.db "PRAGMA integrity_check;"`
4. Check configuration files for errors

---

**Migration Status:** ✅ Ready  
**Last Updated:** December 25, 2024

