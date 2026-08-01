#requires -Version 5.1

[CmdletBinding()]
param(
    [string]$QsdmRoot = ""
)

if ([string]::IsNullOrWhiteSpace($QsdmRoot)) {
    $scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
    $QsdmRoot = (Resolve-Path (Join-Path $scriptDirectory "..")).Path
}

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$Updater = Join-Path $QsdmRoot "scripts\update_local_validator.ps1"
$Launcher = Join-Path $QsdmRoot "scripts\start_local_validator.ps1"
$Watchdog = Join-Path $QsdmRoot "scripts\watch_local_stack.ps1"
$TestID = [guid]::NewGuid().ToString("N")
$TestRoot = Join-Path ([IO.Path]::GetTempPath()) "qsdm-updater-test-$TestID"
$FakeQsdmRoot = Join-Path $TestRoot "qsdm-root"
$PackageSource = Join-Path $TestRoot "package-source"
$PackageRoot = Join-Path $PackageSource "qsdm-validator-v9.8.7-rc.6-windows-amd64"
$Archive = Join-Path $TestRoot "qsdm-validator-v9.8.7-rc.6-windows-amd64.zip"
$Manifest = Join-Path $TestRoot "SHA256SUMS"
$Signature = Join-Path $TestRoot "SHA256SUMS.sig"
$Certificate = Join-Path $TestRoot "SHA256SUMS.cert.pem"
$FakeCosign = Join-Path $TestRoot "cosign.exe"
$ExpectedVersion = "v9.8.7-rc.6"
$ExpectedRevision = "abcdef1"
$OriginalDevAcknowledgement = $env:QSDM_DEV_ALLOW_UNSIGNED_UPDATE_VERIFY

function Assert-Equal {
    param(
        [Parameter(Mandatory)]$Actual,
        [Parameter(Mandatory)]$Expected,
        [Parameter(Mandatory)][string]$Message
    )
    if ($Actual -ne $Expected) {
        throw "$Message Expected '$Expected', got '$Actual'."
    }
}

function Assert-True {
    param(
        [Parameter(Mandatory)][bool]$Value,
        [Parameter(Mandatory)][string]$Message
    )
    if (-not $Value) {
        throw $Message
    }
}

function Assert-ScriptParses {
    param([Parameter(Mandatory)][string]$Path)
    $tokens = $null
    $errors = $null
    [void][Management.Automation.Language.Parser]::ParseFile(
        (Resolve-Path -LiteralPath $Path),
        [ref]$tokens,
        [ref]$errors
    )
    if ($errors.Count -gt 0) {
        throw "$Path has parser errors: $($errors.Message -join ' | ')"
    }
}

function Invoke-VerifyOnly {
    param([string]$ArchivePath = $Archive)
    return & $Updater `
        -QsdmRoot $FakeQsdmRoot `
        -PackageArchivePath $ArchivePath `
        -ReleaseManifestPath $Manifest `
        -ReleaseManifestSignaturePath $Signature `
        -ReleaseManifestCertificatePath $Certificate `
        -ExpectedVersion $ExpectedVersion `
        -ExpectedRevision $ExpectedRevision `
        -VerifyOnly `
        -DevelopmentAllowUnsignedManifest
}

function Invoke-SignedVerifyOnly {
    return & $Updater `
        -QsdmRoot $FakeQsdmRoot `
        -PackageArchivePath $Archive `
        -ReleaseManifestPath $Manifest `
        -ReleaseManifestSignaturePath $Signature `
        -ReleaseManifestCertificatePath $Certificate `
        -ExpectedVersion $ExpectedVersion `
        -ExpectedRevision $ExpectedRevision `
        -CosignPath $FakeCosign `
        -VerifyOnly
}

