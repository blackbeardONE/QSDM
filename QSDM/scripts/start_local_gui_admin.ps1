param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [switch]$NoElevate,
    [switch]$NoPause
)

$ErrorActionPreference = "Stop"

function Test-IsAdmin {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Quote-Arg {
    param([string]$Value)
    return '"' + ($Value -replace '"', '\"') + '"'
}

function Get-LocalGuiProcesses {
    param([string]$Root)

    $rootPrefix = [IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    @(
        Get-Process -ErrorAction SilentlyContinue | Where-Object {
            if ($_.ProcessName -notlike "qsdm-local-gui*") {
                return $false
            }
            try {
                $path = [IO.Path]::GetFullPath($_.Path)
                return $path.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)
            } catch {
                return $false
            }
        }
    )
}

function Stop-ExistingLocalGui {
    param(
        [string]$Root,
        [int]$TimeoutSeconds = 8
    )

    $targets = @(Get-LocalGuiProcesses -Root $Root)
    foreach ($target in $targets) {
        Write-LaunchLog "stopping existing local GUI pid=$($target.Id) path=$($target.Path)"
        Stop-Process -Id $target.Id -Force -ErrorAction Stop
    }

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $remaining = @(Get-LocalGuiProcesses -Root $Root)
        if ($remaining.Count -eq 0) {
            return
        }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)

    $remainingPids = (@(Get-LocalGuiProcesses -Root $Root) | ForEach-Object { $_.Id }) -join ", "
    throw "Existing local GUI process did not stop within $TimeoutSeconds seconds (pid: $remainingPids)."
}

function Get-QueryValue {
    param(
        [Uri]$Uri,
        [string]$Name
    )

    foreach ($item in $Uri.Query.TrimStart('?').Split('&', [StringSplitOptions]::RemoveEmptyEntries)) {
        $parts = $item.Split('=', 2)
        if ([Uri]::UnescapeDataString($parts[0]) -eq $Name) {
            if ($parts.Count -eq 1) {
                return ""
            }
            return [Uri]::UnescapeDataString($parts[1])
        }
    }
    return ""
}

function Wait-ForElevatedGui {
    param(
        [string]$UrlPath,
        [int]$TimeoutSeconds = 12
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    $lastError = "GUI URL was not created"
    do {
        if (Test-Path -LiteralPath $UrlPath) {
            $url = (Get-Content -LiteralPath $UrlPath -Raw).Trim()
            try {
                $uri = [Uri]$url
                if ($uri.Host -ne "127.0.0.1" -and $uri.Host -ne "localhost") {
                    throw "GUI URL is not loopback-only"
                }
                $token = Get-QueryValue -Uri $uri -Name "t"
                if ([string]::IsNullOrWhiteSpace($token)) {
                    throw "GUI URL is missing its access token"
                }
                $snapshotUrl = $uri.GetLeftPart([UriPartial]::Authority) + "/api/snapshot"
                $snapshot = Invoke-RestMethod -Uri $snapshotUrl -Headers @{ "X-QSDM-Token" = $token } -TimeoutSec 2
                if (-not [bool]$snapshot.admin.elevated) {
                    throw "replacement GUI reported a non-elevated process token"
                }
                return $url
            } catch {
                $lastError = $_.Exception.Message
            }
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)

    throw "Elevated local GUI did not become ready within $TimeoutSeconds seconds: $lastError"
}

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$LogPath = Join-Path $LocalRoot "local-gui-admin-launch.log"
$UrlFile = Join-Path $LocalRoot "local-gui-persist.url"
$StartScript = Join-Path $QsdmRoot "scripts\start_local_gui.ps1"
New-Item -ItemType Directory -Force -Path $LocalRoot | Out-Null

function Write-LaunchLog {
    param([string]$Message)
    $stamp = Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"
    Add-Content -LiteralPath $LogPath -Value "$stamp $Message"
}

if (-not (Test-IsAdmin)) {
    if ($NoElevate) {
        Write-LaunchLog "administrator elevation was not granted"
        throw "Administrator elevation was not granted. Approve the Windows UAC prompt and try again."
    }

    Write-LaunchLog "requesting administrator elevation"
    $args = "-NoProfile -ExecutionPolicy Bypass -NoExit -File $(Quote-Arg $PSCommandPath) -QsdmRoot $(Quote-Arg $QsdmRoot) -NoElevate"
    Start-Process -FilePath "powershell.exe" -Verb RunAs -ArgumentList $args
    Write-Host "Windows administrator prompt requested. If nothing appears, check UAC settings."
    exit 0
}

try {
    Write-LaunchLog "admin launcher started elevated=$(Test-IsAdmin)"
    if (-not (Test-Path -LiteralPath $StartScript)) {
        throw "Missing GUI start script: $StartScript"
    }

    Stop-ExistingLocalGui -Root $LocalRoot
    Remove-Item -LiteralPath $UrlFile -Force -ErrorAction SilentlyContinue

    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StartScript -QsdmRoot $QsdmRoot -NoOpen
    if ($LASTEXITCODE -ne 0) {
        throw "Local GUI start script exited with code $LASTEXITCODE."
    }

    $url = Wait-ForElevatedGui -UrlPath $UrlFile
    $displayUrl = ([Uri]$url).GetLeftPart([UriPartial]::Authority)
    Write-LaunchLog "verified elevated admin GUI at $displayUrl"
    Write-Host "Admin GUI verified and opening at $displayUrl"
    Start-Process $url

    Write-Host ""
    Write-Host "Admin GUI launcher finished. You can close this PowerShell window after the browser opens."
    Write-LaunchLog "admin launcher finished"
    if (-not $NoPause) {
        Write-Host "Leaving this window open for diagnostics."
    }
} catch {
    Write-LaunchLog "ERROR: $($_.Exception.Message)"
    Write-Host ""
    Write-Host "QSDM Admin GUI failed:" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    Write-Host "Log: $LogPath"
    if (-not $NoPause) {
        Read-Host "Press Enter to close"
    }
    exit 1
}
