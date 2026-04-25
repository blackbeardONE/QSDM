# Production Deployment Guide - QSDM

## Overview

This guide provides step-by-step instructions for deploying QSDM in a production environment.

**Last Updated:** December 14, 2025  
**Status:** Production Ready

---

## Prerequisites

### System Requirements

- **OS:** Windows 10+, Linux (Ubuntu 20.04+), macOS 12+
- **CPU:** 4+ cores recommended
- **RAM:** 8GB minimum, 16GB+ recommended
- **Storage:** 100GB+ SSD recommended
- **Network:** Stable internet connection, open ports (see Network Configuration)

### Software Dependencies

- **Go:** 1.23+ (for building from source)
- **CUDA Toolkit:** 11.0+ (optional, for GPU acceleration)
- **OpenSSL:** 3.0+ (for liboqs)
- **liboqs:** Latest version (for quantum-safe cryptography)
- **Docker:** 20.10+ (for containerized deployment)

---

## Deployment Options

### Option 1: Docker Deployment (Recommended)

#### Quick Start

```bash
# Clone repository
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM

# Build Docker image
docker build -t qsdm:latest .

# Run container
docker run -d \
  --name qsdm-node \
  -p 8080:8080 \
  -p 8081:8081 \
  -v qsdm-data:/data \
  qsdm:latest
```

#### Docker Compose (Multi-Node)

```yaml
version: '3.8'

services:
  qsdm-node1:
    image: qsdm:latest
    ports:
      - "8080:8080"
      - "8081:8081"
    volumes:
      - qsdm-data1:/data
    environment:
      - NODE_ID=node1
      - NETWORK_PORT=8080
      - DASHBOARD_PORT=8081
    networks:
      - qsdm-network

  qsdm-node2:
    image: qsdm:latest
    ports:
      - "8082:8080"
      - "8083:8081"
    volumes:
      - qsdm-data2:/data
    environment:
      - NODE_ID=node2
      - NETWORK_PORT=8080
      - DASHBOARD_PORT=8081
    networks:
      - qsdm-network

volumes:
  qsdm-data1:
  qsdm-data2:

networks:
  qsdm-network:
    driver: bridge
```

Run with:
```bash
docker-compose up -d
```

---

### Option 2: Native Binary Deployment

#### Windows

```powershell
# Build with CGO
powershell -ExecutionPolicy Bypass -File build_with_cgo_no_cuda.ps1

# Run
$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"
.\qsdm.exe
```

#### Linux

```bash
# Build
CGO_ENABLED=1 go build -o qsdm ./cmd/qsdm

# Run
./qsdm
```

#### macOS

```bash
# Build
CGO_ENABLED=1 go build -o qsdm ./cmd/qsdm

# Run
./qsdm
```

---

## Configuration

### Environment Variables

```bash
# Network Configuration
NETWORK_PORT=8080              # libp2p network port
DASHBOARD_PORT=8081            # Dashboard web server port
LOG_VIEWER_PORT=8082           # Log viewer port

# Storage Configuration
STORAGE_TYPE=sqlite            # sqlite or scylladb
STORAGE_PATH=/data/qsdm.db     # SQLite database path
SCYLLADB_HOSTS=localhost       # ScyllaDB hosts (comma-separated)

# Consensus Configuration
CONSENSUS_TYPE=poe             # Proof-of-Entanglement
QUANTUM_SAFE_ENABLED=true      # Enable quantum-safe crypto

# Monitoring Configuration
METRICS_ENABLED=true           # Enable metrics collection
HEALTH_CHECK_INTERVAL=30s      # Health check interval
LOG_LEVEL=info                 # Log level (debug, info, warn, error)

# CUDA Configuration
CUDA_ENABLED=true              # Enable CUDA acceleration
CUDA_DEVICE_ID=0               # CUDA device ID
```

### Configuration File

Create `config.yaml`:

```yaml
network:
  port: 8080
  bootstrap_nodes:
    - /ip4/127.0.0.1/tcp/8080/p2p/QmNode1
    - /ip4/127.0.0.1/tcp/8081/p2p/QmNode2

storage:
  type: sqlite
  path: /data/qsdm.db
  compression: zstandard
  encryption: aes-gcm

consensus:
  type: poe
  parent_cells_required: 2
  quantum_safe: true

monitoring:
  enabled: true
  dashboard_port: 8081
  metrics_interval: 30s

cuda:
  enabled: true
  device_id: 0
```

---

## Network Configuration

### Firewall Rules

**Required Ports:**
- `8080` - libp2p networking (TCP/UDP)
- `8081` - Dashboard web server (TCP)
- `8082` - Log viewer (TCP, optional)

**Linux (iptables):**
```bash
sudo iptables -A INPUT -p tcp --dport 8080 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 8081 -j ACCEPT
sudo iptables -A INPUT -p udp --dport 8080 -j ACCEPT
```

