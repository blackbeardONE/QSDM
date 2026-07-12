param(
    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$IconPath,

    [Parameter(Mandatory = $true)]
    [string]$OutputPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)$') {
    throw 'Version must use MAJOR.MINOR.PATCH format.'
}
$major = [int]$Matches[1]
$minor = [int]$Matches[2]
$patch = [int]$Matches[3]
if (-not (Test-Path -LiteralPath $IconPath)) {
    throw "QSDM Edge Control icon was not found at $IconPath"
}

$windresCommands = @(Get-Command windres.exe -All -ErrorAction SilentlyContinue)
$windresCommand = $windresCommands |
    Where-Object { $_.Source -match '[\\/]msys64[\\/]mingw64[\\/]bin[\\/]windres\.exe$' } |
    Select-Object -First 1
if (-not $windresCommand) {
    $windresCommand = $windresCommands | Select-Object -First 1
}
if (-not $windresCommand) {
    throw 'MinGW windres.exe is required to build the QSDM Edge Control Windows icon.'
}
$windresExe = $windresCommand.Source
$previousPath = $env:Path
$env:Path = "$(Split-Path -Parent $windresExe);$env:SystemRoot\System32"

$outputDirectory = Split-Path -Parent $OutputPath
$workDirectory = Join-Path ([IO.Path]::GetTempPath()) "qsdm-edge-control-resource-$PID-$([guid]::NewGuid().ToString('N'))"
$resourceScript = Join-Path $workDirectory 'qsdm-edge-control.rc'
$localIcon = Join-Path $workDirectory 'qsdm-edge-control.ico'

New-Item -ItemType Directory -Force -Path $workDirectory | Out-Null
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

try {
    Copy-Item -LiteralPath $IconPath -Destination $localIcon -Force
    $resourceSource = @"
1 ICON "qsdm-edge-control.ico"

1 VERSIONINFO
FILEVERSION $major,$minor,$patch,0
PRODUCTVERSION $major,$minor,$patch,0
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
            VALUE "FileDescription", "QSDM Edge Control\0"
            VALUE "FileVersion", "$Version.0\0"
            VALUE "InternalName", "qsdm-edge-control\0"
            VALUE "OriginalFilename", "qsdm-edge-control.exe\0"
            VALUE "ProductName", "QSDM Edge Control\0"
            VALUE "ProductVersion", "$Version.0\0"
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
        $lastExitCode = 0
        for ($attempt = 1; $attempt -le 3; $attempt++) {
            Remove-Item -LiteralPath $OutputPath -Force -ErrorAction SilentlyContinue
            & $windresExe -J rc -O coff -F pe-x86-64 --use-temp-file -i $resourceScript -o $OutputPath
            $lastExitCode = $LASTEXITCODE
            if ($lastExitCode -eq 0 -and (Test-Path -LiteralPath $OutputPath -PathType Leaf)) {
                $compiled = $true
                break
            }
            Write-Warning "windres.exe attempt $attempt of 3 failed with exit code $lastExitCode; retrying with a clean output."
            Start-Sleep -Milliseconds (250 * $attempt)
        }
        if (-not $compiled) {
            throw "windres.exe failed after 3 attempts (last exit code $lastExitCode)"
        }
    }
    finally {
        Pop-Location
    }
}
finally {
    $env:Path = $previousPath
    Remove-Item -LiteralPath $workDirectory -Recurse -Force -ErrorAction SilentlyContinue
}
