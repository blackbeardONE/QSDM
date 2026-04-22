QSDM Database Exports
=====================

Export Date: 2025-12-25 18:25:42

Files:
------

1. qsdm_20251225_182540.sql (or .db)
   - Main QSDM database
   - Contains transactions, balances, and system data
   - If .db file: Binary copy - use sqlite3 to export on target server
   - If .sql file: SQL dump - can be imported with: sqlite3 qsdmplus.db < qsdm_20251225_182540.sql

2. transactions_20251225_182540.sql (or .db)
   - Transactions database (if separate)
   - Import with: sqlite3 transactions.db < transactions_20251225_182540.sql

3. qsdm_20251225_182540.db-wal / qsdm_20251225_182540.db-shm
   - WAL (Write-Ahead Log) and SHM (Shared Memory) files
   - These are SQLite journal files
   - Copy these along with the main database for complete state

Import Instructions:
-------------------

On the target server:

1. If you have .sql files:
   sqlite3 qsdmplus.db < qsdm_20251225_182540.sql
   sqlite3 transactions.db < transactions_20251225_182540.sql

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
