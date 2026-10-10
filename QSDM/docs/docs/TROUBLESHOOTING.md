# QSDM Troubleshooting Guide

## Hive users

Using the QSDM Hive desktop app? Start here. The full mining guide is
[Mining today (Hive 1.4.21)](MINING_TODAY.md).

| Symptom | What to do |
|---|---|
| "Unlock your QSDM wallet in Hive to mine." | Hive 1.4.21 signs every mining proof with your wallet. Unlock it in **Settings → Wallet** and start the QSDM Miner task again. |
| "QSDM protocol mining requires NVIDIA Turing or newer (7.5+)." | The GPU is too old. Mining needs an NVIDIA GPU with compute capability 7.5 or higher (GeForce RTX 20 series or newer). |
| Hive cannot read the GPU | Open a terminal and run `nvidia-smi`. If it fails, install or update the NVIDIA driver. |
| Enrollment fails | New mining enrollments are paused during the current recovery; only GPUs enrolled before the recovery can mine. Check [Network status](NETWORK_STATUS.md). |
| Many HTTP `429` responses in the miner log | The mining endpoints on `api.qsdm.tech` are rate-limited per IP address. Run fewer rigs behind one internet address. |
| Hive 1.4.20 (Windows) or 1.4.17 (Linux) does not offer 1.4.21 | Expected: the release key was rotated. Download 1.4.21 from the [download page](https://qsdm.tech/download.html) and install it once by hand; your wallet and settings are kept. See [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md). |
| Windows SmartScreen shows "Windows protected your PC" | The installer is not Authenticode-signed yet. Check the SHA-256 as described in [Downloads & verification](DOWNLOADS_AND_VERIFICATION.md), then choose **More info → Run anyway**. |

> **The rest of this page is for developers building the node from source.**
> Some of it is older than the current code.

## Common Issues and Solutions (developers)

### Build Issues

#### CGO/liboqs Build Failures

**Symptoms:**
```
gcc: error: 10\.wasmer\include: linker input file not found
package wasm_modules/wallet/walletcore is not in std
```

**Solutions:**
1. **Disable CGO** (for testing without crypto):
   ```bash
   export CGO_ENABLED=0
   go build ./...
   ```

2. **Install liboqs** (see `INSTALL_OQS.md`):
   - Follow platform-specific installation instructions
   - Set `CGO_CFLAGS` and `CGO_LDFLAGS` environment variables

3. **Use Docker** (recommended):
   ```bash
   docker-compose up --build
   ```

#### WASM Module Build Failures

**Symptoms:**
- WASM files not generating
- Module loading errors

**Solutions:**
1. **Check Go WASM target**:
   ```bash
   GOOS=js GOARCH=wasm go build -o wallet.wasm wasm_modules/wallet/walletcore/walletcore.go
   ```

2. **Verify Wasmer installation** (for WASI modules):
   - Check `wasmer` is in PATH
   - Verify Wasmer C API is installed

---

### Runtime Issues

#### Database Lock Errors

**Symptoms:**
```
database is locked
```

**Solutions:**
1. **Check for multiple connections**:
   - Ensure only one process accesses the database
   - Close database connections properly with `defer store.Close()`

2. **Use WAL mode** (default in SQLiteStorage):
   - Allows concurrent reads
   - Write operations are serialized

3. **Check file permissions**:
   ```bash
   chmod 644 qsdm.db
   ```

#### Transaction Validation Failures

**Symptoms:**
- Transactions rejected
- "Invalid transaction" errors

**Solutions:**
1. **Check transaction structure**:
   - Verify all required fields are present
   - Check signature format (hex-encoded)

2. **Verify signatures**:
   - Ensure transaction was signed before sending
   - Check signature matches transaction data
   - `sender` must equal `hex(sha256(public_key))` of the signing key

3. **Check parent cells** (Proof-of-Entanglement, see [PROOF_OF_ENTANGLEMENT.md](PROOF_OF_ENTANGLEMENT.md)):
   - Use the `parents` from `GET /api/v1/chain/parents`, fetched just before signing
   - Once the network's PoE activation height is reached: 2-10 distinct parents, each committed in the last 8,640 blocks, none equal to the transaction's own ID
   - An HTTP 422 starting `proof-of-entanglement:` means the parents were not accepted; fetch fresh parents and sign again (the same nonce is fine)

#### Balance Mismatches

**Symptoms:**
- Incorrect balances
- Balance not updating after transactions

**Solutions:**
1. **Verify transaction storage**:
   ```go
   err := store.StoreTransaction(txBytes)
   if err != nil {
       log.Printf("Storage error: %v", err)
   }
   ```

2. **Check balance updates**:
   - Ensure `StoreTransaction` is called
   - Verify sender and recipient addresses are correct

3. **Query balance directly**:
   ```go
   balance, err := store.GetBalance(address)
   ```

---

### Network Issues

#### P2P Connection Failures

**Symptoms:**
- Nodes not connecting
- Messages not propagating

**Solutions:**
1. **Check firewall settings**:
   - Ensure libp2p ports are open
   - Default ports: 4001 (TCP), 4001 (UDP)

2. **Verify bootstrap nodes**:
   - Check bootstrap node addresses are correct
   - Ensure bootstrap nodes are running

3. **Check network interface**:
   ```go
   // In networking setup
   host, err := libp2p.New(
       libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4001"),
   )
   ```

#### Message Broadcasting Issues

**Symptoms:**
- Transactions not reaching other nodes
- Governance votes not propagating

**Solutions:**
1. **Verify message format**:
   - Check JSON encoding is correct
   - Ensure message size is reasonable

2. **Check message handlers**:
   - Verify handlers are registered
   - Check for error logs

---

### Governance Issues

#### Proposal Not Found

**Symptoms:**
```
proposal not found
```

**Solutions:**
1. **Check proposal ID**:
   - Ensure proposal ID matches exactly
   - Case-sensitive matching

2. **Verify proposal exists**:
   ```go
   sv.Mu.RLock()
   proposal, exists := sv.Proposals[proposalID]
   sv.Mu.RUnlock()
   ```

3. **Check persistence file**:
   - Verify JSON file exists and is readable
   - Check file permissions

#### Proposal Not Expiring

**Symptoms:**
- `FinalizeProposal` returns "proposal not yet expired"

**Solutions:**
1. **Check expiration time**:
   ```go
   proposal.ExpiresAt = time.Now().Add(duration)
   ```

2. **Wait for expiration**:
   ```go
   time.Sleep(duration + 1*time.Second)
   ```

3. **Manually expire** (for testing):
   ```go
   proposal.ExpiresAt = time.Now().Add(-1 * time.Second)
   ```

#### Quorum Not Reached

**Symptoms:**
- Proposal fails with "quorum not reached"

**Solutions:**
1. **Check vote counts**:
   ```go
   totalVotes := proposal.VotesFor + proposal.VotesAgainst
   if totalVotes < proposal.Quorum {
       // Quorum not reached
   }
   ```

2. **Verify vote weights**:
   - Ensure vote weights are counted correctly
   - Check vote recording

---

### Performance Issues

#### Slow Transaction Processing

**Symptoms:**
- High latency
- Transactions taking too long

**Solutions:**
1. **Check database performance**:
   - Use WAL mode (already default)
   - Consider indexing frequently queried fields
   - Monitor database size

2. **Optimize validation**:
   - Cache validation results where possible
   - Parallelize independent validations

3. **Profile the code**:
   ```bash
   go test -bench=. -cpuprofile=cpu.prof
   go tool pprof cpu.prof
   ```

#### High Memory Usage

**Symptoms:**
- Memory leaks
- Out of memory errors

**Solutions:**
1. **Check for goroutine leaks**:
   - Ensure all goroutines complete
   - Use context cancellation

2. **Monitor memory**:
   ```bash
   go tool pprof http://localhost:6060/debug/pprof/heap
   ```

3. **Review caching**:
   - Limit cache sizes
   - Implement TTL for cached data

---

### Testing Issues

#### Test Timeouts

**Symptoms:**
```
panic: test timed out after 10m0s
```

**Solutions:**
1. **Increase timeout**:
   ```bash
   go test -timeout 30s ./...
   ```

2. **Check for deadlocks**:
   - Review mutex usage
   - Ensure locks are released

3. **Fix blocking operations**:
   - Use channels with timeouts
   - Avoid blocking I/O in tests

#### Test Failures with CGO

**Symptoms:**
- Tests fail when CGO is enabled
- Missing C libraries

**Solutions:**
1. **Skip CGO tests**:
   ```go
   if poe == nil {
       t.Skip("CGO disabled")
   }
   ```

2. **Use build tags**:
   ```go
   // +build cgo
   ```

---

## Debugging Tips

### Enable Verbose Logging

```go
logger := logging.NewLogger("debug.log", 10, 3, 10)
logger.SetLevel(logging.DEBUG)
```

### Check Transaction Structure

```go
var tx map[string]interface{}
json.Unmarshal(txBytes, &tx)
fmt.Printf("Transaction: %+v\n", tx)
```

### Monitor Network Activity

```go
// In libp2p setup
host.Network().Notify(&network.NotifyBundle{
    ConnectedF: func(n network.Network, c network.Conn) {
        log.Printf("Connected to %s", c.RemotePeer())
    },
})
```

### Database Inspection

```bash
sqlite3 qsdm.db
.tables
SELECT * FROM transactions LIMIT 10;
SELECT * FROM balances;
```

---

## Getting Help

1. **Check logs**: Review application logs for error messages
2. **Review documentation**: See `docs/` directory for detailed guides
3. **Run tests**: Execute test suite to verify functionality
4. **Check GitHub Issues**: Search for similar problems

---

*Last Updated: 2025-01-27*

