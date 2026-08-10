param(
    [string]$QsdmRoot = "",
    [string]$Relay = "https://api.qsdm.tech",
    [string]$Slot = "home-validator",
    [string]$Backend = "http://127.0.0.1:8080",
    [int]$IntervalSeconds = 30,
    [int]$RestartAfterFailures = 10,
    [ValidateRange(30, 900)]
    [int]$ValidatorStartupGraceSeconds = 300,
    [int]$GatewayRestartAfterFailures = 3,
    [ValidateRange(5, 120)]
    [int]$GatewayLauncherWaitSeconds = 30,
    [ValidateRange(10, 900)]
    [int]$GatewayRetryInitialSeconds = 30,
    [ValidateRange(30, 3600)]
    [int]$GatewayRetryMaxSeconds = 600,
    [ValidateRange(1, 1440)]
    [int]$CacheMaintenanceMinutes = 30,
    [ValidateRange(0, 1024)]
    [double]$MinimumFreeGiB = 5,
    [ValidateRange(0, 1024)]
    [double]$TargetFreeGiB = 8,
    [switch]$CheckPublicGateway,
    [switch]$NoPublicGatewayCheck,
    [switch]$Once
)

if ([string]::IsNullOrWhiteSpace($QsdmRoot)) {
    $scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
    $QsdmRoot = (Resolve-Path (Join-Path $scriptDirectory "..")).Path
}

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$ModeConfigPath = Join-Path $LocalRoot "validator-mode.json"
$ValidatorMode = "solo"
$ValidatorChainSyncUrls = "https://api.qsdm.tech/api/v1"
$ValidatorBootstrapPeers = ""
$ValidatorPublicP2P = $false
$ValidatorBlockProducer = $false
if (Test-Path -LiteralPath $ModeConfigPath) {
    try {
        $modeConfig = Get-Content -Raw -LiteralPath $ModeConfigPath | ConvertFrom-Json
        if ([string]$modeConfig.mode -eq "networked") {
            $ValidatorMode = "networked"
            if (-not [string]::IsNullOrWhiteSpace([string]$modeConfig.chainSyncUrls)) {
                $ValidatorChainSyncUrls = [string]$modeConfig.chainSyncUrls
            }
            $ValidatorBootstrapPeers = [string]$modeConfig.bootstrapPeers
            $ValidatorPublicP2P = [bool]$modeConfig.publicP2P
            if ($null -ne $modeConfig.PSObject.Properties["blockProducer"]) {
                $ValidatorBlockProducer = [bool]$modeConfig.blockProducer
            }
        }
    } catch {
        throw "Invalid validator mode config at ${ModeConfigPath}: $($_.Exception.Message)"
    }
}
$RunDirName = if ($ValidatorMode -eq "networked") { "run-networked" } else { "run-v2" }
$RunDir = Join-Path $LocalRoot $RunDirName
$LogPath = Join-Path $LocalRoot "watchdog.log"
$PidPath = Join-Path $LocalRoot "watchdog.pid"
$IdentityPath = Join-Path $LocalRoot "watchdog.process.json"
$ValidatorScript = Join-Path $QsdmRoot "scripts\start_local_validator.ps1"
$GatewayScript = Join-Path $QsdmRoot "scripts\start_home_gateway.ps1"
$CacheMaintenanceScript = Join-Path $QsdmRoot "scripts\maintain_generated_cache.ps1"
$ReadyUrl = "$Backend/api/v1/health/ready"
$PublicBaseUrl = "$Relay/attest/$Slot/api/v1"
$PublicUrl = "$PublicBaseUrl/status"
$QsdmCli = Join-Path $QsdmRoot "source\qsdmcli.exe"
$PublicGatewayCheckEnabled = -not $NoPublicGatewayCheck.IsPresent
$ValidatorProcessNames = @(
    "qsdm-local-validator",
    "qsdm-local-validator-sqlite*",
    "qsdm-local-validator-task-catalog",
    "qsdm-local-validator-treasury",
    "qsdm-local-validator-hive",
    "qsdm-local-validator-hive.new",
    "qsdm-sqlite-next",
    "qsdm-sqlite",
    "qsdm-new",
    "qsdm"
)
$GatewayProcessNames = @("qsdm-home-gateway*")

$env:HTTP_PROXY = ""
$env:HTTPS_PROXY = ""
$env:ALL_PROXY = ""
$env:NO_PROXY = "127.0.0.1,localhost,api.qsdm.tech"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

New-Item -ItemType Directory -Force -Path $LocalRoot, $RunDir | Out-Null

