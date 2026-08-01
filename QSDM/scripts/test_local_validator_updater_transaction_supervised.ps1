#requires -Version 5.1

[CmdletBinding()]
param(
    [string]$QsdmRoot = "",
    [ValidateRange(30, 180)]
    [int]$TimeoutSeconds = 75,
    [switch]$KeepArtifacts
)

if ([string]::IsNullOrWhiteSpace($QsdmRoot)) {
    $scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
    $QsdmRoot = (Resolve-Path (Join-Path $scriptDirectory "..")).Path
}

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$TestScript = Join-Path $QsdmRoot "scripts\test_local_validator_updater_transaction.ps1"
$TestID = [guid]::NewGuid().ToString("N")
$SupervisorRoot = Join-Path ([IO.Path]::GetTempPath()) "qsdm-updater-supervisor-$TestID"
$TransactionRoot = Join-Path ([IO.Path]::GetTempPath()) "qsdm-updater-transaction-$TestID"
$StdoutPath = Join-Path $SupervisorRoot "stdout.log"
$StderrPath = Join-Path $SupervisorRoot "stderr.log"
$process = $null
$succeeded = $false

function Assert-DisposablePath {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Prefix
    )
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    $resolved = [IO.Path]::GetFullPath($Path)
    if (-not $resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetFileName($resolved) -notlike "$Prefix*") {
        throw "Refusing to manage a test path outside the disposable temp namespace: $resolved"
    }
}

function Stop-ProcessTree {
    param([Parameter(Mandatory)][int]$ProcessID)
    $taskkill = Join-Path $env:SystemRoot "System32\taskkill.exe"
    if (-not (Test-Path -LiteralPath $taskkill -PathType Leaf)) {
        Stop-Process -Id $ProcessID -Force -ErrorAction SilentlyContinue
        return
    }
    $killer = Start-Process `
        -FilePath $taskkill `
        -ArgumentList "/PID $ProcessID /T /F" `
        -WindowStyle Hidden `
        -PassThru
    if (-not $killer.WaitForExit(10000)) {
        Stop-Process -Id $killer.Id -Force -ErrorAction SilentlyContinue
    }
}

Assert-DisposablePath -Path $SupervisorRoot -Prefix "qsdm-updater-supervisor-"
Assert-DisposablePath -Path $TransactionRoot -Prefix "qsdm-updater-transaction-"

try {
    New-Item -ItemType Directory -Force -Path $SupervisorRoot | Out-Null
    $hostPath = (Get-Process -Id $PID).Path
    if ([string]::IsNullOrWhiteSpace($hostPath) -or
        -not (Test-Path -LiteralPath $hostPath -PathType Leaf)) {
        throw "Could not resolve the current PowerShell executable."
    }

    $arguments = @(
        "-NoProfile",
        "-ExecutionPolicy", "Bypass",
        "-File", "`"$TestScript`"",
        "-QsdmRoot", "`"$QsdmRoot`"",
        "-TestRoot", "`"$TransactionRoot`""
    )
    if ($KeepArtifacts) {
        $arguments += "-KeepArtifacts"
    }

    $process = Start-Process `
        -FilePath $hostPath `
        -ArgumentList ($arguments -join " ") `
        -WorkingDirectory $QsdmRoot `
        -RedirectStandardOutput $StdoutPath `
        -RedirectStandardError $StderrPath `
        -WindowStyle Hidden `
        -PassThru
    # Windows PowerShell 5.1 can lose ExitCode unless the native process
    # handle is materialized before the child exits.
    [void]$process.Handle

    if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
        Stop-ProcessTree -ProcessID $process.Id
        throw "Transaction rehearsal exceeded its hard $TimeoutSeconds-second deadline and its disposable process tree was terminated."
    }
    $process.WaitForExit()
    $process.Refresh()
    $exitCode = $process.ExitCode

    $stdout = if (Test-Path -LiteralPath $StdoutPath) {
        Get-Content -LiteralPath $StdoutPath -Raw
    } else {
        ""
    }
    $stderr = if (Test-Path -LiteralPath $StderrPath) {
        Get-Content -LiteralPath $StderrPath -Raw
    } else {
        ""
    }
    if ($null -eq $exitCode) {
        throw "Transaction rehearsal exited but Windows did not provide an exit code."
    }
    if ($exitCode -ne 0) {
        $detail = ($stdout + "`n" + $stderr).Trim()
        throw "Transaction rehearsal failed with exit code ${exitCode}: $detail"
    }

    $succeeded = $true
    $stdout -split "`r?`n" |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) } |
        ForEach-Object { Write-Output $_ }
} finally {
    if ($null -ne $process -and -not $process.HasExited) {
        Stop-ProcessTree -ProcessID $process.Id
    }
    if ($KeepArtifacts -or -not $succeeded) {
        Write-Host "Transaction supervisor artifacts retained at $SupervisorRoot"
        if (Test-Path -LiteralPath $TransactionRoot) {
            Write-Host "Transaction test artifacts retained at $TransactionRoot"
        }
    } else {
        foreach ($path in @($TransactionRoot, $SupervisorRoot)) {
            $prefix = if ($path -eq $TransactionRoot) {
                "qsdm-updater-transaction-"
            } else {
                "qsdm-updater-supervisor-"
            }
            Assert-DisposablePath -Path $path -Prefix $prefix
            if (Test-Path -LiteralPath $path) {
                Remove-Item -LiteralPath $path -Recurse -Force -ErrorAction SilentlyContinue
            }
        }
    }
}