try {
    foreach ($script in @($Updater, $Launcher, $Watchdog)) {
        Assert-ScriptParses -Path $script
    }

    New-Item -ItemType Directory -Force -Path $FakeQsdmRoot, $PackageRoot | Out-Null
    $className = "QsdmUpdaterFixture$TestID"
    $source = @"
using System;
public static class $className
{
    public static int Main(string[] args)
    {
        if (args.Length == 1 && args[0] == "--version")
        {
            Console.WriteLine("qsdm $ExpectedVersion ($ExpectedRevision, 2026-07-31T00:00:00Z, fixture, windows/amd64)");
            return 0;
        }
        return 0;
    }
}
"@
    $fixtureBinary = Join-Path $PackageRoot "qsdm-validator.exe"
    $sourcePath = Join-Path $TestRoot "fixture.cs"
    [IO.File]::WriteAllText($sourcePath, $source, [Text.UTF8Encoding]::new($false))
    $compiler = @(
        (Join-Path $env:SystemRoot "Microsoft.NET\Framework64\v4.0.30319\csc.exe"),
        (Join-Path $env:SystemRoot "Microsoft.NET\Framework\v4.0.30319\csc.exe")
    ) | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    if (-not $compiler) {
        throw "Windows C# compiler was not found; updater fixture cannot be built."
    }
    & $compiler /nologo /target:exe "/out:$fixtureBinary" $sourcePath
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $fixtureBinary -PathType Leaf)) {
        throw "Windows C# compiler could not build the updater fixture."
    }
    $cosignClassName = "QsdmCosignFixture$TestID"
    $cosignSource = @"