function Write-WatchdogLog {
    param([string]$Message)
    $stamp = Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"
    Add-Content -LiteralPath $LogPath -Value "$stamp $Message"
}

function Get-NativeProcessStartUtc {
    param([Parameter(Mandatory)][int]$ProcessIdentifier)

    if (-not ("QsdmProcessTimes" -as [type])) {
        Add-Type -TypeDefinition @"
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

public static class QsdmProcessTimes
{
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GetProcessTimes(
        IntPtr processHandle,
        out long creationTime,
        out long exitTime,
        out long kernelTime,
        out long userTime);

    public static DateTime GetStartTimeUtc(int processId)
    {
        using (Process process = Process.GetProcessById(processId))
        {
            long creationTime;
            long exitTime;
            long kernelTime;
            long userTime;
            if (!GetProcessTimes(
                process.Handle,
                out creationTime,
                out exitTime,
                out kernelTime,
                out userTime))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error());
            }
            return DateTime.FromFileTimeUtc(creationTime);
        }
    }
}
"@
    }
    return [QsdmProcessTimes]::GetStartTimeUtc($ProcessIdentifier)
}

# Same reasoning as start_local_validator.ps1: Get-FileHash is PS 4.0+ and is
# unresolvable in some hosts this stack is launched from. A throw here kills
# the supervisor itself, after which nothing restarts the validator or the
# gateway -- so the dependency is removed even though this path has not failed
# in production yet.
function Get-QsdmFileSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $stream = [System.IO.File]::OpenRead($Path)
        try {
            $bytes = $sha.ComputeHash($stream)
        } finally {
            $stream.Close()
        }
    } finally {
        $sha.Dispose()
    }
    return ([BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
}

function Write-WatchdogIdentity {
    $process = Get-Process -Id $PID -ErrorAction Stop
    $identity = [ordered]@{
        schema = "qsdm.watchdog-process.v1"
        pid = $PID
        process_start_utc = (Get-NativeProcessStartUtc -ProcessIdentifier $PID).ToString("o")
        process_path = $process.Path
        script = [IO.Path]::GetFullPath($PSCommandPath)
        script_sha256 = Get-QsdmFileSha256 -Path $PSCommandPath
        qsdm_root = $QsdmRoot
        written_at_utc = [DateTime]::UtcNow.ToString("o")
    }
    $tempPath = "$IdentityPath.tmp-$PID"
    [IO.File]::WriteAllText(
        $tempPath,
        ($identity | ConvertTo-Json),
        [Text.UTF8Encoding]::new($false)
    )
    Move-Item -LiteralPath $tempPath -Destination $IdentityPath -Force
}

function Test-HttpOk {
    param(
        [string]$Url,
        [int]$TimeoutSeconds = 5
    )
    try {
        $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec $TimeoutSeconds
        return ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300)
    } catch {
        return $false
    }
}

function Test-PublicGatewayOk {
    # Windows PowerShell's Schannel can fail before sending an HTTPS request on
    # otherwise healthy hosts (SEC_E_NO_CREDENTIALS). qsdmcli uses Go's TLS
    # stack and is part of this installation, so use it as the authoritative
    # public-route probe instead of turning a local TLS-client fault into a
    # gateway restart loop.
    if (-not (Test-Path -LiteralPath $QsdmCli)) {
        return Test-HttpOk -Url $PublicUrl -TimeoutSeconds 10
    }
    $oldApiUrl = $env:QSDM_API_URL
    try {
        $env:QSDM_API_URL = $PublicBaseUrl
        & $QsdmCli status *> $null
        return ($LASTEXITCODE -eq 0)
    } catch {
        return $false
    } finally {
        $env:QSDM_API_URL = $oldApiUrl
    }
}

function Get-ProcessCount {
    param([string]$Name)
    return @(Get-Process -Name $Name -ErrorAction SilentlyContinue).Count
}

function Get-ProcessCountAny {
    param([string[]]$Names)
    $count = 0
    foreach ($name in $Names) {
        $count += Get-ProcessCount -Name $name
    }
    return $count
}

function Get-StackProcesses {
    param([string[]]$Names)

    $seen = @{}
    foreach ($name in $Names) {
        foreach ($process in @(Get-Process -Name $name -ErrorAction SilentlyContinue)) {
            if (-not $seen.ContainsKey($process.Id)) {
                $seen[$process.Id] = $process
            }
        }
    }
    return @($seen.Values)
}

function Stop-StackProcess {
    param([string]$Name)
    Get-Process -Name $Name -ErrorAction SilentlyContinue | ForEach-Object {
        Write-WatchdogLog "stopping stale process $Name pid=$($_.Id)"
        Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    }
}

