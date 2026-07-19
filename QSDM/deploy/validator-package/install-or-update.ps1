#requires -Version 5.1
#requires -RunAsAdministrator

[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $env:ProgramData "QSDM\Validator"),
    [string]$DataDir = (Join-Path $env:ProgramData "QSDM\ValidatorData"),
    [string]$ConfigPath = "",
    [string]$TaskName = "QSDM-Validator",
    [string]$HealthUrl = "http://127.0.0.1:8080/api/v1/health/live",
    [ValidateRange(1, 3600)][int]$HealthTimeoutSeconds = 120,
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"
$ScriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$SourceBinary = Join-Path $ScriptRoot "qsdm-validator.exe"
$ChecksumFile = Join-Path $ScriptRoot "SHA256SUMS.txt"

function Test-ReparsePoint {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return $false }
    return [bool]((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)
}

function Set-ManagedDirectoryAcl {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][ValidateSet('RX', 'M')][string]$ServiceAccess
    )

    $icacls = Join-Path $env:SystemRoot 'System32\icacls.exe'
    $grants = @(
        '*S-1-5-18:(OI)(CI)F',
        '*S-1-5-32-544:(OI)(CI)F',
        "*S-1-5-19:(OI)(CI)$ServiceAccess"
    )
    & $icacls $Path '/inheritance:r' '/grant:r' $grants | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not secure directory ACL: $Path" }
    & $icacls $Path '/setowner' '*S-1-5-18' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not set directory owner: $Path" }
}

function Write-ValidatorInstallState {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][object]$State
    )

    $stateTemp = "$Path.tmp.$PID"
    [IO.File]::WriteAllText($stateTemp, ($State | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $stateTemp -Destination $Path -Force
}

function Stop-InstalledValidator {
    param([string]$ScheduledTaskName, [string]$ExecutablePath)
    $task = Get-ScheduledTask -TaskName $ScheduledTaskName -ErrorAction SilentlyContinue
    if ($task) {
        Stop-ScheduledTask -TaskName $ScheduledTaskName -ErrorAction SilentlyContinue
    }
    $target = [IO.Path]::GetFullPath($ExecutablePath)
    Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath) -eq $target } |
        ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
}

function Resolve-LoopbackHealthEndpoint {
    param([Parameter(Mandatory)][string]$Url)

    $match = [regex]::Match(
        $Url,
        '^https?://(?:127\.0\.0\.1|localhost|\[::1\]):(?<port>[0-9]{1,5})(?:/[A-Za-z0-9._~/%-]*)?\z',
        [Text.RegularExpressions.RegexOptions]::IgnoreCase
    )
    if (-not $match.Success) {
        throw "-HealthUrl must be an explicit loopback HTTP(S) URL with a port and no query or fragment."
    }

    $uri = $null
    if (-not [Uri]::TryCreate($Url, [UriKind]::Absolute, [ref]$uri)) {
        throw "Invalid -HealthUrl."
    }
    $port = [int]$match.Groups['port'].Value
    if ($port -lt 1 -or $port -gt 65535) { throw "-HealthUrl port is out of range." }

    return [pscustomobject]@{ Uri = $uri; Port = $port }
}

function Wait-ValidatorLive {
    param([Uri]$Url, [int]$Port, [int]$TimeoutSeconds, [string]$ExecutablePath)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    $target = [IO.Path]::GetFullPath($ExecutablePath)
    while ([DateTime]::UtcNow -lt $deadline) {
        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 5
            $processIds = @(
                Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
                    Where-Object { $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath) -eq $target } |
                    ForEach-Object { [uint32]$_.ProcessId }
            )
            $ownsListener = if ($processIds.Count -gt 0) {
                Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue |
                    Where-Object { $processIds -contains [uint32]$_.OwningProcess } |
                    Select-Object -First 1
            }
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300 -and $ownsListener) {
                return $true
            }
        } catch {}
        Start-Sleep -Seconds 2
    }
    return $false
}

