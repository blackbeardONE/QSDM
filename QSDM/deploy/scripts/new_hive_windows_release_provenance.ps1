[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$InstallerPath,

    [Parameter(Mandatory = $true)]
    [string]$MetadataEvidencePath,

    [Parameter(Mandatory = $true)]
    [string]$NsisEvidencePath,

    [Parameter(Mandatory = $true)]
    [string]$OutputPath,

    [string]$SourceCommit = $env:GITHUB_SHA,
    [string]$WorkflowRunUrl = "",
    [string]$WorkflowRunId = $env:GITHUB_RUN_ID,
    [string]$WorkflowRunAttempt = $env:GITHUB_RUN_ATTEMPT
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw 'Version must use MAJOR.MINOR.PATCH format.'
}
if ($SourceCommit -notmatch '^[0-9a-fA-F]{40}$') {
    throw 'SourceCommit must be a full 40-character Git commit hash.'
}

$installer = Get-Item -LiteralPath (Resolve-Path -LiteralPath $InstallerPath).Path
$metadata = Get-Content -Raw -LiteralPath $MetadataEvidencePath | ConvertFrom-Json
$nsis = Get-Content -Raw -LiteralPath $NsisEvidencePath | ConvertFrom-Json
$installerHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $installer.FullName).Hash.ToLowerInvariant()

if ($metadata.schema -ne 'qsdm.windows-metadata-evidence.v1' -or
    [string]$metadata.hive_version -ne $Version -or
    @($metadata.files).Count -lt 1) {
    throw 'Windows metadata evidence does not match this Hive release.'
}
if ($nsis.schema -ne 'qsdm.windows-nsis-payload-evidence.v1' -or
    [string]$nsis.installer.name -ne $installer.Name -or
    [string]$nsis.installer.sha256 -ne $installerHash -or
    @($nsis.files).Count -ne @($metadata.files).Count) {
    throw 'NSIS payload evidence does not match the installer and metadata evidence.'
}

if (-not $WorkflowRunUrl -and $env:GITHUB_SERVER_URL -and
    $env:GITHUB_REPOSITORY -and $WorkflowRunId) {
    $WorkflowRunUrl = "$($env:GITHUB_SERVER_URL)/$($env:GITHUB_REPOSITORY)/actions/runs/$WorkflowRunId"
}

$provenance = [ordered]@{
    schema = 'qsdm.hive.release.v1'
    version = $Version
    source_commit = $SourceCommit.ToLowerInvariant()
    built_at = (Get-Date).ToUniversalTime().ToString('o')
    target_channel = 'latest'
    platform = 'windows-x64'
    authenticode_status = 'NotSigned'
    workflow_run_url = $WorkflowRunUrl
    workflow_run_id = $WorkflowRunId
    workflow_run_attempt = $WorkflowRunAttempt
    installer_sha256 = $installerHash
    installer_size = [long]$installer.Length
    metadata_executables_verified = @($metadata.files).Count
    nsis_executables_verified = @($nsis.files).Count
    packager = 'electron-builder 26.15.3 --prepackaged'
}

$outputParent = Split-Path -Parent $OutputPath
if ($outputParent) {
    New-Item -ItemType Directory -Force -Path $outputParent | Out-Null
}
$provenance | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
Write-Host "Created Windows release provenance: $OutputPath"
