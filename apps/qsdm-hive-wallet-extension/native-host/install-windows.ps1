param(
    [Parameter(Mandatory = $true)]
    [string]$ExtensionId,
    [string]$HostPath = ""
)

$ErrorActionPreference = 'Stop'
$extensionIdPattern = '^[a-p]{32}$'
if ($ExtensionId -notmatch $extensionIdPattern) {
    throw 'ExtensionId must be the 32-character Chrome or Edge extension ID.'
}
if (-not $HostPath) {
    $HostPath = Join-Path $PSScriptRoot '..\..\native\qsdm-hive-wallet-host.exe'
}
$HostPath = (Resolve-Path -LiteralPath $HostPath).Path
$installDir = Join-Path $env:LOCALAPPDATA 'QSDM\HiveWalletBridge'
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$manifestPath = Join-Path $installDir 'tech.qsdm.hive_wallet.json'
$manifest = [ordered]@{
    name = 'tech.qsdm.hive_wallet'
    description = 'QSDM Hive Wallet native bridge'
    path = $HostPath
    type = 'stdio'
    allowed_origins = @("chrome-extension://$ExtensionId/")
}
$manifestJson = $manifest | ConvertTo-Json -Depth 4
$utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText($manifestPath, $manifestJson, $utf8WithoutBom)

$registryRoots = @(
    'HKCU:\Software\Google\Chrome\NativeMessagingHosts\tech.qsdm.hive_wallet',
    'HKCU:\Software\Microsoft\Edge\NativeMessagingHosts\tech.qsdm.hive_wallet'
)
foreach ($registryPath in $registryRoots) {
    New-Item -Force -Path $registryPath | Out-Null
    Set-Item -LiteralPath $registryPath -Value $manifestPath
}
Write-Host "QSDM Hive Wallet bridge registered for extension $ExtensionId"