function Stop-StackProcesses {
    param([string[]]$Names)
    foreach ($name in $Names) {
        Stop-StackProcess -Name $name
    }
}

function Start-Validator {
    if (-not (Test-Path -LiteralPath $ValidatorScript)) {
        Write-WatchdogLog "missing validator script: $ValidatorScript"
        return
    }
    Write-WatchdogLog "starting validator mode=$ValidatorMode block_producer=$ValidatorBlockProducer"
    $stdout = Join-Path $LocalRoot "watchdog-validator-start.out.log"
    $stderr = Join-Path $LocalRoot "watchdog-validator-start.err.log"
    $argString = "-NoProfile -ExecutionPolicy Bypass -File $(Quote-Arg $ValidatorScript) -QsdmRoot $(Quote-Arg $QsdmRoot) -HealthWaitSeconds $ValidatorStartupGraceSeconds"
    if ($ValidatorMode -eq "networked") {
        $argString += " -Networked -ChainSyncUrls $(Quote-Arg $ValidatorChainSyncUrls)"
        if (-not [string]::IsNullOrWhiteSpace($ValidatorBootstrapPeers)) {
            $argString += " -BootstrapPeers $(Quote-Arg $ValidatorBootstrapPeers)"
        }
        if ($ValidatorPublicP2P) {
            $argString += " -PublicP2P"
        }
        if ($ValidatorBlockProducer) {
            $argString += " -BlockProducer"
        }
    }
    $process = Start-Process `
        -FilePath "powershell.exe" `
        -ArgumentList $argString `
        -WorkingDirectory $QsdmRoot `
        -WindowStyle Hidden `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr `
        -PassThru
    $launcherWaitMilliseconds = ($ValidatorStartupGraceSeconds + 30) * 1000
    if (-not $process.WaitForExit($launcherWaitMilliseconds)) {
        Write-WatchdogLog "validator launcher timed out pid=$($process.Id)"
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        return
    }
    $process.Refresh()
    Write-WatchdogLog "validator launcher exited code=$($process.ExitCode)"
}

function Quote-Arg {
    param([string]$Value)
    return '"' + ($Value -replace '"', '\"') + '"'
}

function Start-Gateway {
    if (-not (Test-Path -LiteralPath $GatewayScript)) {
        Write-WatchdogLog "missing gateway script: $GatewayScript"
        return $false
    }
    $stdout = Join-Path $LocalRoot "home-gateway.out.log"
    $stderr = Join-Path $LocalRoot "home-gateway.err.log"
    $argString = "-NoProfile -ExecutionPolicy Bypass -File $(Quote-Arg $GatewayScript) -Relay $(Quote-Arg $Relay) -Slot $(Quote-Arg $Slot) -Backend $(Quote-Arg $Backend)"
    Write-WatchdogLog "starting home gateway relay=$Relay slot=$Slot"
    try {
        $process = Start-Process `
            -FilePath "powershell.exe" `
            -ArgumentList $argString `
            -WorkingDirectory $QsdmRoot `
            -WindowStyle Hidden `
            -RedirectStandardOutput $stdout `
            -RedirectStandardError $stderr `
            -PassThru
    } catch {
        Write-WatchdogLog "home gateway launcher could not start: $($_.Exception.Message)"
        return $false
    }
    Write-WatchdogLog "home gateway launcher pid=$($process.Id)"

    if (-not $process.WaitForExit($GatewayLauncherWaitSeconds * 1000)) {
        $gatewayProcesses = @(Get-StackProcesses -Names $GatewayProcessNames)
        if ($gatewayProcesses.Count -gt 0) {
            Write-WatchdogLog "home gateway became active while launcher remained open pid=$($gatewayProcesses[0].Id)"
            return $true
        }
        Write-WatchdogLog "home gateway launcher timed out pid=$($process.Id)"
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        return $false
    }

    $process.Refresh()
    if ($process.ExitCode -ne 0) {
        Write-WatchdogLog "home gateway launcher exited code=$($process.ExitCode); see $stderr"
        return $false
    }

    $startupDeadline = (Get-Date).AddSeconds(10)
    do {
        $gatewayProcesses = @(Get-StackProcesses -Names $GatewayProcessNames)
        if ($gatewayProcesses.Count -gt 0) {
            Write-WatchdogLog "home gateway active pid=$($gatewayProcesses[0].Id)"
            return $true
        }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $startupDeadline)

    Write-WatchdogLog "home gateway launcher exited successfully but no gateway process became active"
    return $false
}

function Invoke-GeneratedCacheMaintenance {
    if (-not (Test-Path -LiteralPath $CacheMaintenanceScript -PathType Leaf)) {
        Write-WatchdogLog "generated cache maintenance script is missing: $CacheMaintenanceScript"
        return
    }
    try {
        $raw = (& $CacheMaintenanceScript -QsdmRoot $QsdmRoot `
            -MinimumFreeGiB $MinimumFreeGiB -TargetFreeGiB $TargetFreeGiB `
            -Apply) -join "`n"
        $result = $raw | ConvertFrom-Json
        if ([int]$result.removed_count -gt 0 -or [bool]$result.disk_pressure) {
            Write-WatchdogLog ("generated cache maintenance removed_count={0} removed_bytes={1} initial_free_bytes={2} final_free_bytes={3} reserve_satisfied={4}" -f `
                $result.removed_count, $result.removed_bytes, $result.initial_free_bytes, `
                $result.final_free_bytes, $result.reserve_satisfied)
        }
    } catch {
        Write-WatchdogLog "generated cache maintenance failed: $($_.Exception.Message)"
    }
}

