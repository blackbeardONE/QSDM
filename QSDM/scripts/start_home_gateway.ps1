param(
    [string]$Relay = $env:QSDM_HOME_GATEWAY_RELAY,
    [string]$Slot = $env:QSDM_HOME_GATEWAY_SLOT,
    [string]$Backend = "http://127.0.0.1:8080",
    [string]$KeyPath = (Join-Path (Resolve-Path (Join-Path $PSScriptRoot "..\source\.cache\local-validator")).Path "home-gateway.key"),
    [switch]$AllowEnrollment,
    [switch]$DisableHive,
    [switch]$Restart,
    [int]$StartupWaitSeconds = 5
)

$ErrorActionPreference = "Stop"

$LocalValidatorPath = (Resolve-Path (Join-Path $PSScriptRoot "..\source\.cache\local-validator")).Path
$PreferredNewExePath = Join-Path $LocalValidatorPath "qsdm-home-gateway-hive.new.exe"
$PreferredExePath = Join-Path $LocalValidatorPath "qsdm-home-gateway-hive.exe"
$FallbackExePath = Join-Path $LocalValidatorPath "qsdm-home-gateway.exe"
$LauncherLog = Join-Path $LocalValidatorPath "home-gateway.launcher.log"
$StdoutLog = Join-Path $LocalValidatorPath "home-gateway.out.log"
$StderrLog = Join-Path $LocalValidatorPath "home-gateway.err.log"
$PidFile = Join-Path $LocalValidatorPath "home-gateway.pid"
$GatewayProcessNames = @(
    "qsdm-home-gateway-hive",
    "qsdm-home-gateway-hive.new",
    "qsdm-home-gateway"
)
$ExePath = $FallbackExePath
if (Test-Path -LiteralPath $PreferredNewExePath) {
    $ExePath = $PreferredNewExePath
} elseif (Test-Path -LiteralPath $PreferredExePath) {
    $ExePath = $PreferredExePath
}
$DurableKeyPath = Join-Path ([Environment]::GetFolderPath("UserProfile")) ".qsdm\home-gateway.key"
if (-not (Test-Path -LiteralPath $ExePath)) {
    throw "Missing qsdm-home-gateway executable. Build it from QSDM/source with: go build -o .cache/local-validator/qsdm-home-gateway-hive.exe ./cmd/qsdm-home-gateway"
}
if (-not (Test-Path -LiteralPath $KeyPath) -and (Test-Path -LiteralPath $DurableKeyPath)) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $KeyPath) | Out-Null
    Copy-Item -LiteralPath $DurableKeyPath -Destination $KeyPath -Force
}
if (-not (Test-Path -LiteralPath $KeyPath)) {
    throw "Missing gateway key at $KeyPath or $DurableKeyPath. Generate one with: $ExePath --generate-key"
}
if (-not (Test-Path -LiteralPath $DurableKeyPath)) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $DurableKeyPath) | Out-Null
    Copy-Item -LiteralPath $KeyPath -Destination $DurableKeyPath -Force
}
if ([string]::IsNullOrWhiteSpace($Relay)) {
    throw "Relay is required. Pass -Relay https://your-relay.example or set QSDM_HOME_GATEWAY_RELAY."
}
if ([string]::IsNullOrWhiteSpace($Slot)) {
    throw "Slot is required. Pass -Slot your-slot-id or set QSDM_HOME_GATEWAY_SLOT."
}

function Write-GatewayLauncherLog {
    param([string]$Message)
    $stamp = Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"
    Add-Content -LiteralPath $LauncherLog -Value "$stamp $Message"
}

function Get-GatewayProcess {
    Get-Process -Name $GatewayProcessNames -ErrorAction SilentlyContinue
}

function Stop-ExistingGateway {
    if (Test-Path -LiteralPath $PidFile) {
        $pidText = (Get-Content -LiteralPath $PidFile -Raw).Trim()
        if ($pidText -match '^\d+$') {
            Stop-Process -Id ([int]$pidText) -Force -ErrorAction SilentlyContinue
        }
    }
    Get-GatewayProcess | ForEach-Object {
        Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    }
}

if ($Restart) {
    Write-GatewayLauncherLog "restart requested; stopping existing gateway"
    Stop-ExistingGateway
    Start-Sleep -Seconds 1
} else {
    $existing = @(Get-GatewayProcess)
    if ($existing.Count -gt 0) {
        Write-GatewayLauncherLog "gateway already running pid=$($existing[0].Id)"
        Write-Host "Gateway already running pid=$($existing[0].Id)"
        exit 0
    }
}

$KeyHex = (Get-Content -LiteralPath $KeyPath -Raw).Trim()
$args = @(
    "--relay", $Relay,
    "--slot", $Slot,
    "--key-hex", $KeyHex,
    "--backend", $Backend
)
if ($AllowEnrollment) {
    $args += "--allow-enrollment"
}
if (-not $DisableHive) {
    $args += "--allow-hive"
}

$process = Start-Process `
    -FilePath $ExePath `
    -ArgumentList $args `
    -WorkingDirectory $LocalValidatorPath `
    -WindowStyle Hidden `
    -RedirectStandardOutput $StdoutLog `
    -RedirectStandardError $StderrLog `
    -PassThru

Set-Content -LiteralPath $PidFile -Value $process.Id
Write-GatewayLauncherLog "started gateway pid=$($process.Id) exe=$ExePath relay=$Relay slot=$Slot backend=$Backend"

$waitSeconds = [Math]::Max(1, [Math]::Min($StartupWaitSeconds, 10))
Start-Sleep -Seconds $waitSeconds
$running = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
if ($null -eq $running) {
    Write-GatewayLauncherLog "gateway exited during startup pid=$($process.Id)"
    throw "Gateway exited during startup. Check $StderrLog"
}

Write-Host "Gateway started pid=$($process.Id)"
exit 0
