[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$PrivateKeyPath,
    [string]$OutputDirectory,
    [string]$ChromePath = 'C:\Program Files\Google\Chrome\Application\chrome.exe',
    [string]$UpdateUrl = 'https://qsdm.tech/downloads/qsdm-hive-wallet-extension-updates.xml'
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $PSScriptRoot 'dist'
}
$PrivateKeyPath = (Resolve-Path -LiteralPath $PrivateKeyPath).Path
$ChromePath = (Resolve-Path -LiteralPath $ChromePath).Path

$manifest = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'manifest.json') |
    ConvertFrom-Json
$version = [string]$manifest.version
if ($version -notmatch '^\d+\.\d+\.\d+$') {
    throw "Extension manifest has an invalid version: $version"
}

$packageFiles = @(
    'manifest.json',
    'background.js',
    'content.js',
    'provider.js',
    'popup.html',
    'popup.css',
    'popup.js',
    'qsdm-hive-icon.png'
)

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$OutputDirectory = (Resolve-Path -LiteralPath $OutputDirectory).Path
$stageRoot = Join-Path $OutputDirectory ".crx-stage-$([guid]::NewGuid().ToString('N'))"
$extensionRoot = Join-Path $stageRoot 'qsdm-hive-wallet-extension'
New-Item -ItemType Directory -Path $extensionRoot | Out-Null

try {
    foreach ($file in $packageFiles) {
        $source = Join-Path $PSScriptRoot $file
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "Extension package input is missing: $source"
        }
        Copy-Item -LiteralPath $source -Destination (Join-Path $extensionRoot $file)
    }

    $packedManifestPath = Join-Path $extensionRoot 'manifest.json'
    $packedManifest = Get-Content -Raw -LiteralPath $packedManifestPath |
        ConvertFrom-Json
    $packedManifest.PSObject.Properties.Remove('key')
    foreach ($contentScript in $packedManifest.content_scripts) {
        $contentScript.matches = @(
            $contentScript.matches | Where-Object {
                $_ -notin @('http://localhost/*', 'http://127.0.0.1/*')
            }
        )
    }
    $packedManifest | Add-Member -NotePropertyName 'update_url' -NotePropertyValue $UpdateUrl

    $manifestJson = $packedManifest | ConvertTo-Json -Depth 20
    $utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText(
        $packedManifestPath,
        "$manifestJson$([Environment]::NewLine)",
        $utf8WithoutBom
    )

    $chromeArguments =
        "--pack-extension=`"$extensionRoot`" --pack-extension-key=`"$PrivateKeyPath`""
    $chromeStdout = Join-Path $stageRoot 'chrome-pack.stdout.log'
    $chromeStderr = Join-Path $stageRoot 'chrome-pack.stderr.log'
    $chromeProcess = Start-Process -FilePath $ChromePath `
        -ArgumentList $chromeArguments -Wait -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $chromeStdout -RedirectStandardError $chromeStderr
    if ($chromeProcess.ExitCode -ne 0) {
        $chromeDetail = @(
            Get-Content -LiteralPath $chromeStdout -ErrorAction SilentlyContinue
            Get-Content -LiteralPath $chromeStderr -ErrorAction SilentlyContinue
        ) -join [Environment]::NewLine
        throw "Chrome failed to package the CRX (exit code $($chromeProcess.ExitCode)). $chromeDetail"
    }

    $generatedCrx = "$extensionRoot.crx"
    if (-not (Test-Path -LiteralPath $generatedCrx -PathType Leaf)) {
        throw "Chrome did not create the expected CRX: $generatedCrx"
    }

    $outputCrx = Join-Path $OutputDirectory "qsdm-hive-wallet-extension-$version.crx"
    Copy-Item -LiteralPath $generatedCrx -Destination $outputCrx -Force
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $outputCrx).Hash.ToLowerInvariant()

    [pscustomobject]@{
        Path = $outputCrx
        Version = $version
        Sha256 = $hash
        UpdateUrl = $UpdateUrl
    }
} finally {
    Remove-Item -LiteralPath $stageRoot -Recurse -Force -ErrorAction SilentlyContinue
}
