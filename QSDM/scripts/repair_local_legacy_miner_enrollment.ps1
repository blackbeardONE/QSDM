param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$NodeID = "",
    [int]$LegacyEnrollmentMaxHeight = 10000,
    [switch]$PrepareOnly
)

$ErrorActionPreference = "Stop"

$localRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$runDir = Join-Path $localRoot "run-v2"
$statePath = Join-Path $runDir "qsdm_enrollment.json"
$minerConfigPath = Join-Path $HOME ".qsdm\miner.toml"
$archiveDir = Join-Path $runDir "Archived"
$readyUrl = "http://127.0.0.1:8080/api/v1/health/ready"
$apiBase = "http://127.0.0.1:8080/api/v1"

function Read-TomlString {
    param([string]$Text, [string]$Key)
    # Older qsdmminer-gui builds wrote CR-only line endings on Windows.
    $Text = $Text.Replace("`r`n", "`n").Replace("`r", "`n")
    $escaped = [regex]::Escape($Key)
    $pattern = '(?m)^\s*' + $escaped + '\s*=\s*"([^"]+)"'
    $match = [regex]::Match($Text, $pattern)
    if (-not $match.Success) { return "" }
    return $match.Groups[1].Value.Replace('\\', '\')
}

function Stop-ProcessByPidFile {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return $false }
    $text = (Get-Content -Raw -LiteralPath $Path).Trim()
    if ($text -notmatch '^\d+$') { return $false }
    $process = Get-Process -Id ([int]$text) -ErrorAction SilentlyContinue
    if (-not $process) { return $false }
    Stop-Process -Id $process.Id -Force
    return $true
}

if (-not (Test-Path -LiteralPath $statePath)) {
    throw "Enrollment state was not found at $statePath"
}
if (-not (Test-Path -LiteralPath $minerConfigPath)) {
    throw "Miner configuration was not found at $minerConfigPath"
}

$config = Get-Content -Raw -LiteralPath $minerConfigPath
if ([string]::IsNullOrWhiteSpace($NodeID)) {
    $NodeID = Read-TomlString -Text $config -Key "node_id"
}
$gpuUUID = Read-TomlString -Text $config -Key "gpu_uuid"
$hmacKeyPath = Read-TomlString -Text $config -Key "hmac_key_path"
if ([string]::IsNullOrWhiteSpace($NodeID) -or
    [string]::IsNullOrWhiteSpace($gpuUUID) -or
    [string]::IsNullOrWhiteSpace($hmacKeyPath)) {
    throw "miner.toml must define node_id, gpu_uuid, and hmac_key_path"
}
if (-not (Test-Path -LiteralPath $hmacKeyPath)) {
    throw "Miner HMAC key was not found at $hmacKeyPath"
}

$state = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json
$records = @($state.records)
$record = @($records | Where-Object { $_.node_id -eq $NodeID })
if ($record.Count -ne 1) {
    throw "Expected exactly one enrollment for NodeID $NodeID; found $($record.Count)"
}
$record = $record[0]
if ($record.gpu_uuid -ne $gpuUUID) {
    throw "Refusing repair: enrollment GPU $($record.gpu_uuid) does not match local GPU $gpuUUID"
}
if ([uint64]$record.enrolled_at_height -gt [uint64]$LegacyEnrollmentMaxHeight) {
    throw "Refusing repair: enrollment height $($record.enrolled_at_height) is newer than the legacy cutoff $LegacyEnrollmentMaxHeight"
}

$localHmacHex = (Get-Content -Raw -LiteralPath $hmacKeyPath).Trim().ToLowerInvariant()
$stateHmacHex = [Convert]::ToHexString(
    [Convert]::FromBase64String([string]$record.hmac_key)
).ToLowerInvariant()
if ($localHmacHex -ne $stateHmacHex) {
    throw "Refusing repair: local miner HMAC key does not match the enrolled NodeID"
}

$summary = [pscustomobject]@{
    NodeID = $NodeID
    GPUUUID = $gpuUUID
    LegacyOwner = $record.owner
    EnrolledAtHeight = $record.enrolled_at_height
    StatePath = $statePath
}
$summary | Format-List
if ($PrepareOnly) { exit 0 }

$watchdogWasRunning = Stop-ProcessByPidFile -Path (Join-Path $localRoot "watchdog.pid")
$validatorNames = @(
    "qsdm-local-validator",
    "qsdm-local-validator-sqlite.hotfix",
    "qsdm-local-validator-sqlite.candidate",
    "qsdm-local-validator-sqlite.new",
    "qsdm-local-validator-sqlite",
    "qsdm-local-validator-task-catalog",
    "qsdm-local-validator-hive",
    "qsdm-local-validator-hive.new",
    "qsdm-local-validator-next",
    "qsdm-sqlite-next",
    "qsdm-sqlite",
    "qsdm-new",
    "qsdm"
)
foreach ($name in $validatorNames) {
    Get-Process -Name $name -ErrorAction SilentlyContinue |
        Stop-Process -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Seconds 2

New-Item -ItemType Directory -Force -Path $archiveDir | Out-Null
$stamp = Get-Date -Format "yyyyMMddTHHmmss"
$backupPath = Join-Path $archiveDir "qsdm_enrollment.$stamp.before-legacy-retirement.json"
Copy-Item -LiteralPath $statePath -Destination $backupPath

$state.records = @($records | Where-Object { $_.node_id -ne $NodeID })
$json = $state | ConvertTo-Json -Depth 16
$tempPath = "$statePath.repair-$PID"
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText($tempPath, $json, $utf8NoBom)
Move-Item -LiteralPath $tempPath -Destination $statePath -Force

& (Join-Path $QsdmRoot "scripts\start_local_validator.ps1") -Restart
$deadline = (Get-Date).AddSeconds(45)
do {
    try {
        $response = Invoke-WebRequest -Uri $readyUrl -UseBasicParsing -TimeoutSec 3
        if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300) { break }
    } catch {
        Start-Sleep -Seconds 1
    }
} while ((Get-Date) -lt $deadline)
if ((Get-Date) -ge $deadline) {
    throw "Validator did not become ready after legacy enrollment retirement. Backup: $backupPath"
}

if ($watchdogWasRunning) {
    $watchdogScript = Join-Path $QsdmRoot "scripts\watch_local_stack.ps1"
    Start-Process -FilePath "pwsh.exe" -WindowStyle Hidden -ArgumentList @(
        "-NoProfile", "-File", $watchdogScript, "-QsdmRoot", $QsdmRoot,
        "-CheckPublicGateway"
    ) | Out-Null
}

[pscustomobject]@{
    Repaired = $true
    NodeID = $NodeID
    ArchivedState = $backupPath
    ValidatorReady = $true
    Next = "Submit a signed v2 enrollment from QSDM Hive."
} | Format-List
