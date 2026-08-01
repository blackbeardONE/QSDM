param(
    [string]$ExtensionId = 'habkkkednignfkoffhpbjahcjbikkahh',
    [string]$FirefoxExtensionId = 'qsdm-wallet@qsdm.tech',
    [string]$HostPath = ""
)

$ErrorActionPreference = 'Stop'
$extensionIdPattern = '^[a-p]{32}$'
$firefoxExtensionIdPattern = '^[A-Za-z0-9._+@-]{1,128}$'
if ($ExtensionId -notmatch $extensionIdPattern) {
    throw 'ExtensionId must be the 32-character Chrome or Edge extension ID.'
}
if ($FirefoxExtensionId -notmatch $firefoxExtensionIdPattern) {
    throw 'FirefoxExtensionId contains unsupported characters.'
}
if (-not $HostPath) {
    $HostPath = Join-Path $PSScriptRoot '..\..\native\qsdm-hive-wallet-host.exe'
}
$HostPath = (Resolve-Path -LiteralPath $HostPath).Path
$installDir = Join-Path $env:LOCALAPPDATA 'QSDM\HiveWalletBridge'
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$chromiumManifestPath = Join-Path $installDir 'tech.qsdm.hive_wallet.json'
$firefoxManifestPath = Join-Path $installDir 'tech.qsdm.hive_wallet.firefox.json'
$chromiumManifest = [ordered]@{
    name = 'tech.qsdm.hive_wallet'
    description = 'QSDM Hive Wallet native bridge'
    path = $HostPath
    type = 'stdio'
    allowed_origins = @("chrome-extension://$ExtensionId/")
}
$firefoxManifest = [ordered]@{
    name = 'tech.qsdm.hive_wallet'
    description = 'QSDM Hive Wallet native bridge'
    path = $HostPath
    type = 'stdio'
    allowed_extensions = @($FirefoxExtensionId)
}
$utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText($chromiumManifestPath, ($chromiumManifest | ConvertTo-Json -Depth 4), $utf8WithoutBom)
[System.IO.File]::WriteAllText($firefoxManifestPath, ($firefoxManifest | ConvertTo-Json -Depth 4), $utf8WithoutBom)

$registryTargets = @(
    @('HKCU:\Software\Google\Chrome\NativeMessagingHosts\tech.qsdm.hive_wallet', $chromiumManifestPath),
    @('HKCU:\Software\Microsoft\Edge\NativeMessagingHosts\tech.qsdm.hive_wallet', $chromiumManifestPath),
    @('HKCU:\Software\Mozilla\NativeMessagingHosts\tech.qsdm.hive_wallet', $firefoxManifestPath)
)
foreach ($target in $registryTargets) {
    $registryPath = $target[0]
    New-Item -Force -Path $registryPath | Out-Null
    Set-Item -LiteralPath $registryPath -Value $target[1]
}
Write-Host "QSDM Wallet bridge registered for Chromium $ExtensionId and Firefox $FirefoxExtensionId"
