param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("referral", "faucet", "integration")]
    [string]$Role,
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [Parameter(Mandatory = $true)][string]$KeystorePath,
    [Parameter(Mandatory = $true)][string]$PassphraseFile,
    [Parameter(Mandatory = $true)][string]$TokenFile,
    [string]$ApiUrl = "http://127.0.0.1:8080",
    [int]$Port = 0,
    [double]$MaxPayout = 0,
    [double]$MinimumReserve = 0,
    [double]$FeeCell = 0.001,
    [switch]$Restart
)

$ErrorActionPreference = "Stop"
$sourceRoot = Join-Path $QsdmRoot "source"
$binary = Join-Path $sourceRoot "qsdm-treasury-signer.exe"
$runDir = Join-Path $sourceRoot ".cache\treasury\$Role"

if ($Port -eq 0) { $Port = if ($Role -eq "referral") { 8897 } elseif ($Role -eq "faucet") { 8898 } else { 8899 } }
if ($MaxPayout -le 0) { $MaxPayout = if ($Role -eq "referral") { 5 } elseif ($Role -eq "faucet") { 1 } else { 1 } }
if ($MinimumReserve -lt 0) { throw "MinimumReserve cannot be negative." }
foreach ($path in @($KeystorePath, $PassphraseFile, $TokenFile)) {
    if (-not (Test-Path -LiteralPath $path)) { throw "Missing required file: $path" }
}
$token = (Get-Content -LiteralPath $TokenFile -Raw).Trim()
if ($token.Length -lt 64) { throw "Signer token must contain at least 64 characters." }

if (-not (Test-Path -LiteralPath $binary)) {
    $go = "C:\Program Files\Go\bin\go.exe"
    if (-not (Test-Path -LiteralPath $go)) { $go = "go" }
    Push-Location $sourceRoot
    try {
        & $go build -o $binary ./cmd/qsdm-game-signer
        if ($LASTEXITCODE -ne 0) { throw "Failed to build qsdm-treasury-signer." }
    } finally {
        Pop-Location
    }
}

New-Item -ItemType Directory -Force -Path $runDir | Out-Null
$stdout = Join-Path $runDir "stdout.log"
$stderr = Join-Path $runDir "stderr.log"
$pidFile = Join-Path $runDir "signer.pid"
$existing = $null
if (Test-Path -LiteralPath $pidFile) {
    $existingPid = [int](Get-Content -LiteralPath $pidFile -Raw)
    $existing = Get-Process -Id $existingPid -ErrorAction SilentlyContinue
}
if ($existing -and -not $Restart) {
    throw "A $Role treasury signer is already running. Use -Restart to replace it."
}
if ($existing) {
    Stop-Process -Id $existing.Id -Force
}

$keys = @(
    "QSDM_SIGNER_LISTEN", "QSDM_SIGNER_API_URL", "QSDM_SIGNER_KEYSTORE",
    "QSDM_SIGNER_PASSPHRASE_FILE", "QSDM_SIGNER_TOKEN", "QSDM_SIGNER_TOKEN_FILE", "QSDM_SIGNER_FEE",
    "QSDM_SIGNER_ROLE", "QSDM_SIGNER_MAX_PAYOUT", "QSDM_SIGNER_MIN_RESERVE"
)
$saved = @{}
foreach ($key in $keys) { $saved[$key] = [Environment]::GetEnvironmentVariable($key, "Process") }
try {
    $env:QSDM_SIGNER_LISTEN = "127.0.0.1:$Port"
    $env:QSDM_SIGNER_API_URL = $ApiUrl
    $env:QSDM_SIGNER_KEYSTORE = (Resolve-Path $KeystorePath).Path
    $env:QSDM_SIGNER_PASSPHRASE_FILE = (Resolve-Path $PassphraseFile).Path
    Remove-Item Env:QSDM_SIGNER_TOKEN -ErrorAction SilentlyContinue
    $env:QSDM_SIGNER_TOKEN_FILE = (Resolve-Path $TokenFile).Path
    $env:QSDM_SIGNER_FEE = [string]$FeeCell
    $env:QSDM_SIGNER_ROLE = $Role
    $env:QSDM_SIGNER_MAX_PAYOUT = [string]$MaxPayout
    $env:QSDM_SIGNER_MIN_RESERVE = [string]$MinimumReserve
    $process = Start-Process -FilePath $binary -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
    Set-Content -LiteralPath $pidFile -Value $process.Id -NoNewline -Encoding ASCII
} finally {
    foreach ($key in $keys) { [Environment]::SetEnvironmentVariable($key, $saved[$key], "Process") }
}

Start-Sleep -Milliseconds 500
$health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/healthz" -TimeoutSec 5
Write-Host "QSDM $Role treasury signer is running"
Write-Host "  PID:       $($process.Id)"
Write-Host "  URL:       http://127.0.0.1:$Port"
Write-Host "  Address:   $($health.address)"
Write-Host "  Max:       $MaxPayout CELL per payout"
Write-Host "  Reserve:   $MinimumReserve CELL"
