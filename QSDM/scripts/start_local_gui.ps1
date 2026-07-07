param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [switch]$NoOpen,
    [switch]$NoStayOpen,
    [switch]$AllowParallel
)

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$UrlFile = Join-Path $LocalRoot "local-gui-persist.url"
$OutLog = Join-Path $LocalRoot "local-gui-persist.out.log"
$ErrLog = Join-Path $LocalRoot "local-gui-persist.err.log"

New-Item -ItemType Directory -Force -Path $LocalRoot | Out-Null

$candidates = @(
    "qsdm-local-gui-home-server.exe",
    "qsdm-local-gui-hive-v2.exe",
    "qsdm-local-gui-hive.exe",
    "qsdm-local-gui-persist.exe",
    "qsdm-local-gui-next.exe",
    "qsdm-local-gui-sqlite.exe",
    "qsdm-local-gui.exe"
)

$ExePath = $null
foreach ($candidate in $candidates) {
    $path = Join-Path $LocalRoot $candidate
    if (Test-Path -LiteralPath $path) {
        $ExePath = $path
        break
    }
}
if ($null -eq $ExePath) {
    throw "Missing local GUI executable in $LocalRoot"
}

$running = Get-Process -ErrorAction SilentlyContinue | Where-Object {
    $_.ProcessName -like "qsdm-local-gui*"
}
if ($running -and -not $AllowParallel) {
    Write-Host "QSDM local GUI is already running."
    exit 0
}

$env:QSDM_LOCAL_GUI_URL_FILE = $UrlFile
if ($NoStayOpen) {
    Remove-Item Env:\QSDM_LOCAL_GUI_STAY_OPEN -ErrorAction SilentlyContinue
} else {
    $env:QSDM_LOCAL_GUI_STAY_OPEN = "1"
}
if ($NoOpen) {
    $env:QSDM_LOCAL_GUI_NO_OPEN = "1"
} else {
    Remove-Item Env:\QSDM_LOCAL_GUI_NO_OPEN -ErrorAction SilentlyContinue
}

$env:HTTP_PROXY = ""
$env:HTTPS_PROXY = ""
$env:ALL_PROXY = ""
$env:NO_PROXY = "127.0.0.1,localhost,api.qsdm.tech"

$process = Start-Process `
    -FilePath $ExePath `
    -WorkingDirectory $QsdmRoot `
    -WindowStyle Hidden `
    -RedirectStandardOutput $OutLog `
    -RedirectStandardError $ErrLog `
    -PassThru

Write-Host "QSDM local GUI started pid=$($process.Id)"
