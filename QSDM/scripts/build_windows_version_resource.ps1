param(
    [Parameter(Mandatory = $true)]
    [string]$ProductVersion,

    [string]$FileVersion = "",

    [Parameter(Mandatory = $true)]
    [string]$ProductName,

    [Parameter(Mandatory = $true)]
    [string]$FileDescription,

    [Parameter(Mandatory = $true)]
    [string]$InternalName,

    [Parameter(Mandatory = $true)]
    [string]$OriginalFilename,

    [Parameter(Mandatory = $true)]
    [string]$OutputPath,

    [string]$IconPath = ""
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($ProductVersion -notmatch '^(\d+)\.(\d+)\.(\d+)$') {
    throw 'ProductVersion must use MAJOR.MINOR.PATCH format.'
}
$productParts = @([int]$Matches[1], [int]$Matches[2], [int]$Matches[3])
if (-not $FileVersion) {
    $FileVersion = $ProductVersion
}
if ($FileVersion -notmatch '^(\d+)\.(\d+)\.(\d+)$') {
    throw 'FileVersion must use MAJOR.MINOR.PATCH format.'
}
$fileParts = @([int]$Matches[1], [int]$Matches[2], [int]$Matches[3])

foreach ($value in @($ProductName, $FileDescription, $InternalName, $OriginalFilename)) {
    if ([string]::IsNullOrWhiteSpace($value) -or $value -match '[\x00\r\n"]') {
        throw 'Version resource text must be non-empty and cannot contain quotes or control characters.'
    }
}
if ($IconPath -and -not (Test-Path -LiteralPath $IconPath -PathType Leaf)) {
    throw "Version resource icon was not found: $IconPath"
}

$windresCommands = @(Get-Command windres.exe -All -ErrorAction SilentlyContinue)
$windresCommand = $windresCommands |
    Where-Object { $_.Source -match '[\\/]msys64[\\/]mingw64[\\/]bin[\\/]windres\.exe$' } |
    Select-Object -First 1
if (-not $windresCommand) {
    $windresCommand = $windresCommands | Select-Object -First 1
}
if (-not $windresCommand) {
    throw 'MinGW windres.exe is required to build Windows version metadata.'
}

$OutputPath = [IO.Path]::GetFullPath($OutputPath)
$outputDirectory = Split-Path -Parent $OutputPath
$workDirectory = Join-Path ([IO.Path]::GetTempPath()) "qsdm-version-resource-$PID-$([guid]::NewGuid().ToString('N'))"
$resourceScript = Join-Path $workDirectory 'qsdm-version.rc'
$localIcon = Join-Path $workDirectory 'qsdm.ico'
$iconEntry = ''

New-Item -ItemType Directory -Force -Path $workDirectory | Out-Null
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

$previousPath = $env:Path
$env:Path = "$(Split-Path -Parent $windresCommand.Source);$env:SystemRoot\System32"
$savedEnvironment = @{}
$sanitizedEnvironmentNames = @('VSCODE_NLS_CONFIG', 'VSCODE_L10N_BUNDLE_LOCATION')

try {
    foreach ($name in $sanitizedEnvironmentNames) {
        $value = [Environment]::GetEnvironmentVariable($name, [EnvironmentVariableTarget]::Process)
        if ($null -ne $value) {
            $savedEnvironment[$name] = $value
            [Environment]::SetEnvironmentVariable($name, $null, [EnvironmentVariableTarget]::Process)
        }
    }

    if ($IconPath) {
        Copy-Item -LiteralPath $IconPath -Destination $localIcon -Force
        $iconEntry = '1 ICON "qsdm.ico"'
    }

    $resourceSource = @"
$iconEntry

1 VERSIONINFO
FILEVERSION $($fileParts[0]),$($fileParts[1]),$($fileParts[2]),0
PRODUCTVERSION $($productParts[0]),$($productParts[1]),$($productParts[2]),0
FILEFLAGSMASK 0x3fL
FILEFLAGS 0x0L
FILEOS 0x40004L
FILETYPE 0x1L
FILESUBTYPE 0x0L
BEGIN
    BLOCK "StringFileInfo"
    BEGIN
        BLOCK "040904b0"
        BEGIN
            VALUE "CompanyName", "QSDM\0"
            VALUE "FileDescription", "$FileDescription\0"
            VALUE "FileVersion", "$FileVersion.0\0"
            VALUE "InternalName", "$InternalName\0"
            VALUE "LegalCopyright", "Copyright (c) 2024-2026 Joedel Lopez Dalioan\0"
            VALUE "OriginalFilename", "$OriginalFilename\0"
            VALUE "ProductName", "$ProductName\0"
            VALUE "ProductVersion", "$ProductVersion.0\0"
        END
    END
    BLOCK "VarFileInfo"
    BEGIN
        VALUE "Translation", 0x0409, 1200
    END
END
"@
    [IO.File]::WriteAllText($resourceScript, $resourceSource, [Text.Encoding]::ASCII)

    Push-Location $workDirectory
    try {
        $compiled = $false
        for ($attempt = 1; $attempt -le 3; $attempt++) {
            Remove-Item -LiteralPath $OutputPath -Force -ErrorAction SilentlyContinue
            & $windresCommand.Source -J rc -O coff -F pe-x86-64 -i $resourceScript -o $OutputPath
            if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $OutputPath -PathType Leaf)) {
                $compiled = $true
                break
            }
            Start-Sleep -Milliseconds (250 * $attempt)
        }
        if (-not $compiled) {
            throw "windres.exe failed to create $OutputPath"
        }
    }
    finally {
        Pop-Location
    }
}
finally {
    $env:Path = $previousPath
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], [EnvironmentVariableTarget]::Process)
    }
    Remove-Item -LiteralPath $workDirectory -Recurse -Force -ErrorAction SilentlyContinue
}
