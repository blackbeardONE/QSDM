QSDM Migration Package
======================

Created: 2025-12-25 18:29:23

This package contains all files needed to migrate QSDM to a new development server.

Package Contents:
-----------------

1. source/          - All source code (Go, Rust, JavaScript)
2. databases/       - Database exports and production databases
3. config/          - Configuration files and examples
4. scripts/         - Utility scripts
5. docs/            - Documentation
6. tests/           - Test files
7. deploy/          - Deployment configurations
8. setup.sh         - Linux/Unix setup script
9. setup.ps1        - Windows setup script
10. MIGRATION_GUIDE.md - Detailed migration instructions

Quick Start:
-----------

Linux/Unix:
  chmod +x setup.sh
  ./setup.sh

Windows:
  powershell -ExecutionPolicy Bypass -File setup.ps1

Manual Setup:
------------

1. Install dependencies:
   - Go: go mod download (in source/)
   - Node.js: npm install
   - Python: pip install -r requirements.txt (if needed)

2. Import databases:
   - Use SQL dumps in databases/ directory
   - Or copy binary .db files directly

3. Build the project:
   - cd source
   - go build -o ../qsdmplus ./cmd/qsdmplus

4. Configure:
   - Review config/ directory
   - Copy example configs as needed

For detailed instructions, see MIGRATION_GUIDE.md

