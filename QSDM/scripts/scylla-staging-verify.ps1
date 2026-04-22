# Run scyllasmoke then the optional Scylla integration test against a reachable cluster.
# From repo: .\QSDM\scripts\scylla-staging-verify.ps1
# Defaults SCYLLA_HOSTS to 127.0.0.1 if unset. Forwards SCYLLA_* (keyspace, TLS, auth) from the environment.
# Empty single-node cluster: $env:SCYLLA_AUTO_CREATE_KEYSPACE = "1" (SCYLLA_MIGRATION.md §10); CI sets this.

$ErrorActionPreference = "Stop"
$SourceRoot = Join-Path (Split-Path $PSScriptRoot -Parent) "source"
Set-Location $SourceRoot

if (-not $env:SCYLLA_HOSTS) { $env:SCYLLA_HOSTS = "127.0.0.1" }
if (-not $env:SCYLLA_KEYSPACE) { $env:SCYLLA_KEYSPACE = "qsdm" }

Write-Host "=== scylla-staging-verify (hosts=$($env:SCYLLA_HOSTS) keyspace=$($env:SCYLLA_KEYSPACE)) ===" -ForegroundColor Cyan
Write-Host "1. scyllasmoke"
go run ./cmd/scyllasmoke
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host ""
Write-Host "2. integration test (build tag scylla)"
go test -tags scylla ./pkg/storage/... -run TestIntegrationScyllaRecentTxPath -count=1
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host ""
Write-Host "=== scylla-staging-verify OK ===" -ForegroundColor Green
