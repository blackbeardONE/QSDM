# QSDM+ User Guide

**Last Updated:** 2025-01-27

---

## Table of Contents

1. [Introduction](#introduction)
2. [Getting Started](#getting-started)
3. [Core Concepts](#core-concepts)
4. [Using the CLI Tools](#using-the-cli-tools)
5. [Monitoring and Management](#monitoring-and-management)
6. [Advanced Topics](#advanced-topics)
7. [Best Practices](#best-practices)

---

## Introduction

**QSDM+** (Quantum-Secure Dynamic Mesh Ledger) is a decentralized electronic cash system that uses quantum-safe cryptography and a dynamic mesh architecture for transaction processing.

### Key Features

- **Quantum-Safe Cryptography:** CRYSTALS-Dilithium signatures
- **Dynamic Submesh Routing:** Priority-based transaction routing
- **Governance System:** Snapshot-based voting
- **3D Mesh Validation:** Multi-parent cell validation
- **WASM Modules:** Extensible wallet and validator modules

---

## Getting Started

### Installation

See `QUICK_START.md` for installation instructions.

### First Run

1. Start the node:
   ```bash
   ./qsdmplus
   ```

2. Access the dashboard:
   ```
   http://localhost:8081
   ```

3. Check logs:
   ```
   http://localhost:8080
   ```

---

## Core Concepts

### Transactions

Transactions in QSDM contain:
- **ID:** Unique transaction identifier
- **Sender/Recipient:** Wallet addresses
- **Amount:** Transaction value
- **Fee:** Transaction fee
- **Parent Cells:** References to previous transactions (2-5 cells)
- **Signature:** Quantum-safe signature
- **GeoTag:** Geographic region tag

### Submeshes

Submeshes route transactions based on:
- **Fee Threshold:** Minimum fee for routing
- **Priority Level:** Routing priority (higher = faster)
- **GeoTags:** Geographic regions (US, EU, ASIA, etc.)

### Governance

Governance uses snapshot voting:
- **Proposals:** Governance proposals with expiration
- **Voting:** Token-weighted votes
- **Quorum:** Minimum votes required
- **Finalization:** Automatic after expiration

---

## Using the CLI Tools

### Submesh CLI

The submesh CLI manages dynamic submeshes.

**Available Commands:**

```
help                              - Show help
list                              - List all submeshes
add <name> <priority> [fee] [tags] - Add submesh
remove <name>                     - Remove submesh
update <name> <priority> [fee] [tags] - Update submesh
exit                              - Exit CLI
```

**Examples:**

```bash
# List all submeshes
> list

# Add a high-priority submesh for US/EU with 0.01 fee threshold
> add fastlane 10 0.01 US,EU

# Add a low-priority submesh for ASIA
> add slowlane 5 0.001 ASIA

# Update submesh priority
> update fastlane 15 0.01 US,EU,ASIA

# Remove a submesh
> remove slowlane
```

### Governance CLI

The governance CLI manages proposals and voting.

**Available Commands:**

```
help                                    - Show help
propose <id> <duration> <quorum> <desc> - Create proposal
vote <id> <voter> <weight> <support>   - Cast vote
status <id>                            - Check proposal status
list                                    - List all proposals
finalize <id>                          - Finalize proposal (if expired)
exit                                   - Exit CLI
```

**Examples:**

```bash
# Create a proposal (60 seconds duration, quorum of 5)
> propose prop1 60 5 Increase block size

# Vote on a proposal (voter1, weight 10, support=true)
> vote prop1 voter1 10 true

# Check proposal status
> status prop1

# List all proposals
> list

# Finalize an expired proposal
> finalize prop1
```

---

## Monitoring and Management

### Dashboard

Access the monitoring dashboard at `http://localhost:8081`.

**Dashboard Sections:**

1. **Transaction Metrics**
   - Processed, valid, invalid, stored transactions
   - Validity rate percentage

2. **Network Metrics**
   - Messages sent and received
   - Network activity

3. **Governance Metrics**
   - Proposals created
   - Votes cast

4. **System Metrics**
   - Uptime
   - Quarantines triggered
   - Reputation updates

5. **Component Health**
   - Status of all system components
   - Health indicators

6. **Recent Errors**
   - Last error message
   - Error timestamp

### API Endpoints

**Health Check:**
```bash
curl http://localhost:8081/api/health
```

**Metrics:**
```bash
curl http://localhost:8081/api/metrics
```

### Logs

**Web Log Viewer:**
```
http://localhost:8080
```

**Log File:**
- Location: `qsdmplus.log`
- Rotation: Automatic (100MB max, 7 backups)

---

## Advanced Topics

### Wallet Management

**Get Wallet Address:**
- Check logs for "Wallet service initialized"
- Address is displayed in startup logs

**Check Balance:**
- View in dashboard
- Query via storage API (if implemented)
- Check logs for balance updates

**Create Transactions:**
- Automatic: Every 30 seconds (if wallet initialized)
- Manual: Use wallet API or modify code

### Network Configuration

**Port Configuration:**
- Default: 4001 (libp2p)
- Modify in `pkg/networking/libp2p.go`

**Bootstrap Nodes:**
- Configure in network setup
- Add peer addresses for discovery

### Storage Options

**SQLite (Default):**
- File: `qsdmplus.db`
- WAL mode enabled
- Automatic compression

**ScyllaDB (Optional):**
- Set `USE_SCYLLA=true` in environment
- Configure hosts and keyspace
- Falls back to SQLite if unavailable

---

## Best Practices

### Node Operation

1. **Monitor Dashboard Regularly**
   - Check component health
   - Watch for errors
   - Monitor transaction flow

2. **Review Logs**
   - Check for warnings/errors
   - Monitor network activity
   - Track transaction processing

3. **Manage Submeshes**
   - Create appropriate submeshes for your use case
   - Set reasonable fee thresholds
   - Use geotags for geographic routing

4. **Participate in Governance**
   - Review proposals before voting
   - Consider token weight
   - Monitor proposal expiration

### Security

1. **Protect Private Keys**
   - Wallet keys are stored in memory
   - Don't expose key material
   - Use secure key management in production

2. **Network Security**
   - Use firewall rules
   - Limit network exposure
   - Monitor for suspicious activity

3. **Database Security**
   - Encrypt database at rest
   - Use secure file permissions
   - Regular backups

### Performance

1. **Optimize Submesh Configuration**
   - Use appropriate priority levels
   - Set realistic fee thresholds
   - Balance load across submeshes

2. **Monitor Performance**
   - Use dashboard metrics
   - Review benchmark results
   - Optimize based on usage patterns

3. **Storage Management**
   - Monitor database size
   - Consider archiving old transactions
   - Use compression (enabled by default)

---

## Troubleshooting

See `TROUBLESHOOTING.md` for detailed troubleshooting guide.

**Common Issues:**

1. **Node won't start**
   - Check port availability
   - Verify dependencies
   - Review error logs

2. **No transactions**
   - Verify wallet initialization
   - Check network connectivity
   - Review balance

3. **Dashboard not loading**
   - Check port 8081
   - Review server logs
   - Test API endpoints

---

## Additional Resources

- **API Reference:** `docs/API_REFERENCE.md`
- **Troubleshooting:** `docs/TROUBLESHOOTING.md`
- **Quick Start:** `docs/QUICK_START.md`
- **Deployment:** `DEPLOYMENT_GUIDE.md`
- **Phase 2 Guide:** `docs/PHASE2_CLI_USER_GUIDE.md`

---

*For questions or issues, check the documentation or review the codebase.*

