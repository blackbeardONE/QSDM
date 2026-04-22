# PowerShell script to organize QSDM project files
# Moves files to appropriate directories

Write-Host "=== Organizing QSDM Project Files ===" -ForegroundColor Cyan
Write-Host ""

# Create directories
Write-Host "Creating directory structure..." -ForegroundColor Green
New-Item -ItemType Directory -Force -Path "scripts" | Out-Null
New-Item -ItemType Directory -Force -Path "docs\archive" | Out-Null
New-Item -ItemType Directory -Force -Path "tests\archive" | Out-Null
New-Item -ItemType Directory -Force -Path "config" | Out-Null

# Move build scripts
Write-Host "Moving build scripts..." -ForegroundColor Green
$buildScripts = @("build.sh", "build.ps1", "build_no_cgo.ps1", "rebuild_liboqs.sh", "rebuild_liboqs.ps1", "run.sh", "run.ps1")
foreach ($script in $buildScripts) {
    if (Test-Path $script) {
        Move-Item $script "scripts\" -Force
        Write-Host "  Moved: $script" -ForegroundColor Gray
    }
}

# Move test scripts
Write-Host "Moving test scripts..." -ForegroundColor Green
Get-ChildItem -Filter "test_*.ps1" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "test_*.sh" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "test_*.bat" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "run_*_tests.*" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue

# Move check/verify scripts
Write-Host "Moving utility scripts..." -ForegroundColor Green
Get-ChildItem -Filter "check_*.ps1" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "verify_*.ps1" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "fix_*.ps1" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "diagnose_*.ps1" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "start_*.ps1" | Move-Item -Destination "scripts\" -Force -ErrorAction SilentlyContinue

# Move documentation to docs/archive (but keep important ones)
Write-Host "Moving old documentation to docs/archive..." -ForegroundColor Green
$importantDocs = @("README.md", "LICENSE", "PROJECT_STRUCTURE.md")
Get-ChildItem -Filter "*.md" | Where-Object { $importantDocs -notcontains $_.Name } | Move-Item -Destination "docs\archive\" -Force -ErrorAction SilentlyContinue

# Move config examples
Write-Host "Moving configuration files..." -ForegroundColor Green
Get-ChildItem -Filter "*.toml.example" | Move-Item -Destination "config\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "*.yaml.example" | Move-Item -Destination "config\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "*.service" | Move-Item -Destination "config\" -Force -ErrorAction SilentlyContinue

# Move test files
Write-Host "Moving test files..." -ForegroundColor Green
Get-ChildItem -Filter "test_*.c" | Move-Item -Destination "tests\archive\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "test_*.go" | Where-Object { $_.DirectoryName -eq (Get-Location).Path } | Move-Item -Destination "tests\archive\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "test_*.exe" | Move-Item -Destination "tests\archive\" -Force -ErrorAction SilentlyContinue
Get-ChildItem -Filter "test_*.txt" | Move-Item -Destination "tests\archive\" -Force -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "=== Organization Complete ===" -ForegroundColor Green
Write-Host ""
Write-Host "Note: You may need to update:" -ForegroundColor Yellow
Write-Host "  - Import paths in Go files" -ForegroundColor Gray
Write-Host "  - Script paths in documentation" -ForegroundColor Gray
Write-Host "  - Build script references" -ForegroundColor Gray