**Windows (PowerShell):**
```powershell
New-NetFirewallRule -DisplayName "QSDM Network" -Direction Inbound -LocalPort 8080 -Protocol TCP -Action Allow
New-NetFirewallRule -DisplayName "QSDM Dashboard" -Direction Inbound -LocalPort 8081 -Protocol TCP -Action Allow
```

### Bootstrap Nodes

Configure bootstrap nodes in `config.yaml` or via environment variables:

```bash
BOOTSTRAP_NODES="/ip4/192.168.1.100/tcp/8080/p2p/QmNode1,/ip4/192.168.1.101/tcp/8080/p2p/QmNode2"
```

---

## Monitoring & Observability

### Dashboard Access

Access the monitoring dashboard at:
```
http://localhost:8081
```

**Endpoints:**
- `/` - Dashboard UI
- `/api/metrics` - Metrics JSON
- `/api/health` - Health status JSON

### Health Checks

```bash
# Check health
curl http://localhost:8081/api/health

# Check metrics
curl http://localhost:8081/api/metrics
```

### Logging

Logs are written to:
- `qsdm.log` - Main application log
- `qsdm-error.log` - Error log (if configured)

**Log Viewer:**
```
http://localhost:8082
```

---

## Backup & Recovery

### Backup Strategy

**SQLite:**
```bash
# Backup database
cp /data/qsdm.db /backup/qsdm-$(date +%Y%m%d).db

# Compressed backup
tar -czf /backup/qsdm-$(date +%Y%m%d).tar.gz /data/qsdm.db
```

**ScyllaDB:**
```bash
# Use nodetool for ScyllaDB backups
nodetool snapshot qsdm_keyspace
```

### Recovery

```bash
# Restore from backup
cp /backup/qsdm-20251214.db /data/qsdm.db

# Restart node
systemctl restart qsdm
```

---

## Security Hardening

### 1. Run as Non-Root User

```bash
# Create user
sudo useradd -r -s /bin/false qsdm

# Set ownership
sudo chown -R qsdm:qsdm /data

# Run as user
sudo -u qsdm ./qsdm
```

### 2. Firewall Configuration

See Network Configuration section above.

### 3. TLS/SSL (For Dashboard)

```bash
# Generate certificates
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365

# Configure in config.yaml
dashboard:
  tls_enabled: true
  cert_file: /path/to/cert.pem
  key_file: /path/to/key.pem
```

### 4. Secrets Management

Use environment variables or secrets management tools:

```bash
# Use secrets file (restrict permissions)
chmod 600 secrets.env
source secrets.env
```

---

## Performance Tuning

### SQLite Optimization

```yaml
storage:
  sqlite:
    journal_mode: WAL
    synchronous: NORMAL
    cache_size: -64000  # 64MB cache
    page_size: 4096
```

### CUDA Optimization

```yaml
cuda:
  enabled: true
  batch_size: 1000
  stream_count: 4
  memory_pool_size: 2GB
```

### Network Optimization

```yaml
network:
  connection_pool_size: 100
  message_batch_size: 100
  compression: zstandard
```

---

## Troubleshooting

### Common Issues

**1. Port Already in Use**
```bash
# Find process using port
netstat -ano | findstr :8080  # Windows
lsof -i :8080                 # Linux/macOS

# Kill process
kill -9 <PID>
```

**2. CUDA Not Available**
```bash
# Check CUDA
nvidia-smi

# Verify CUDA installation
nvcc --version
```

**3. Storage Errors**
```bash
# Check disk space
df -h

# Check file permissions
ls -la /data/qsdm.db
```

**4. Network Connectivity**
```bash
# Test port connectivity
telnet localhost 8080

# Check firewall
sudo ufw status  # Linux
```

---

## Scaling

### Horizontal Scaling

1. **Add More Nodes:**
   - Deploy additional nodes
   - Configure bootstrap nodes
   - Nodes will automatically discover each other

2. **Load Balancing:**
   - Use reverse proxy (nginx, HAProxy)
   - Configure health checks
   - Distribute dashboard traffic

### Vertical Scaling

1. **Increase Resources:**
   - More CPU cores
   - More RAM
   - Faster storage (NVMe SSD)

2. **Enable CUDA:**
   - Install CUDA Toolkit
   - Enable GPU acceleration
   - Configure CUDA device

---

## Maintenance

### Regular Tasks

**Daily:**
- Monitor dashboard for errors
- Check disk space
- Review logs

**Weekly:**
- Backup database
- Review performance metrics
- Update dependencies

**Monthly:**
- Security updates
- Performance optimization
- Capacity planning

---

## Support

### Resources

- **Documentation:** `docs/` directory
- **Troubleshooting:** `docs/TROUBLESHOOTING.md`
- **API Reference:** `docs/API_REFERENCE.md`
- **User Guide:** `docs/USER_GUIDE.md`

### Getting Help

- **GitHub Issues:** https://github.com/blackbeardONE/QSDM/issues
- **Community:** [To be defined]

---

**Status:** Production Ready  
**Last Updated:** December 14, 2025

