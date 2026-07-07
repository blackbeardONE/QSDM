param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$LauncherName = "QSDM-Local-GUI",
    [switch]$NoOpen,
    [switch]$NoRunNow
)

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$StartScript = Join-Path $QsdmRoot "scripts\start_local_gui.ps1"
if (-not (Test-Path -LiteralPath $StartScript)) {
    throw "Missing GUI start script: $StartScript"
}

$startup = [Environment]::GetFolderPath("Startup")
if ([string]::IsNullOrWhiteSpace($startup)) {
    throw "Could not locate the current user's Startup folder"
}
New-Item -ItemType Directory -Force -Path $startup | Out-Null

$launcher = Join-Path $startup "$LauncherName.vbs"
$args = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$StartScript`" -QsdmRoot `"$QsdmRoot`""
if ($NoOpen) {
    $args += " -NoOpen"
}
$command = "powershell.exe $args"
$vbsCommand = $command.Replace('"', '""')
Set-Content -LiteralPath $launcher -Encoding ASCII -Value @"
Set shell = CreateObject("WScript.Shell")
shell.Run "$vbsCommand", 0, False
"@

$legacy = Join-Path $startup "QSDM Local Validator.cmd"
if (Test-Path -LiteralPath $legacy) {
    $legacyText = Get-Content -LiteralPath $legacy -Raw
    if ($legacyText -match "start_local_validator\.ps1") {
        Remove-Item -LiteralPath $legacy -Force
        Write-Host "Removed legacy validator-only Startup launcher: $legacy"
    }
}

if (-not $NoRunNow) {
    $runArgs = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", $StartScript, "-QsdmRoot", $QsdmRoot)
    if ($NoOpen) {
        $runArgs += "-NoOpen"
    }
    & powershell.exe @runArgs
}

Write-Host "Installed GUI Startup launcher: $launcher"
Write-Host "Action: $command"
