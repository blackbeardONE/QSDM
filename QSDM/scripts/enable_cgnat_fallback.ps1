param(
    [string]$QsdmRoot = "",
    [string]$Relay = "",
    [string]$Slot = "",
    [string]$Backend = "http://127.0.0.1:8080",
    [string]$BootstrapPeers = "",
    [string]$ChainSyncUrls = "",
    [switch]$PublicP2P,
    [switch]$NoRestart
)

$ErrorActionPreference = "Stop"
if ([string]::IsNullOrWhiteSpace($QsdmRoot)) {
    $QsdmRoot = (Resolve-Path (Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) "..")).Path
}
$QsdmRoot = (Resolve-Path $QsdmRoot).Path
. (Join-Path $QsdmRoot "scripts\lib\qsdm-endpoints.ps1")
$Relay = Resolve-QsdmEndpointValue -Value $Relay -Name "home_gateway_relay" -EnvVar "QSDM_HOME_GATEWAY_RELAY"
$Slot = Resolve-QsdmEndpointValue -Value $Slot -Name "home_gateway_slot" -EnvVar "QSDM_HOME_GATEWAY_SLOT"
$ChainSyncUrls = Resolve-QsdmEndpointValue -Value $ChainSyncUrls -Name "core_api_base" -EnvVar "QSDM_CHAIN_SYNC_URLS"


$validatorScript = Join-Path $QsdmRoot "scripts\start_local_validator.ps1"
$gatewayScript = Join-Path $QsdmRoot "scripts\start_home_gateway.ps1"
$verifyScript = Join-Path $QsdmRoot "scripts\validate_vps_independence.ps1"
foreach ($required in @($validatorScript, $gatewayScript, $verifyScript)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Missing required QSDM script: $required"
    }
}

$validatorArgs = @(
    "-NoProfile",
    "-ExecutionPolicy", "Bypass",
    "-File", $validatorScript,
    "-QsdmRoot", $QsdmRoot,
    "-Networked",
    "-CgNatFallback",
    "-ChainSyncUrls", $ChainSyncUrls,
    "-HealthWaitSeconds", "300",
    "-LockWaitSeconds", "30"
)
$localCoreReady = $false
try {
    $readyResponse = Invoke-WebRequest -Uri "$Backend/api/v1/health/ready" -UseBasicParsing -TimeoutSec 3
    $localCoreReady = ($readyResponse.StatusCode -ge 200 -and $readyResponse.StatusCode -lt 300)
} catch {
    $localCoreReady = $false
}
if ($NoRestart -or $localCoreReady) {
    $validatorArgs += "-UpdateProfileOnly"
} else {
    $validatorArgs += "-Restart"
}
if (-not [string]::IsNullOrWhiteSpace($BootstrapPeers)) {
    $validatorArgs += @("-BootstrapPeers", $BootstrapPeers)
}
if ($PublicP2P) {
    $validatorArgs += "-PublicP2P"
}

& powershell.exe @validatorArgs
if ($LASTEXITCODE -ne 0) {
    throw "start_local_validator.ps1 failed with exit code $LASTEXITCODE"
}

$gatewayArgs = @(
    "-NoProfile",
    "-ExecutionPolicy", "Bypass",
    "-File", $gatewayScript,
    "-Relay", $Relay,
    "-Slot", $Slot,
    "-Backend", $Backend,
    "-ReadOnly",
    "-Restart"
)
& powershell.exe @gatewayArgs
if ($LASTEXITCODE -ne 0) {
    throw "start_home_gateway.ps1 failed with exit code $LASTEXITCODE"
}

$verificationOutput = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $verifyScript `
    -QsdmRoot $QsdmRoot `
    -Relay $Relay `
    -Slot $Slot `
    -AcceptFallback 2>&1
$verificationExit = $LASTEXITCODE
$verificationOutput | ForEach-Object { Write-Output $_ }
if ($verificationExit -ne 0) {
    Write-Warning "CGNAT fallback is configured locally, but the relay surface is not reachable yet. Check outbound TCP 443 to $Relay or configure another reachable relay."
}
exit $verificationExit