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
$identityFile = Join-Path $runDir "signer.process.json"

function Remove-SignerProcessRecords {
    Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $identityFile -Force -ErrorAction SilentlyContinue
}

function ConvertTo-UtcTimestamp {
    param([Parameter(Mandatory)][object]$Value)

    if ($Value -is [DateTime]) {
        return ([DateTime]$Value).ToUniversalTime()
    }
    if ($Value -is [DateTimeOffset]) {
        return ([DateTimeOffset]$Value).UtcDateTime
    }
    return [DateTime]::Parse(
        [string]$Value,
        [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::RoundtripKind
    ).ToUniversalTime()
}

function Get-ManagedSignerProcess {
    if (-not (Test-Path -LiteralPath $pidFile -PathType Leaf)) {
        return $null
    }
    if ((Get-Item -LiteralPath $pidFile -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw "Refusing a reparse-point $Role signer PID file."
    }

    $existingPid = 0
    if (-not [int]::TryParse((Get-Content -LiteralPath $pidFile -Raw).Trim(), [ref]$existingPid) -or $existingPid -le 0) {
        Remove-SignerProcessRecords
        return $null
    }
    $candidate = Get-Process -Id $existingPid -ErrorAction SilentlyContinue
    if (-not $candidate) {
        Remove-SignerProcessRecords
        return $null
    }

    $expectedBinary = [IO.Path]::GetFullPath($binary)
    $expectedProcessName = [IO.Path]::GetFileNameWithoutExtension($expectedBinary)
    if ($candidate.ProcessName -ne $expectedProcessName) {
        Write-Warning "Ignoring stale $Role signer PID $existingPid because it belongs to process $($candidate.ProcessName)."
        Remove-SignerProcessRecords
        return $null
    }
    try {
        $actualBinary = [IO.Path]::GetFullPath($candidate.Path)
    } catch {
        throw "Cannot verify the $Role treasury signer PID $existingPid executable; refusing to stop it."
    }
    if ($actualBinary -ne $expectedBinary) {
        Write-Warning "Ignoring stale $Role signer PID $existingPid because it belongs to $actualBinary."
        Remove-SignerProcessRecords
        return $null
    }

    $actualStart = $candidate.StartTime.ToUniversalTime()
    $actualHash = (Get-FileHash -LiteralPath $expectedBinary -Algorithm SHA256).Hash.ToLowerInvariant()
    if (Test-Path -LiteralPath $identityFile -PathType Leaf) {
        if ((Get-Item -LiteralPath $identityFile -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw "Refusing a reparse-point $Role signer identity file."
        }
        $identity = Get-Content -LiteralPath $identityFile -Raw | ConvertFrom-Json
        $recordedStart = ConvertTo-UtcTimestamp -Value $identity.process_start_utc
        $identityPortMatches = if ($null -ne $identity.PSObject.Properties['port']) {
            [int]$identity.port -eq $Port
        } else {
            try {
                $legacyHealth = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/healthz" -TimeoutSec 2
                $legacyHealth.status -eq "ok" -and $legacyHealth.role -eq $Role
            } catch {
                $false
            }
        }
        if ([string]$identity.schema -ne "qsdm.treasury-signer-process.v1" -or
            [string]$identity.role -ne $Role -or
            -not $identityPortMatches -or
            [int]$identity.pid -ne $existingPid -or
            [IO.Path]::GetFullPath([string]$identity.binary) -ne $expectedBinary -or
            [Math]::Abs(($recordedStart - $actualStart).TotalSeconds) -gt 2 -or
            ([string]$identity.sha256).ToLowerInvariant() -ne $actualHash) {
            throw "The $Role treasury signer process identity does not match PID $existingPid; refusing to stop it."
        }
    } else {
        $pidFileWrite = (Get-Item -LiteralPath $pidFile).LastWriteTimeUtc
        if ([Math]::Abs(($pidFileWrite - $actualStart).TotalSeconds) -gt 15) {
            throw "Legacy $Role signer PID record is stale; refusing to stop PID $existingPid."
        }
    }
    return $candidate
}

function Write-SignerProcessIdentity {
    param([Parameter(Mandatory)][System.Diagnostics.Process]$Process)

    $identity = [ordered]@{
        schema = "qsdm.treasury-signer-process.v1"
        role = $Role
        port = $Port
        pid = $Process.Id
        process_start_utc = $Process.StartTime.ToUniversalTime().ToString("o")
        binary = [IO.Path]::GetFullPath($binary)
        sha256 = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
        launcher_pid = $PID
        written_at_utc = [DateTime]::UtcNow.ToString("o")
    }
    $tempPath = "$identityFile.tmp-$PID"
    [IO.File]::WriteAllText($tempPath, ($identity | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $tempPath -Destination $identityFile -Force
}
$existing = Get-ManagedSignerProcess
if ($existing -and -not $Restart) {
    throw "A $Role treasury signer is already running. Use -Restart to replace it."
}
if ($existing) {
    Stop-Process -Id $existing.Id -Force
    if (-not $existing.WaitForExit(10000)) {
        throw "$Role treasury signer PID $($existing.Id) did not stop within 10 seconds."
    }
    Remove-SignerProcessRecords
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
    Write-SignerProcessIdentity -Process $process
} finally {
    foreach ($key in $keys) { [Environment]::SetEnvironmentVariable($key, $saved[$key], "Process") }
}

$health = $null
$healthDeadline = [DateTime]::UtcNow.AddSeconds(10)
try {
    while ([DateTime]::UtcNow -lt $healthDeadline) {
        $process.Refresh()
        if ($process.HasExited) {
            throw "The $Role signer exited before becoming healthy (exit code $($process.ExitCode))."
        }
        try {
            $candidateHealth = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/healthz" -TimeoutSec 2
            if ($candidateHealth.status -eq "ok" -and $candidateHealth.role -eq $Role) {
                $health = $candidateHealth
                break
            }
        } catch {
            # The signer can need a short moment to bind its loopback listener.
        }
        Start-Sleep -Milliseconds 250
    }
    if ($null -eq $health) {
        throw "The $Role signer did not become healthy on loopback port $Port within 10 seconds."
    }
} catch {
    if (Get-Process -Id $process.Id -ErrorAction SilentlyContinue) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-SignerProcessRecords
    throw
}
Write-Host "QSDM $Role treasury signer is running"
Write-Host "  PID:       $($process.Id)"
Write-Host "  URL:       http://127.0.0.1:$Port"
Write-Host "  Address:   $($health.address)"
Write-Host "  Max:       $MaxPayout CELL per payout"
Write-Host "  Reserve:   $MinimumReserve CELL"
