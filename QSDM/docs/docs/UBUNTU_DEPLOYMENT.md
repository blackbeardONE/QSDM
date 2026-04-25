# Ubuntu 24.04 Deployment Guide

**Last Updated:** December 2024  
**Target:** Ubuntu 24.04 LTS VPS

---

## Overview

This guide will help you deploy QSDM on an Ubuntu 24.04 VPS. QSDM is a quantum-safe blockchain that uses ML-DSA-87 for 256-bit quantum-resistant security.

---

## Prerequisites

- Ubuntu 24.04 LTS VPS
- Root or sudo access
- At least 2GB RAM (4GB+ recommended)
- At least 20GB disk space
- Internet connection

---

## Step 1: Initial Server Setup

### Update System

```bash
sudo apt update
sudo apt upgrade -y
```

### Install Essential Tools

```bash
sudo apt install -y \
    build-essential \
    cmake \
    git \
    curl \
    wget \
    vim \
    htop \
    ufw
```

---

## Step 2: Install Go

### Install Go 1.20+

```bash
# Download Go (check latest version at https://go.dev/dl/)
wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz

# Remove old Go installation if exists
sudo rm -rf /usr/local/go

# Extract and install
sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz

# Add to PATH
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# Verify installation
go version
```

### Alternative: Install via Package Manager

```bash
sudo apt install -y golang-go
go version
```

---

## Step 3: Install Dependencies

### Install OpenSSL and SQLite Development Headers

```bash
sudo apt install -y \
    libssl-dev \
    libsqlite3-dev \
    pkg-config
```

---

## Step 4: Clone and Build QSDM

### Clone Repository

```bash
cd ~
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM
```

### Build liboqs (Quantum-Safe Library)

```bash
# Make scripts executable
chmod +x rebuild_liboqs.sh build.sh run.sh

# Build liboqs (takes 10-30 minutes)
./rebuild_liboqs.sh
```

This will:
- Clone and build liboqs
- Enable ML-DSA-87 (256-bit quantum-safe)
- Install to `./liboqs_install/`

### Build QSDM

```bash
# Build QSDM binary
./build.sh
```

This creates the `qsdm` binary in the current directory.

---

## Step 5: Configure QSDM

### Create Configuration File

```bash
# Copy example config
cp config/qsdm.toml.example qsdm.toml

# Edit configuration
vim qsdm.toml
```

**Example `qsdm.toml` for VPS:**

```toml
[network]
port = 4001
bootstrap_peers = []

[storage]
type = "sqlite"
sqlite_path = "/opt/qsdm/qsdm.db"

[monitoring]
dashboard_port = 8081
log_viewer_port = 8080
log_file = "/opt/qsdm/qsdm.log"
log_level = "INFO"

[api]
port = 8443
enable_tls = false  # Set to true if you have certificates

[wallet]
initial_balance = 1000.0

[governance]
proposal_file = "/opt/qsdm/proposals.json"

[performance]
transaction_interval = "30s"
health_check_interval = "30s"
```

---

## Step 6: Production Deployment

### Create QSDM User

```bash
# Create system user
sudo useradd -r -s /bin/false -d /opt/qsdm qsdm

# Create directories
sudo mkdir -p /opt/qsdm
sudo chown qsdm:qsdm /opt/qsdm
```

### Install QSDM Files

```bash
# Copy binary
sudo cp qsdm /opt/qsdm/
sudo chmod +x /opt/qsdm/qsdm

# Copy configuration
sudo cp qsdm.toml /opt/qsdm/

# Copy liboqs libraries
sudo cp -r liboqs_install /opt/qsdm/

# Set ownership
sudo chown -R qsdm:qsdm /opt/qsdm
```

### Create Systemd Service

```bash
# Copy service file
sudo cp config/qsdm.service /etc/systemd/system/

# Edit service file if needed
sudo vim /etc/systemd/system/qsdm.service

# Reload systemd
sudo systemctl daemon-reload

# Enable service (start on boot)
sudo systemctl enable qsdm

# Start service
sudo systemctl start qsdm

# Check status
sudo systemctl status qsdm
```

### View Logs

```bash
# Systemd logs
sudo journalctl -u qsdm -f

# Application logs
sudo tail -f /opt/qsdm/qsdm.log
```

---

## Step 7: Firewall Configuration

### Configure UFW

```bash
# Allow SSH (important!)
sudo ufw allow 22/tcp

# Allow QSDM network port
sudo ufw allow 4001/tcp

# Allow dashboard
sudo ufw allow 8081/tcp

# Allow log viewer
sudo ufw allow 8080/tcp

# Allow API (if using)
sudo ufw allow 8443/tcp

# Enable firewall
sudo ufw enable

# Check status
sudo ufw status
```

---

## Step 8: Verify Deployment

### Check Service Status

```bash
sudo systemctl status qsdm
```

### Check Logs

```bash
# Systemd logs
sudo journalctl -u qsdm --no-pager -n 50

# Application logs
sudo tail -n 50 /opt/qsdm/qsdm.log
```

### Test Endpoints

