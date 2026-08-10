[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$PrivateKeyPath,
    [string]$OutputDirectory,
    [string]$ChromePath = 'C:\Program Files\Google\Chrome\Application\chrome.exe',
    [string]$UpdateUrl = 'https://qsdm.tech/downloads/qsdm-hive-wallet-extension-updates.xml',
    [string]$ExpectedExtensionId = 'nmmhneekhgaegpmbnhiacglhoncicflc'
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $PSScriptRoot 'dist'
}
$PrivateKeyPath = (Resolve-Path -LiteralPath $PrivateKeyPath).Path
$ChromePath = (Resolve-Path -LiteralPath $ChromePath).Path

function Get-ChromiumExtensionId {
    param([Parameter(Mandatory = $true)][string]$PemPath)

    $rsa = [System.Security.Cryptography.RSA]::Create()
    try {
        $rsa.ImportFromPem([System.IO.File]::ReadAllText($PemPath))
        $publicKey = $rsa.ExportSubjectPublicKeyInfo()
    } finally {
        $rsa.Dispose()
    }

    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $digest = $sha256.ComputeHash($publicKey)
    } finally {
        $sha256.Dispose()
    }

    $id = [System.Text.StringBuilder]::new(32)
    for ($index = 0; $index -lt 16; $index++) {
        [void]$id.Append([char](97 + ($digest[$index] -shr 4)))
        [void]$id.Append([char](97 + ($digest[$index] -band 15)))
    }
    return $id.ToString()
}

$extensionId = Get-ChromiumExtensionId -PemPath $PrivateKeyPath
if ($extensionId -cne $ExpectedExtensionId) {
    throw "CRX signing key produces extension ID $extensionId; expected $ExpectedExtensionId."
}

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
    'home.html',
    'home.css',
    'home.js',
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
    $packedManifest.PSObject.Properties.Remove('browser_specific_settings')
    $packedManifest.background.PSObject.Properties.Remove('scripts')
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

    $codebaseUrl = [System.Uri]::new(
        [System.Uri]$UpdateUrl,
        [System.IO.Path]::GetFileName($outputCrx)
    ).AbsoluteUri
    $updateManifestPath = Join-Path $OutputDirectory `
        'qsdm-hive-wallet-extension-updates.xml'
    $xmlSettings = [System.Xml.XmlWriterSettings]::new()
    $xmlSettings.Encoding = [System.Text.UTF8Encoding]::new($false)
    $xmlSettings.Indent = $true
    $xmlWriter = [System.Xml.XmlWriter]::Create($updateManifestPath, $xmlSettings)
    try {
        $xmlWriter.WriteStartDocument()
        $xmlWriter.WriteStartElement(
            'gupdate',
            'http://www.google.com/update2/response'
        )
        $xmlWriter.WriteAttributeString('protocol', '2.0')
        $xmlWriter.WriteStartElement('app')
        $xmlWriter.WriteAttributeString('appid', $extensionId)
        $xmlWriter.WriteStartElement('updatecheck')
        $xmlWriter.WriteAttributeString('codebase', $codebaseUrl)
        $xmlWriter.WriteAttributeString('version', $version)
        $xmlWriter.WriteEndElement()
        $xmlWriter.WriteEndElement()
        $xmlWriter.WriteEndElement()
        $xmlWriter.WriteEndDocument()
    } finally {
        $xmlWriter.Dispose()
    }

    [pscustomobject]@{
        Path = $outputCrx
        Version = $version
        ExtensionId = $extensionId
        Sha256 = $hash
        UpdateUrl = $UpdateUrl
        UpdateManifestPath = $updateManifestPath
        CodebaseUrl = $codebaseUrl
    }
} finally {
    Remove-Item -LiteralPath $stageRoot -Recurse -Force -ErrorAction SilentlyContinue
}