$mutex = [System.Threading.Mutex]::new($false, "Local\QSDMLocalStackWatchdog")
if (-not $mutex.WaitOne(0)) {
    Write-WatchdogLog "another watchdog instance is already running"
    exit 0
}
Set-Content -LiteralPath $PidPath -Value ([string]$PID)
Write-WatchdogIdentity

$validatorFailures = 0
$gatewayFailures = 0
$gatewayLaunchFailures = 0
$nextGatewayLaunchAt = [DateTime]::MinValue
$lastCacheMaintenance = [DateTime]::MinValue

try {
    Write-WatchdogLog "watchdog started root=$QsdmRoot relay=$Relay slot=$Slot check_public_gateway=$PublicGatewayCheckEnabled validator_startup_grace_seconds=$ValidatorStartupGraceSeconds gateway_retry_initial_seconds=$GatewayRetryInitialSeconds gateway_retry_max_seconds=$GatewayRetryMaxSeconds once=$Once"
    do {
        try {
            if (((Get-Date) - $lastCacheMaintenance).TotalMinutes -ge $CacheMaintenanceMinutes) {
                Invoke-GeneratedCacheMaintenance
                $lastCacheMaintenance = Get-Date
            }
            $validatorReady = Test-HttpOk -Url $ReadyUrl -TimeoutSeconds 5
            if ($validatorReady) {
                $validatorFailures = 0
            } else {
                $validatorFailures++
                $validatorProcesses = @(Get-StackProcesses -Names $ValidatorProcessNames)
                $validatorCount = $validatorProcesses.Count
                if ($validatorCount -eq 0) {
                    Write-WatchdogLog "validator not ready failure=$validatorFailures process_count=0; starting validator"
                    Start-Validator
                    Start-Sleep -Seconds 2
                    $validatorReady = Test-HttpOk -Url $ReadyUrl -TimeoutSeconds 5
                    $validatorFailures = if ($validatorReady) { 0 } else { 1 }
                } else {
                    $newestStartTime = @($validatorProcesses | ForEach-Object {
                        try { $_.StartTime } catch { [DateTime]::MinValue }
                    } | Sort-Object -Descending | Select-Object -First 1)
                    $startupAgeSeconds = if ($newestStartTime.Count -eq 1 -and $newestStartTime[0] -ne [DateTime]::MinValue) {
                        [int]((Get-Date) - $newestStartTime[0]).TotalSeconds
                    } else {
                        $ValidatorStartupGraceSeconds
                    }
                    if ($startupAgeSeconds -lt $ValidatorStartupGraceSeconds) {
                        Write-WatchdogLog "validator not ready but startup grace is active process_count=$validatorCount age_seconds=$startupAgeSeconds grace_seconds=$ValidatorStartupGraceSeconds"
                        $validatorFailures = 0
                    } elseif ($validatorFailures -ge $RestartAfterFailures) {
                        Write-WatchdogLog "validator remained unready after startup grace failure=$validatorFailures process_count=$validatorCount age_seconds=$startupAgeSeconds; restarting"
                        Stop-StackProcesses -Names $ValidatorProcessNames
                        Start-Validator
                        Start-Sleep -Seconds 2
                        $validatorReady = Test-HttpOk -Url $ReadyUrl -TimeoutSeconds 5
                        $validatorFailures = if ($validatorReady) { 0 } else { 1 }
                    } else {
                        Write-WatchdogLog "validator not ready after startup grace failure=$validatorFailures process_count=$validatorCount age_seconds=$startupAgeSeconds"
                    }
                }
            }

            $gatewayProcesses = @(Get-StackProcesses -Names $GatewayProcessNames | Sort-Object StartTime -Descending)
            $gatewayCount = $gatewayProcesses.Count
            if ($validatorReady -and $gatewayCount -eq 0) {
                if ((Get-Date) -ge $nextGatewayLaunchAt) {
                    $gatewayStarted = Start-Gateway
                    if ($gatewayStarted) {
                        $gatewayFailures = 0
                        $gatewayLaunchFailures = 0
                        $nextGatewayLaunchAt = [DateTime]::MinValue
                    } else {
                        $gatewayLaunchFailures++
                        $backoffPower = [Math]::Min($gatewayLaunchFailures - 1, 10)
                        $retrySeconds = [int][Math]::Min(
                            $GatewayRetryMaxSeconds,
                            $GatewayRetryInitialSeconds * [Math]::Pow(2, $backoffPower)
                        )
                        $nextGatewayLaunchAt = (Get-Date).AddSeconds($retrySeconds)
                        Write-WatchdogLog "home gateway launch failed count=$gatewayLaunchFailures; retrying in $retrySeconds second(s)"
                    }
                }
            } elseif ($validatorReady -and $gatewayCount -gt 1) {
                $keep = $gatewayProcesses[0].Id
                Write-WatchdogLog "multiple home gateways detected count=$gatewayCount keeping_pid=$keep"
                $gatewayProcesses | Select-Object -Skip 1 | ForEach-Object {
                    Write-WatchdogLog "stopping duplicate home gateway pid=$($_.Id)"
                    Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
                }
                $gatewayFailures = 0
                $gatewayLaunchFailures = 0
                $nextGatewayLaunchAt = [DateTime]::MinValue
            } elseif ($validatorReady -and $gatewayCount -eq 1 -and $PublicGatewayCheckEnabled) {
                $gatewayLaunchFailures = 0
                $nextGatewayLaunchAt = [DateTime]::MinValue
                if (Test-PublicGatewayOk) {
                    if ($gatewayFailures -gt 0) {
                        Write-WatchdogLog "gateway public check recovered after $gatewayFailures failure(s)"
                    }
                    $gatewayFailures = 0
                } else {
                    $gatewayFailures++
                    if ($gatewayFailures -ge $GatewayRestartAfterFailures) {
                        Write-WatchdogLog "gateway public check failed failure=$gatewayFailures url=$PublicUrl; restarting stale tunnel"
                        Stop-StackProcesses -Names $GatewayProcessNames
                        $gatewayStarted = Start-Gateway
                        Start-Sleep -Seconds 2
                        $gatewayFailures = 0
                        if (-not $gatewayStarted) {
                            $gatewayLaunchFailures = 1
                            $retrySeconds = [Math]::Min($GatewayRetryMaxSeconds, $GatewayRetryInitialSeconds)
                            $nextGatewayLaunchAt = (Get-Date).AddSeconds($retrySeconds)
                            Write-WatchdogLog "home gateway restart failed; retrying in $retrySeconds second(s)"
                        }
                    } elseif ($gatewayFailures -eq 1) {
                        Write-WatchdogLog "gateway public check failed failure=$gatewayFailures url=$PublicUrl; waiting before recovery"
                    }
                }
            }
        } catch {
            Write-WatchdogLog "watchdog loop error: $($_.Exception.Message)"
        }

        if ($Once) {
            break
        }
        Start-Sleep -Seconds $IntervalSeconds
    } while ($true)
} finally {
    Write-WatchdogLog "watchdog stopped"
    if (Test-Path -LiteralPath $PidPath) {
        $currentPid = (Get-Content -LiteralPath $PidPath -Raw).Trim()
        if ($currentPid -eq [string]$PID) {
            Remove-Item -LiteralPath $PidPath -Force -ErrorAction SilentlyContinue
        }
    }
    if (Test-Path -LiteralPath $IdentityPath) {
        try {
            $identity = Get-Content -LiteralPath $IdentityPath -Raw | ConvertFrom-Json
            if ([int]$identity.pid -eq $PID) {
                Remove-Item -LiteralPath $IdentityPath -Force -ErrorAction SilentlyContinue
            }
        } catch {
            Write-WatchdogLog "could not parse watchdog process identity while clearing it: $($_.Exception.Message)"
        }
    }
    $mutex.ReleaseMutex() | Out-Null
    $mutex.Dispose()
}
