param(
    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$IconPath,

    [Parameter(Mandatory = $true)]
    [string]$OutputPath
)

$ErrorActionPreference = 'Stop'

$builder = Join-Path $PSScriptRoot 'build_windows_version_resource.ps1'
& $builder `
    -ProductVersion $Version `
    -FileVersion $Version `
    -ProductName 'QSDM Hive' `
    -FileDescription 'QSDM Edge Control' `
    -InternalName 'qsdm-edge-control' `
    -OriginalFilename 'qsdm-edge-control.exe' `
    -IconPath $IconPath `
    -OutputPath $OutputPath
