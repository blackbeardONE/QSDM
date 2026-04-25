# QSDM Dashboard Access Guide

**Last Updated:** December 2024

---

## Quick Access

### Step 1: Start QSDM Node

**Windows:**
```powershell
.\scripts\run.ps1
```

**Linux:**
```bash
./scripts/run.sh
```

### Step 2: Wait for Dashboard to Start

Look for this message in the logs:
```
Dashboard server starting on port 8081
Dashboard will be available at http://localhost:8081
```

### Step 3: Open in Browser

Simply open:
```
http://localhost:8081
```

**That's it!** No login required (currently unsecured).

---

## Available Endpoints

### Dashboard UI
```
http://localhost:8081/
```
- Main monitoring dashboard
- Real-time metrics visualization
- Health status display

### Metrics API
```
http://localhost:8081/api/metrics
```
- Returns JSON with current system metrics
- Transaction statistics
- Performance metrics
- Network statistics

**Example Response:**
```json
{
  "transactions_valid": 1234,
  "transactions_invalid": 5,
  "transactions_stored": 1229,
  "uptime_seconds": 3600,
  ...
}
```

### Health API
```
http://localhost:8081/api/health
```
- Returns JSON with component health status
- Network, storage, consensus, governance, wallet, dashboard status

**Example Response:**
```json
{
  "overall_status": "healthy",
  "components": {
    "network": "healthy",
    "storage": "healthy",
    "consensus": "healthy",
    ...
  }
}
```

---

## Configuration

### Default Port
- **Default:** `8081`
- **Configurable:** Yes

### Change Dashboard Port

**Option 1: Config File (qsdm.toml)**
```toml
[monitoring]
dashboard_port = 9090
```

**Option 2: Config File (qsdm.yaml)**
```yaml
monitoring:
  dashboard_port: 9090
```

**Option 3: Environment Variable**
```bash
export DASHBOARD_PORT=9090
```

**Windows:**
```powershell
$env:DASHBOARD_PORT = "9090"
```

---

## Security Status

### ⚠️ Current Status: **UNSECURED**

The dashboard currently has:
- ❌ **NO authentication** - No login required
- ❌ **NO authorization** - No access control
- ❌ **NO rate limiting** - No protection against abuse
- ❌ **NO security headers** - Missing security headers

### Security Risks

1. **Anyone with network access** can view:
   - System metrics
   - Transaction statistics
   - Health status
   - Network information

2. **Information disclosure:**
   - Sensitive operational data exposed
   - No access logging
   - No audit trail

3. **Potential attacks:**
   - DoS attacks (no rate limiting)
   - Information gathering for attackers
   - Unauthorized monitoring

### Recommendations

**For Development:**
- ✅ Current setup is acceptable (localhost only)
- ⚠️ Don't expose to public network

**For Production:**
- 🔴 **MUST add authentication** before deployment
- 🔴 **MUST add authorization** (role-based access)
- 🔴 **MUST add rate limiting**
- 🔴 **MUST add security headers**
- 🔴 **MUST use HTTPS/TLS**

---

## Troubleshooting

### Dashboard Not Loading

**Check 1: Is QSDM running?**
```bash
# Check if process is running
ps aux | grep qsdm  # Linux
Get-Process qsdm    # Windows
```

**Check 2: Is port 8081 in use?**
```bash
# Linux
netstat -tuln | grep 8081
lsof -i :8081

# Windows
netstat -ano | findstr :8081
```

**Check 3: Check logs**
```bash
# Look for dashboard startup messages
tail -f qsdm.log | grep -i dashboard
```

**Check 4: Test API endpoint**
```bash
# Test health endpoint
curl http://localhost:8081/api/health

# Should return JSON with health status
```

### Port Already in Use

**Solution 1: Change dashboard port**
```toml
[monitoring]
dashboard_port = 9090
```

**Solution 2: Free up port 8081**
```bash
# Linux - Find and kill process using port
lsof -ti:8081 | xargs kill -9

# Windows - Find and kill process
netstat -ano | findstr :8081
taskkill /PID <PID> /F
```

### Dashboard Shows "Failed to load"

**Check:**
1. Static files are embedded correctly
2. Check browser console for errors
3. Check server logs for errors
4. Verify dashboard server started successfully

---

## Remote Access

### ⚠️ Security Warning

By default, the dashboard only listens on `localhost` (127.0.0.1), which means:
- ✅ **Safe:** Only accessible from the same machine
- ❌ **Not accessible:** From other machines on the network

### Accessing from Another Machine

**Option 1: SSH Tunnel (Recommended)**
```bash
# From remote machine
ssh -L 8081:localhost:8081 user@qsdm-server

# Then access: http://localhost:8081
```

**Option 2: Reverse Proxy (If needed)**
- Use nginx/Apache as reverse proxy
- Add authentication at proxy level
- Use HTTPS/TLS

**Option 3: VPN (For production)**
- Connect via VPN
- Access dashboard through VPN network

### ⚠️ Never Expose to Public Internet

**DO NOT:**
- ❌ Open port 8081 in firewall to public
- ❌ Use port forwarding without authentication
- ❌ Expose dashboard without security

**DO:**
- ✅ Use SSH tunnel for remote access
- ✅ Use VPN for production access
- ✅ Add authentication before exposing

---

## What You'll See

### Dashboard UI Features

1. **Metrics Display**
   - Transaction counts (valid, invalid, stored)
   - System uptime
   - Performance metrics
   - Error counts

2. **Health Status**
   - Component health (network, storage, consensus, etc.)
   - Overall system status
   - Health check timestamps

3. **Real-time Updates**
   - Auto-refreshing metrics
   - Live health status
   - Current system state

---

## Next Steps

### For Security

1. **Add Authentication** (HIGH priority)
   - Implement login page
   - Use JWT tokens (reuse API auth)
   - Session management

2. **Add Authorization** (HIGH priority)
   - Role-based access control
   - Admin vs. viewer roles
   - Permission checks

3. **Add Security Headers** (MEDIUM priority)
   - Security headers middleware
   - CORS configuration
   - Content Security Policy

4. **Add Rate Limiting** (MEDIUM priority)
   - Per-IP rate limiting
   - API endpoint protection
   - DoS prevention

---

## Summary

**Quick Access:**
1. Start QSDM: `.\scripts\run.ps1` (Windows) or `./scripts/run.sh` (Linux)
2. Open browser: `http://localhost:8081`
3. No login required (currently)

**Security Status:**
- ⚠️ **Unsecured** - No authentication
- ⚠️ **Localhost only** - Safe for development
- 🔴 **Must secure** before production deployment

**Available Endpoints:**
- Dashboard: `http://localhost:8081/`
- Metrics: `http://localhost:8081/api/metrics`
- Health: `http://localhost:8081/api/health`

---

*For production deployment, authentication and security must be added!*