```bash
# Health check
curl http://localhost:8081/api/health

# Dashboard
curl http://localhost:8081/api/metrics
```

### Access Dashboard

Open in browser:
- **Dashboard:** `http://your-vps-ip:8081`
- **Log Viewer:** `http://your-vps-ip:8080`

---

## Step 9: Maintenance

### Restart Service

```bash
sudo systemctl restart qsdm
```

### Stop Service

```bash
sudo systemctl stop qsdm
```

### Update QSDM

```bash
# Stop service
sudo systemctl stop qsdm

# Backup current installation
sudo cp -r /opt/qsdm /opt/qsdm.backup

# Update code
cd ~/QSDM
git pull

# Rebuild
./rebuild_liboqs.sh  # Only if liboqs changed
./build.sh

# Install new binary
sudo cp qsdm /opt/qsdm/
sudo chown qsdm:qsdm /opt/qsdm/qsdm

# Start service
sudo systemctl start qsdm

# Verify
sudo systemctl status qsdm
```

### View Metrics

```bash
# Via API
curl http://localhost:8081/api/metrics | jq

# Via dashboard
# Open http://your-vps-ip:8081 in browser
```

---

## Troubleshooting

### Service Won't Start

**Check logs:**
```bash
sudo journalctl -u qsdm -n 100
```

**Common issues:**
1. **Missing liboqs:** Ensure `LD_LIBRARY_PATH` is set in service file
2. **Port in use:** Change port in `qsdm.toml`
3. **Permission denied:** Check file ownership (`sudo chown -R qsdm:qsdm /opt/qsdm`)

### liboqs Not Found

**Check library path:**
```bash
# Find liboqs
find /opt/qsdm -name "liboqs.so*"

# Update service file
sudo vim /etc/systemd/system/qsdm.service
# Update LD_LIBRARY_PATH
```

### High Memory Usage

**Monitor resources:**
```bash
htop
```

**Adjust log level:**
```toml
[monitoring]
log_level = "WARN"  # Reduce logging
```

### Database Issues

**Check database:**
```bash
sudo -u qsdm sqlite3 /opt/qsdm/qsdm.db ".tables"
```

**Backup database:**
```bash
sudo -u qsdm cp /opt/qsdm/qsdm.db /opt/qsdm/qsdm.db.backup
```

---

## Security Recommendations

### 1. Use TLS for API

```toml
[api]
enable_tls = true
tls_cert_file = "/etc/ssl/certs/qsdm.crt"
tls_key_file = "/etc/ssl/private/qsdm.key"
```

### 2. Restrict Dashboard Access

Use reverse proxy (nginx) with authentication:
```nginx
location / {
    auth_basic "QSDM Dashboard";
    auth_basic_user_file /etc/nginx/.htpasswd;
    proxy_pass http://localhost:8081;
}
```

### 3. Regular Updates

```bash
# Update system
sudo apt update && sudo apt upgrade -y

# Update QSDM
cd ~/QSDM && git pull && ./build.sh
```

### 4. Monitor Logs

```bash
# Set up log rotation
sudo vim /etc/logrotate.d/qsdm
```

---

## Performance Tuning

### Increase File Descriptors

```bash
# Edit limits
sudo vim /etc/security/limits.conf
# Add:
qsdm soft nofile 65536
qsdm hard nofile 65536
```

### Optimize SQLite

The service file already includes optimizations. For custom tuning, edit `qsdm.toml`.

---

## Backup Strategy

### Automated Backup Script

Create `/opt/qsdm/backup.sh`:

```bash
#!/bin/bash
BACKUP_DIR="/opt/qsdm/backups"
DATE=$(date +%Y%m%d_%H%M%S)

mkdir -p "$BACKUP_DIR"

# Backup database
cp /opt/qsdm/qsdm.db "$BACKUP_DIR/qsdm_$DATE.db"

# Backup configuration
cp /opt/qsdm/qsdm.toml "$BACKUP_DIR/qsdm_$DATE.toml"

# Keep only last 7 days
find "$BACKUP_DIR" -name "qsdm_*.db" -mtime +7 -delete
find "$BACKUP_DIR" -name "qsdm_*.toml" -mtime +7 -delete
```

**Add to crontab:**
```bash
sudo crontab -e
# Add:
0 2 * * * /opt/qsdm/backup.sh
```

---

## Monitoring

### System Monitoring

```bash
# Install monitoring tools
sudo apt install -y prometheus-node-exporter

# Or use QSDM's built-in dashboard
# http://your-vps-ip:8081
```

### Alerting

Set up alerts for:
- Service down
- High memory usage
- Disk space low
- High error rate

---

## Next Steps

- **Multi-node setup:** Configure bootstrap peers
- **Load balancing:** Use nginx as reverse proxy
- **Monitoring:** Integrate with Prometheus/Grafana
- **Backup:** Set up automated backups

---

## Support

For issues or questions:
- Check logs: `sudo journalctl -u qsdm`
- Review documentation: `docs/`
- GitHub Issues: [QSDM Issues](https://github.com/blackbeardONE/QSDM/issues)

---

*Happy deploying! 🚀*

