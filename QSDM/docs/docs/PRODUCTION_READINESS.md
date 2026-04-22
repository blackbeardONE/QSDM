# Production Readiness Guide

**Last Updated:** December 2024  
**Status:** Production-Ready Features Implemented ✅

---

## Overview

QSDM now includes production-ready features for configuration management and enhanced logging. This guide covers the new capabilities and how to use them.

---

## Configuration Management

### Configuration File Support

QSDM now supports configuration files in TOML or YAML format, with environment variable override.

**Priority Order:**
1. **Environment Variables** (highest priority)
2. **Config File** (`qsdm.toml` or `qsdm.yaml`)
3. **Defaults** (lowest priority)

### Quick Start

1. **Copy example config:**
   ```powershell
   Copy-Item config/qsdmplus.toml.example qsdm.toml
   # Or for YAML:
   Copy-Item config/qsdmplus.yaml.example qsdm.yaml
   ```

2. **Customize configuration:**
   Edit `qsdm.toml` or `qsdm.yaml` with your settings

3. **Override with environment variables (optional):**
   ```powershell
   $env:LOG_LEVEL = "DEBUG"
   $env:NETWORK_PORT = "5001"
   ```

### Configuration File Format

#### TOML Example (`qsdm.toml`)

```toml
[network]
port = 4001
bootstrap_peers = ["127.0.0.1:4001"]

[storage]
type = "sqlite"
sqlite_path = "qsdmplus.db"

[monitoring]
dashboard_port = 8081
log_viewer_port = 8080
log_file = "qsdmplus.log"
log_level = "INFO"  # DEBUG, INFO, WARN, ERROR

[api]
port = 8443
enable_tls = true
```

#### YAML Example (`qsdm.yaml`)

```yaml
network:
  port: 4001
  bootstrap_peers: ["127.0.0.1:4001"]

storage:
  type: "sqlite"
  sqlite_path: "qsdmplus.db"

monitoring:
  dashboard_port: 8081
  log_viewer_port: 8080
  log_file: "qsdmplus.log"
  log_level: "INFO"  # DEBUG, INFO, WARN, ERROR
```

### Environment Variable Override

All configuration values can be overridden with environment variables:

```powershell
# Network
$env:NETWORK_PORT = "5001"
$env:BOOTSTRAP_PEERS = "peer1:4001,peer2:4001"

# Monitoring
$env:LOG_LEVEL = "DEBUG"
$env:DASHBOARD_PORT = "9091"

# Storage
$env:STORAGE_TYPE = "sqlite"
$env:SQLITE_PATH = "custom.db"
```

### Config File Location

By default, QSDM looks for:
- `qsdm.toml` (preferred)
- `qsdm.yaml` or `qsdm.yml`

You can specify a custom config file:
```powershell
$env:CONFIG_FILE = "custom-config.toml"
```

---

## Enhanced Logging

### Log Levels

QSDM now supports four log levels:

| Level | Description | Use Case |
|-------|------------|----------|
| **DEBUG** | Detailed diagnostic information | Development, troubleshooting |
| **INFO** | General informational messages | Normal operation |
| **WARN** | Warning messages | Potential issues |
| **ERROR** | Error messages | Failures, always logged |

### Setting Log Level

**Via Config File:**
```toml
# config/qsdm.toml
[monitoring]
log_level = "DEBUG"
```

**Via Environment Variable:**
```powershell
$env:LOG_LEVEL = "DEBUG"
```

### Log Output Formats

QSDM supports two log formats:

1. **JSON Format** (default)
   - Structured logging
   - Easy to parse
   - Includes request IDs

2. **Text Format**
   - Human-readable
   - Traditional log format

### Request ID Tracking

Request IDs help track operations across the system:

```go
// Generate new request ID
requestID := logger.NewRequestID()

// Set existing request ID
logger.SetRequestID("custom-id")

// Get current request ID
currentID := logger.GetRequestID()
```

**Example Log Output with Request ID:**

```json
{
  "level": "INFO",
  "msg": "Transaction processed",
  "timestamp": "2024-12-20T10:30:45Z",
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "tx_id": "abc123",
  "amount": 100.0
}
```

### Log Rotation

Logs are automatically rotated:
- **Max Size:** 100 MB per file
- **Max Backups:** 7 files
- **Max Age:** 28 days
- **Compression:** Enabled for old logs

---

