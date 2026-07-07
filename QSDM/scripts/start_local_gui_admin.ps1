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

if (-not (Test-IsAdmin) -and -not $NoElevate) {
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

    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StartScript -QsdmRoot $QsdmRoot
    Start-Sleep -Seconds 2

    if (Test-Path -LiteralPath $UrlFile) {
        $url = (Get-Content -LiteralPath $UrlFile -Raw).Trim()
        Write-LaunchLog "admin GUI URL: $url"
        Write-Host "Admin GUI URL: $url"
        Start-Process $url
    } else {
        Write-LaunchLog "admin GUI started but URL file was not found: $UrlFile"
        Write-Host "Admin GUI started, but URL file was not found: $UrlFile"
    }

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