using System;
public static class $cosignClassName
{
    public static int Main(string[] args)
    {
        if (args.Length == 1 && args[0] == "version")
        {
            Console.WriteLine("GitVersion: v2.6.2");
            return 0;
        }
        if (args.Length > 0 && args[0] == "verify-blob")
        {
            Console.WriteLine("Verified OK");
            return 0;
        }
        return 2;
    }
}
"@
    $cosignSourcePath = Join-Path $TestRoot "cosign-fixture.cs"
    [IO.File]::WriteAllText($cosignSourcePath, $cosignSource, [Text.UTF8Encoding]::new($false))
    & $compiler /nologo /target:exe "/out:$FakeCosign" $cosignSourcePath
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $FakeCosign -PathType Leaf)) {
        throw "Windows C# compiler could not build the Cosign fixture."
    }

    $binaryHash = (Get-FileHash -LiteralPath $fixtureBinary -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(
        (Join-Path $PackageRoot "SHA256SUMS.txt"),
        "$binaryHash  qsdm-validator.exe`n",
        [Text.UTF8Encoding]::new($false)
    )
    Add-Type -AssemblyName System.IO.Compression.FileSystem | Out-Null
    [IO.Compression.ZipFile]::CreateFromDirectory(
        $PackageSource,
        $Archive,
        [IO.Compression.CompressionLevel]::Optimal,
        $false
    )
    $archiveHash = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(
        $Manifest,
        "$archiveHash  $([IO.Path]::GetFileName($Archive))`n",
        [Text.UTF8Encoding]::new($false)
    )
    [IO.File]::WriteAllText($Signature, "fixture-signature", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($Certificate, "fixture-certificate", [Text.UTF8Encoding]::new($false))
    $env:QSDM_DEV_ALLOW_UNSIGNED_UPDATE_VERIFY = "I_UNDERSTAND_THIS_IS_NOT_RELEASE_VERIFICATION"

    $verified = Invoke-SignedVerifyOnly
    Assert-True -Value ([bool]$verified.success) -Message "Valid fixture package was not accepted."
    Assert-True -Value ([bool]$verified.signature_verified) -Message "Signature-verifier orchestration did not complete."
    Assert-Equal -Actual ([string]$verified.cosign_version) -Expected "2.6.2" -Message "Cosign version gate mismatch."
    Assert-Equal -Actual ([string]$verified.status) -Expected "verified" -Message "Unexpected verify-only status."
    Assert-Equal -Actual ([string]$verified.to_version) -Expected $ExpectedVersion -Message "Version metadata mismatch."
    Assert-Equal -Actual ([string]$verified.to_revision) -Expected $ExpectedRevision -Message "Revision metadata mismatch."
    Assert-Equal -Actual ([string]$verified.binary_sha256) -Expected $binaryHash -Message "Inner checksum mismatch."
    Assert-Equal -Actual ([string]$verified.package_sha256) -Expected $archiveHash -Message "Archive checksum mismatch."

    $stagingRoot = Join-Path $FakeQsdmRoot "source\.cache\local-validator\.update-staging"
    $leftovers = @(
        if (Test-Path -LiteralPath $stagingRoot) {
            Get-ChildItem -LiteralPath $stagingRoot -Directory
        }
    )
    Assert-Equal -Actual $leftovers.Count -Expected 0 -Message "Verify-only left a transaction staging directory."

    $tamperedArchive = Join-Path $TestRoot "qsdm-validator-v9.8.7-rc.6-windows-amd64-tampered.zip"
    Copy-Item -LiteralPath $Archive -Destination $tamperedArchive
    [IO.File]::AppendAllText($tamperedArchive, "tamper", [Text.UTF8Encoding]::new($false))
    $expectedName = Join-Path $TestRoot "qsdm-validator-v9.8.7-rc.6-windows-amd64.zip"
    Move-Item -LiteralPath $Archive -Destination "$Archive.valid"
    Move-Item -LiteralPath $tamperedArchive -Destination $expectedName
    $tamperRejected = $false
    try {
        Invoke-VerifyOnly | Out-Null
    } catch {
        $tamperRejected = $_.Exception.Message -like "*checksum mismatch*"
    } finally {
        Remove-Item -LiteralPath $expectedName -Force -ErrorAction SilentlyContinue
        Move-Item -LiteralPath "$Archive.valid" -Destination $Archive
    }
    Assert-True -Value $tamperRejected -Message "A tampered release archive was not rejected."

    $unsignedActivationRejected = $false
    try {
        & $Updater `
            -QsdmRoot $FakeQsdmRoot `
            -PackageArchivePath $Archive `
            -ReleaseManifestPath $Manifest `
            -ReleaseManifestSignaturePath $Signature `
            -ReleaseManifestCertificatePath $Certificate `
            -ExpectedVersion $ExpectedVersion `
            -ExpectedRevision $ExpectedRevision `
            -DevelopmentAllowUnsignedManifest `
            -Confirm:$false | Out-Null
    } catch {
        $unsignedActivationRejected = $_.Exception.Message -like "*can never activate*"
    }
    Assert-True -Value $unsignedActivationRejected -Message "Unsigned activation was not rejected."

    $lockPath = Join-Path $FakeQsdmRoot "source\.cache\local-validator\validator-update.lock"
    $heldLock = [IO.File]::Open(
        $lockPath,
        [IO.FileMode]::OpenOrCreate,
        [IO.FileAccess]::ReadWrite,
        [IO.FileShare]::None
    )
    try {
        $lockRejected = $false
        $started = [DateTime]::UtcNow
        try {
            & $Updater `
                -QsdmRoot $FakeQsdmRoot `
                -PackageArchivePath $Archive `
                -ReleaseManifestPath $Manifest `
                -ReleaseManifestSignaturePath $Signature `
                -ReleaseManifestCertificatePath $Certificate `
                -ExpectedVersion $ExpectedVersion `
                -ExpectedRevision $ExpectedRevision `
                -VerifyOnly `
                -DevelopmentAllowUnsignedManifest `
                -LockWaitSeconds 1 | Out-Null
        } catch {
            $lockRejected = $_.Exception.Message -like "*overlapping update*"
        }
        $elapsed = ([DateTime]::UtcNow - $started).TotalSeconds
        Assert-True -Value $lockRejected -Message "Overlapping updater lock was not rejected."
        Assert-True -Value ($elapsed -lt 4) -Message "Updater lock rejection exceeded its bounded deadline."
    } finally {
        $heldLock.Dispose()
    }

    [pscustomobject]@{
        schema = "qsdm.local-validator-updater-test.v1"
        success = $true
        parser_checks = 3
        verified_package = $true
        signature_verifier_orchestration = $true
        tamper_rejected = $true
        unsigned_activation_rejected = $true
        overlapping_update_rejected = $true
    } | ConvertTo-Json -Compress
} finally {
    $env:QSDM_DEV_ALLOW_UNSIGNED_UPDATE_VERIFY = $OriginalDevAcknowledgement
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    $resolvedTestRoot = [IO.Path]::GetFullPath($TestRoot)
    if ($resolvedTestRoot.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -and
        [IO.Path]::GetFileName($resolvedTestRoot) -like "qsdm-updater-test-*") {
        Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