## Usage Examples

### Example 1: Development Setup

**`qsdm.toml`:**
```toml
[monitoring]
log_level = "DEBUG"
log_file = "qsdm-dev.log"

[network]
port = 4001
```

**Run:**
```powershell
.\run.ps1
```

### Example 2: Production Setup

**`qsdm.toml`:**
```toml
[monitoring]
log_level = "INFO"
log_file = "/var/log/qsdm/qsdmplus.log"

[network]
port = 4001
bootstrap_peers = ["node1.example.com:4001", "node2.example.com:4001"]

[api]
enable_tls = true
tls_cert_file = "/etc/qsdm/cert.pem"
tls_key_file = "/etc/qsdm/key.pem"
```

**Override for specific deployment:**
```powershell
$env:LOG_LEVEL = "WARN"  # Reduce logging in production
$env:NETWORK_PORT = "5001"
```

### Example 3: Debugging with Request IDs

```go
// In your code
requestID := logger.NewRequestID()
logger.Info("Processing transaction", 
    "request_id", requestID,
    "tx_id", tx.ID,
    "amount", tx.Amount)

// All subsequent logs in this context will include the request ID
logger.Debug("Validating signature", "tx_id", tx.ID)
logger.Info("Transaction stored", "tx_id", tx.ID)
```

---

## Best Practices

### Configuration

1. **Use config files for defaults:**
   - Store standard configuration in `qsdm.toml`
   - Use environment variables for deployment-specific overrides

2. **Version control:**
   - Commit `qsdmplus.toml.example` to version control
   - Don't commit `qsdm.toml` with sensitive data
   - Use environment variables for secrets

3. **Multiple environments:**
   ```powershell
   # Development
   $env:CONFIG_FILE = "qsdm-dev.toml"
   
   # Production
   $env:CONFIG_FILE = "qsdm-prod.toml"
   ```

### Logging

1. **Log levels:**
   - **Development:** `DEBUG` for detailed diagnostics
   - **Staging:** `INFO` for normal operation
   - **Production:** `WARN` or `ERROR` to reduce noise

2. **Request IDs:**
   - Generate request IDs at the start of operations
   - Include request IDs in all related log entries
   - Use request IDs to trace operations across services

3. **Structured logging:**
   - Use key-value pairs for structured data
   - Include context (tx_id, user_id, etc.)
   - Avoid logging sensitive information

---

## Troubleshooting

### Config File Not Loading

**Problem:** Config file changes not taking effect

**Solutions:**
1. Check file name: `qsdm.toml` or `qsdm.yaml`
2. Verify file format (valid TOML/YAML)
3. Check environment variables (they override config file)
4. Verify file path (run from project root)

### Log Level Not Working

**Problem:** DEBUG logs not appearing

**Solutions:**
1. Verify log level in config: `log_level = "DEBUG"`
2. Check environment variable: `$env:LOG_LEVEL = "DEBUG"`
3. Restart the application after changing log level

### Request IDs Not Appearing

**Problem:** Request IDs missing from logs

**Solutions:**
1. Ensure you're using JSON format (default)
2. Generate request ID: `logger.NewRequestID()`
3. Check that request ID is set before logging

---

## Migration Guide

### From Environment Variables Only

**Before:**
```powershell
$env:NETWORK_PORT = "4001"
$env:LOG_LEVEL = "INFO"
.\run.ps1
```

**After:**
```toml
# qsdm.toml
[network]
port = 4001

[monitoring]
log_level = "INFO"
```

```powershell
.\run.ps1  # Config file loaded automatically
```

### From Old Logger

**Before:**
```go
logger.Info("Message")
```

**After:**
```go
logger.Info("Message", "key", "value")  // Same API, now with structured logging
logger.Debug("Debug message")  // New: DEBUG level
```

---

## Summary

✅ **Configuration Management:**
- TOML/YAML config file support
- Environment variable override
- Example config files provided

✅ **Enhanced Logging:**
- Log levels (DEBUG, INFO, WARN, ERROR)
- Request ID tracking
- Structured JSON logging
- Automatic log rotation

✅ **Production Ready:**
- Easy configuration management
- Better observability
- Improved debugging capabilities

---

*For more information, see:*
- `docs/QUICK_START.md` - Quick start guide
- `docs/NEXT_STEPS.md` - Next development steps
- `qsdmplus.toml.example` - Example configuration

