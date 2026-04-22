# Export SQLite Databases for Migration
# This script exports all SQLite databases to SQL dump files

Write-Host "=== QSDM Database Export Script ===" -ForegroundColor Cyan
Write-Host ""

$exportDir = "database_exports"
$timestamp = Get-Date -Format "yyyyMMdd_HHmmss"

# Create export directory
if (-not (Test-Path $exportDir)) {
    New-Item -ItemType Directory -Path $exportDir | Out-Null
    Write-Host "Created export directory: $exportDir" -ForegroundColor Green
}

# Function to export database
function Export-Database {
    param(
        [string]$dbPath,
        [string]$outputFile
    )
    
    if (-not (Test-Path $dbPath)) {
        Write-Host "Warning: Database file not found: $dbPath" -ForegroundColor Yellow
        return $false
    }
    
    Write-Host "Exporting: $dbPath" -ForegroundColor Yellow
    
    # Check if sqlite3 is available
    $sqlite3Available = $false
    try {
        $null = sqlite3 --version 2>&1
        $sqlite3Available = $true
    } catch {
        $sqlite3Available = $false
    }
    
    if ($sqlite3Available) {
        # Use sqlite3 command-line tool
        Write-Host "  Using sqlite3 command-line tool..." -ForegroundColor Gray
        sqlite3 $dbPath ".dump" | Out-File -FilePath $outputFile -Encoding UTF8
        Write-Host "  Exported to: $outputFile" -ForegroundColor Green
        return $true
    } else {
        # Alternative: Copy the database file directly
        Write-Host "  sqlite3 not found. Copying database file directly..." -ForegroundColor Gray
        Copy-Item -Path $dbPath -Destination $outputFile -Force
        Write-Host "  Copied to: $outputFile" -ForegroundColor Green
        Write-Host "  Note: This is a binary copy. Use sqlite3 on target server to export." -ForegroundColor Yellow
        return $true
    }
}

# Export main database
$qsdmDb = "qsdmplus.db"
$qsdmExport = Join-Path $exportDir "qsdm_${timestamp}.sql"
if (Test-Path $qsdmDb) {
    Export-Database -dbPath $qsdmDb -outputFile $qsdmExport
    
    # Also copy WAL and SHM files if they exist
    if (Test-Path "qsdmplus.db-wal") {
        Copy-Item -Path "qsdmplus.db-wal" -Destination (Join-Path $exportDir "qsdm_${timestamp}.db-wal") -Force
        Write-Host "  Copied WAL file: qsdmplus.db-wal" -ForegroundColor Gray
    }
    if (Test-Path "qsdmplus.db-shm") {
        Copy-Item -Path "qsdmplus.db-shm" -Destination (Join-Path $exportDir "qsdm_${timestamp}.db-shm") -Force
        Write-Host "  Copied SHM file: qsdmplus.db-shm" -ForegroundColor Gray
    }
} else {
    Write-Host "Warning: qsdmplus.db not found" -ForegroundColor Yellow
}

# Export transactions database
$transactionsDb = "transactions.db"
$transactionsExport = Join-Path $exportDir "transactions_${timestamp}.sql"
if (Test-Path $transactionsDb) {
    Export-Database -dbPath $transactionsDb -outputFile $transactionsExport
} else {
    Write-Host "Warning: transactions.db not found" -ForegroundColor Yellow
}

# Create a README with instructions
$readmePath = Join-Path $exportDir "README.txt"
$readmeContent = @"
QSDM Database Exports
=====================

Export Date: $(Get-Date -Format "yyyy-MM-dd HH:mm:ss")

Files:
------

1. qsdm_${timestamp}.sql (or .db)
   - Main QSDM database
   - Contains transactions, balances, and system data
   - If .db file: Binary copy - use sqlite3 to export on target server
   - If .sql file: SQL dump - can be imported with: sqlite3 qsdmplus.db < qsdm_${timestamp}.sql

2. transactions_${timestamp}.sql (or .db)
   - Transactions database (if separate)
   - Import with: sqlite3 transactions.db < transactions_${timestamp}.sql

3. qsdm_${timestamp}.db-wal / qsdm_${timestamp}.db-shm
   - WAL (Write-Ahead Log) and SHM (Shared Memory) files
   - These are SQLite journal files
   - Copy these along with the main database for complete state

Import Instructions:
-------------------

On the target server:

1. If you have .sql files:
   sqlite3 qsdmplus.db < qsdm_${timestamp}.sql
   sqlite3 transactions.db < transactions_${timestamp}.sql

2. If you have .db files (binary copies):
   - Copy the .db files to the target server
   - Copy the .db-wal and .db-shm files if present
   - SQLite will automatically use them

3. Set proper permissions:
   chmod 644 qsdmplus.db transactions.db

4. Verify the import:
   sqlite3 qsdmplus.db "SELECT COUNT(*) FROM transactions;"
   sqlite3 qsdmplus.db "SELECT COUNT(*) FROM balances;"

Notes:
------
- The databases use WAL mode for better concurrency
- Make sure to stop the QSDM service before exporting
- On the target server, ensure SQLite version compatibility
"@

Set-Content -Path $readmePath -Value $readmeContent -Encoding UTF8
Write-Host ""
Write-Host "Created README: $readmePath" -ForegroundColor Green

Write-Host ""
Write-Host "=== Export Complete ===" -ForegroundColor Cyan
Write-Host "Exported files are in: $exportDir" -ForegroundColor Green
Write-Host ""

