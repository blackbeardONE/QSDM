param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$Relay = "https://api.qsdm.tech",
    [string]$Slot = "home-validator",
    [string]$Backend = "http://127.0.0.1:8080",
    [int]$IntervalSeconds = 30,
    [int]$RestartAfterFailures = 10,
    [switch]$CheckPublicGateway,
    [switch]$Once
)

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$ModeConfigPath = Join-Path $LocalRoot "validator-mode.json"
$ValidatorMode = "solo"
$ValidatorChainSyncUrls = "https://api.qsdm.tech/api/v1"
$ValidatorBootstrapPeers = ""
$ValidatorPublicP2P = $false
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
        }
    } catch {
        throw "Invalid validator mode config at ${ModeConfigPath}: $($_.Exception.Message)"
    }
}
$RunDirName = if ($ValidatorMode -eq "networked") { "run-networked" } else { "run-v2" }
$RunDir = Join-Path $LocalRoot $RunDirName
$LogPath = Join-Path $LocalRoot "watchdog.log"
$PidPath = Join-Path $LocalRoot "watchdog.pid"
$ValidatorScript = Join-Path $QsdmRoot "scripts\start_local_validator.ps1"
$GatewayScript = Join-Path $QsdmRoot "scripts\start_home_gateway.ps1"
$ReadyUrl = "$Backend/api/v1/health/ready"
$PublicUrl = "$Relay/attest/$Slot/api/v1/status"
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
$GatewayProcessNames = @(
    "qsdm-home-gateway",
    "qsdm-home-gateway-hive",
    "qsdm-home-gateway-hive.new"
)

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
    Write-WatchdogLog "starting validator mode=$ValidatorMode"
    $stdout = Join-Path $LocalRoot "watchdog-validator-start.out.log"
    $stderr = Join-Path $LocalRoot "watchdog-validator-start.err.log"
    $argString = "-NoProfile -ExecutionPolicy Bypass -File $(Quote-Arg $ValidatorScript) -QsdmRoot $(Quote-Arg $QsdmRoot)"
    if ($ValidatorMode -eq "networked") {
        $argString += " -Networked -ChainSyncUrls $(Quote-Arg $ValidatorChainSyncUrls)"
        if (-not [string]::IsNullOrWhiteSpace($ValidatorBootstrapPeers)) {
            $argString += " -BootstrapPeers $(Quote-Arg $ValidatorBootstrapPeers)"
        }
        if ($ValidatorPublicP2P) {
            $argString += " -PublicP2P"
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
    if (-not $process.WaitForExit(70000)) {
        Write-WatchdogLog "validator launcher timed out pid=$($process.Id)"
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        return
    }
    Write-WatchdogLog "validator launcher exited code=$($process.ExitCode)"
}

function Quote-Arg {
    param([string]$Value)
    return '"' + ($Value -replace '"', '\"') + '"'
}

function Start-Gateway {
    if (-not (Test-Path -LiteralPath $GatewayScript)) {
        Write-WatchdogLog "missing gateway script: $GatewayScript"
        return
    }
    $stdout = Join-Path $LocalRoot "home-gateway.out.log"
    $stderr = Join-Path $LocalRoot "home-gateway.err.log"
    $argString = "-NoProfile -ExecutionPolicy Bypass -File $(Quote-Arg $GatewayScript) -Relay $(Quote-Arg $Relay) -Slot $(Quote-Arg $Slot) -Backend $(Quote-Arg $Backend)"
    Write-WatchdogLog "starting home gateway relay=$Relay slot=$Slot"
    $process = Start-Process `
        -FilePath "powershell.exe" `
        -ArgumentList $argString `
        -WorkingDirectory $QsdmRoot `
        -WindowStyle Hidden `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr `
        -PassThru
    Write-WatchdogLog "home gateway launcher pid=$($process.Id)"
}

$mutex = [System.Threading.Mutex]::new($false, "Local\QSDMLocalStackWatchdog")
if (-not $mutex.WaitOne(0)) {
    Write-WatchdogLog "another watchdog instance is already running"
    exit 0
}
Set-Content -LiteralPath $PidPath -Value ([string]$PID)

$validatorFailures = 0
$gatewayFailures = 0

try {
    Write-WatchdogLog "watchdog started root=$QsdmRoot relay=$Relay slot=$Slot check_public_gateway=$($CheckPublicGateway.IsPresent) once=$Once"
    do {
        try {
            $validatorReady = Test-HttpOk -Url $ReadyUrl -TimeoutSeconds 5
            if ($validatorReady) {
                $validatorFailures = 0
            } else {
                $validatorFailures++
                $validatorCount = Get-ProcessCountAny -Names $ValidatorProcessNames
                Write-WatchdogLog "validator not ready failure=$validatorFailures process_count=$validatorCount"
                if ($validatorCount -eq 0 -or $validatorFailures -ge $RestartAfterFailures) {
                    Stop-StackProcesses -Names $ValidatorProcessNames
                    Start-Validator
                    Start-Sleep -Seconds 2
                    $validatorReady = Test-HttpOk -Url $ReadyUrl -TimeoutSeconds 5
                    $validatorFailures = 0
                }
            }

            $gatewayProcesses = @($GatewayProcessNames | ForEach-Object {
                Get-Process -Name $_ -ErrorAction SilentlyContinue
            } | Sort-Object StartTime -Descending)
            $gatewayCount = $gatewayProcesses.Count
            if ($validatorReady -and $gatewayCount -eq 0) {
                Start-Gateway
                $gatewayFailures = 0
            } elseif ($validatorReady -and $gatewayCount -gt 1) {
                $keep = $gatewayProcesses[0].Id
                Write-WatchdogLog "multiple home gateways detected count=$gatewayCount keeping_pid=$keep"
                $gatewayProcesses | Select-Object -Skip 1 | ForEach-Object {
                    Write-WatchdogLog "stopping duplicate home gateway pid=$($_.Id)"
                    Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
                }
                $gatewayFailures = 0
            } elseif ($validatorReady -and $gatewayCount -eq 1 -and $CheckPublicGateway) {
                if (Test-HttpOk -Url $PublicUrl -TimeoutSeconds 10) {
                    if ($gatewayFailures -gt 0) {
                        Write-WatchdogLog "gateway public check recovered after $gatewayFailures failure(s)"
                    }
                    $gatewayFailures = 0
                } else {
                    $gatewayFailures++
                    if ($gatewayFailures -eq 1 -or ($gatewayFailures % $RestartAfterFailures) -eq 0) {
                        Write-WatchdogLog "gateway public check failed failure=$gatewayFailures url=$PublicUrl; leaving gateway running"
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
    $mutex.ReleaseMutex() | Out-Null
    $mutex.Dispose()
}