if (-not [IO.Path]::IsPathRooted($InstallDir)) { throw "-InstallDir must be absolute." }
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
$InstallRoot = [IO.Path]::GetPathRoot($InstallDir)
if ($InstallDir.TrimEnd('\') -eq $InstallRoot.TrimEnd('\')) { throw "Refusing to install at a drive root." }
if ($InstallRoot -notmatch '^[A-Za-z]:\\$') { throw "-InstallDir must be on a local drive." }
$InstallDir = $InstallDir.TrimEnd('\')
if (Test-Path -LiteralPath $InstallDir) {
    if (-not (Test-Path -LiteralPath $InstallDir -PathType Container) -or (Test-ReparsePoint -Path $InstallDir)) {
        throw "Refusing a non-directory or reparse-point install path: $InstallDir"
    }
    $prospectiveState = Join-Path $InstallDir 'validator-install-state.json'
    $unmanagedEntry = Get-ChildItem -LiteralPath $InstallDir -Force | Select-Object -First 1
    if (-not (Test-Path -LiteralPath $prospectiveState -PathType Leaf) -and $unmanagedEntry) {
        throw "Refusing to adopt a non-empty install directory without QSDM install state: $InstallDir"
    }
}
if (-not (Test-Path -LiteralPath $SourceBinary -PathType Leaf) -or (Test-ReparsePoint -Path $SourceBinary)) {
    throw "Package binary is missing or is a reparse point: $SourceBinary"
}
if (-not (Test-Path -LiteralPath $ChecksumFile -PathType Leaf) -or (Test-ReparsePoint -Path $ChecksumFile)) {
    throw "Package checksum file is missing or is a reparse point."
}

$checksumLine = Get-Content -LiteralPath $ChecksumFile |
    Where-Object { $_ -match '^([0-9a-fA-F]{64})\s+\*?qsdm-validator\.exe$' } |
    Select-Object -First 1
if (-not $checksumLine) { throw "SHA256SUMS.txt has no valid qsdm-validator.exe entry." }
$expectedHash = ([regex]::Match($checksumLine, '^([0-9a-fA-F]{64})')).Groups[1].Value.ToLowerInvariant()
$actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $SourceBinary).Hash.ToLowerInvariant()
if ($actualHash -ne $expectedHash) { throw "Package binary checksum mismatch." }

$versionOutput = ((& $SourceBinary --version 2>&1) | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $versionOutput -notmatch '^qsdm\s') {
    throw "Package binary did not return canonical qsdm version metadata."
}
Write-Host "Verified package: $versionOutput" -ForegroundColor Green

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Set-ManagedDirectoryAcl -Path $InstallDir -ServiceAccess RX
$TargetBinary = Join-Path $InstallDir "qsdm-validator.exe"
$StateFile = Join-Path $InstallDir "validator-install-state.json"
$RunScript = Join-Path $InstallDir "run-validator.ps1"
$previousState = $null
if (Test-Path -LiteralPath $StateFile) {
    if (-not (Test-Path -LiteralPath $StateFile -PathType Leaf) -or (Test-ReparsePoint -Path $StateFile)) {
        throw "Refusing a non-file or reparse-point install state: $StateFile"
    }
    try { $previousState = Get-Content -Raw -LiteralPath $StateFile | ConvertFrom-Json } catch {
        throw "Invalid validator install state: $StateFile"
    }
}
if (-not $PSBoundParameters.ContainsKey('TaskName') -and $previousState -and $previousState.task) {
    $TaskName = [string]$previousState.task
}
if (-not $PSBoundParameters.ContainsKey('HealthUrl') -and $previousState -and $previousState.health) {
    $HealthUrl = [string]$previousState.health
}
if (-not $PSBoundParameters.ContainsKey('DataDir') -and $previousState -and $previousState.data) {
    $DataDir = [string]$previousState.data
}
if ($TaskName -notmatch '^[A-Za-z0-9_. -]+$') { throw "Invalid -TaskName." }
$healthEndpoint = Resolve-LoopbackHealthEndpoint -Url $HealthUrl
if (-not [IO.Path]::IsPathRooted($DataDir)) { throw "-DataDir must be absolute." }
$DataDir = [IO.Path]::GetFullPath($DataDir).TrimEnd('\')
$DataRoot = [IO.Path]::GetPathRoot($DataDir)
if ($DataRoot -notmatch '^[A-Za-z]:\\$') { throw "-DataDir must be on a local drive." }
$dataIsInstallAncestor = $InstallDir.StartsWith($DataDir + '\', [StringComparison]::OrdinalIgnoreCase)
$dataIsInsideInstall = $DataDir.StartsWith($InstallDir + '\', [StringComparison]::OrdinalIgnoreCase)
if ($DataDir -eq $InstallDir -or $dataIsInstallAncestor -or $dataIsInsideInstall -or $DataDir.TrimEnd('\') -eq $DataRoot.TrimEnd('\')) {
    throw "-DataDir must not overlap the install directory or be a drive root."
}
$recordedDataDir = if ($previousState -and $previousState.data) {
    [IO.Path]::GetFullPath([string]$previousState.data).TrimEnd('\')
} else { '' }
if (Test-Path -LiteralPath $DataDir) {
    if (-not (Test-Path -LiteralPath $DataDir -PathType Container) -or (Test-ReparsePoint -Path $DataDir)) {
        throw "Refusing a non-directory or reparse-point data path: $DataDir"
    }
    $sameRecordedData = [string]::Equals($recordedDataDir, $DataDir, [StringComparison]::OrdinalIgnoreCase)
    $unmanagedData = -not $sameRecordedData -and
        [bool](Get-ChildItem -LiteralPath $DataDir -Force | Select-Object -First 1)
    if ($unmanagedData) { throw "Refusing to adopt a non-empty unmanaged data directory: $DataDir" }
}

$ConfigNeedsCopy = $false
if ($ConfigPath) {
    $ConfigPath = (Resolve-Path -LiteralPath $ConfigPath).Path
    if (Test-ReparsePoint -Path $ConfigPath) { throw "Refusing a reparse-point config: $ConfigPath" }
    $extension = [IO.Path]::GetExtension($ConfigPath).ToLowerInvariant()
    if ($extension -notin @('.toml', '.yaml', '.yml')) { throw "-ConfigPath must be TOML or YAML." }
    $ConfigTarget = Join-Path $InstallDir ("qsdm" + $extension)
    if (Test-Path -LiteralPath $ConfigTarget) {
        if ([IO.Path]::GetFullPath($ConfigPath) -ne [IO.Path]::GetFullPath($ConfigTarget)) {
            throw "Refusing to overwrite existing config: $ConfigTarget"
        }
    } else {
        $ConfigNeedsCopy = $true
    }
} elseif ($previousState -and $previousState.config -and (Test-Path -LiteralPath $previousState.config -PathType Leaf)) {
    $ConfigTarget = [string]$previousState.config
} else {
    $ConfigTarget = @(
        (Join-Path $InstallDir 'qsdm.toml'),
        (Join-Path $InstallDir 'qsdm.yaml'),
        (Join-Path $InstallDir 'qsdm.yml')
    ) | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    if (-not $ConfigTarget) { throw "Fresh install requires -ConfigPath PATH." }
}
if (Test-ReparsePoint -Path $ConfigTarget) { throw "Refusing a reparse-point config: $ConfigTarget" }

$ExistingTask = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($ExistingTask -and -not $previousState) {
    throw "Refusing to replace an existing scheduled task without QSDM install state: $TaskName"
}

# Persist an explicit in-progress marker before creating managed data or
# copying configuration. A retry can then resume safely after power loss
# without treating its own files as an unrelated installation.
if (-not $previousState) {
    Write-ValidatorInstallState -Path $StateFile -State ([ordered]@{
        status = 'installing'
        version = $versionOutput
        config = $ConfigTarget
        binary = $TargetBinary
        previous = ''
        previousSha256 = ''
        task = $TaskName
        health = $HealthUrl
        data = $DataDir
        installedAt = [DateTime]::UtcNow.ToString('o')
    })
}

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Set-ManagedDirectoryAcl -Path $DataDir -ServiceAccess M
if ($ConfigNeedsCopy) {
    Copy-Item -LiteralPath $ConfigPath -Destination $ConfigTarget
}

$StagedBinary = Join-Path $InstallDir (".qsdm-validator.staged.{0}.exe" -f $PID)
Copy-Item -LiteralPath $SourceBinary -Destination $StagedBinary -Force
$BackupPath = ""
$BackupHash = ""
$ReplacementInstalled = $false
$ExistingTaskXml = if ($ExistingTask) { Export-ScheduledTask -TaskName $TaskName } else { $null }
$WasTaskRunning = [bool]($ExistingTask -and $ExistingTask.State -eq 'Running')
try {
    $stagedVersion = ((& $StagedBinary --version 2>&1) | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $stagedVersion -notmatch '^qsdm\s') { throw "Staged binary smoke check failed." }

    $timestamp = "{0}.{1}" -f [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ'), $PID
    if (Test-Path -LiteralPath $TargetBinary) {
        if (-not (Test-Path -LiteralPath $TargetBinary -PathType Leaf) -or (Test-ReparsePoint -Path $TargetBinary)) {
            throw "Refusing to replace a non-file or reparse-point binary."
        }
        $BackupPath = Join-Path $InstallDir "qsdm-validator.backup.$timestamp.exe"
        Copy-Item -LiteralPath $TargetBinary -Destination $BackupPath
        $BackupHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $BackupPath).Hash.ToLowerInvariant()
    }

    Stop-InstalledValidator -ScheduledTaskName $TaskName -ExecutablePath $TargetBinary
    Move-Item -LiteralPath $StagedBinary -Destination $TargetBinary -Force
    $ReplacementInstalled = $true

    $escapedConfig = $ConfigTarget.Replace("'", "''")
    $escapedBinary = $TargetBinary.Replace("'", "''")
    $runner = @"
`$ErrorActionPreference = 'Stop'
`$env:CONFIG_FILE = '$escapedConfig'
`$env:QSDM_PRODUCTION_MODE = '1'
`$env:QSDM_REQUIRE_SQLITE_STORAGE = '1'
& '$escapedBinary'
exit `$LASTEXITCODE
"@
    [IO.File]::WriteAllText($RunScript, $runner, [Text.UTF8Encoding]::new($false))

    $arguments = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$RunScript`""
    $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $arguments -WorkingDirectory $DataDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $principal = New-ScheduledTaskPrincipal -UserId "NT AUTHORITY\LOCAL SERVICE" -LogonType ServiceAccount -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -StartWhenAvailable `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -MultipleInstances IgnoreNew `
        -RestartCount 999 `
        -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null

    if (-not $NoStart) {
        Start-ScheduledTask -TaskName $TaskName
        if (-not (Wait-ValidatorLive -Url $healthEndpoint.Uri -Port $healthEndpoint.Port -TimeoutSeconds $HealthTimeoutSeconds -ExecutablePath $TargetBinary)) {
            throw "Validator did not become live from the installed executable within $HealthTimeoutSeconds seconds."
        }
    }

    $state = [ordered]@{
        status = 'installed'
        version = $versionOutput
        config = $ConfigTarget
        binary = $TargetBinary
        previous = $BackupPath
        previousSha256 = $BackupHash
        task = $TaskName
        health = $HealthUrl
        data = $DataDir
        installedAt = [DateTime]::UtcNow.ToString('o')
    }
    Write-ValidatorInstallState -Path $StateFile -State $state

    Write-Host "Installed $versionOutput at $TargetBinary" -ForegroundColor Green
    if ($BackupPath) { Write-Host "Rollback copy: $BackupPath" }
} catch {
    $failure = $_
    Write-Warning "Installation failed; restoring the previous validator state."
    try { Stop-InstalledValidator -ScheduledTaskName $TaskName -ExecutablePath $TargetBinary } catch {}
    if ($ReplacementInstalled) {
        try {
            if ($BackupPath -and (Test-Path -LiteralPath $BackupPath -PathType Leaf)) {
                Copy-Item -LiteralPath $BackupPath -Destination $TargetBinary -Force
            } else {
                Remove-Item -LiteralPath $TargetBinary -Force -ErrorAction SilentlyContinue
            }
        } catch { Write-Warning "Could not restore the prior validator binary: $($_.Exception.Message)" }
    }
    try {
        if ($ExistingTaskXml) {
            Register-ScheduledTask -TaskName $TaskName -Xml $ExistingTaskXml -Force | Out-Null
            if ($WasTaskRunning) { Start-ScheduledTask -TaskName $TaskName }
        } else {
            Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
        }
    } catch { Write-Warning "Could not restore the prior scheduled task state: $($_.Exception.Message)" }
    throw $failure
} finally {
    Remove-Item -LiteralPath $StagedBinary -Force -ErrorAction SilentlyContinue
}
