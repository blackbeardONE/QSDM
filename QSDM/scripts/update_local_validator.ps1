#requires -Version 5.1

[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string]$QsdmRoot = "",
    [Parameter(Mandatory)]
    [string]$PackageArchivePath,
    [Parameter(Mandatory)]
    [string]$ReleaseManifestPath,
    [Parameter(Mandatory)]
    [string]$ReleaseManifestSignaturePath,
    [Parameter(Mandatory)]
    [string]$ReleaseManifestCertificatePath,
    [Parameter(Mandatory)]
    [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$')]
    [string]$ExpectedVersion,
    [Parameter(Mandatory)]
    [ValidatePattern('^[0-9a-fA-F]{7,40}$')]
    [string]$ExpectedRevision,
    [string]$CosignPath = "cosign",
    [string]$HealthUrl = "http://127.0.0.1:8080/api/v1/health/ready",
    [string]$StatusUrl = "http://127.0.0.1:8080/api/v1/status",
    [ValidateRange(10, 900)]
    [int]$HealthWaitSeconds = 120,
    [ValidateRange(3, 120)]
    [int]$ProcessStopWaitSeconds = 15,
    [ValidateRange(3, 120)]
    [int]$WatchdogWaitSeconds = 15,
    [ValidateRange(1, 60)]
    [int]$LockWaitSeconds = 5,
    [ValidateRange(2, 50)]
    [int]$KeepBackups = 5,
    [ValidateRange(7, 3650)]
    [int]$BackupRetentionDays = 30,
    [switch]$PruneExpiredBackups,
    [switch]$VerifyOnly,
    [switch]$DevelopmentAllowUnsignedManifest
)

if ([string]::IsNullOrWhiteSpace($QsdmRoot)) {
    $scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
    $QsdmRoot = (Resolve-Path (Join-Path $scriptDirectory "..")).Path
}

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$QsdmRoot = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $QsdmRoot).Path)
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$ModeConfigPath = Join-Path $LocalRoot "validator-mode.json"
$ActiveBinaryStatePath = Join-Path $LocalRoot "validator-active.json"
$WatchdogPidPath = Join-Path $LocalRoot "watchdog.pid"
$WatchdogIdentityPath = Join-Path $LocalRoot "watchdog.process.json"
$WatchdogScript = Join-Path $QsdmRoot "scripts\watch_local_stack.ps1"
$ValidatorLauncher = Join-Path $QsdmRoot "scripts\start_local_validator.ps1"
$BackupsRoot = Join-Path $LocalRoot "release-backups"
$StagingRoot = Join-Path $LocalRoot ".update-staging"
$ResultsRoot = Join-Path $LocalRoot "update-results"
$LatestResultPath = Join-Path $ResultsRoot "latest.json"
$UpdateLockPath = Join-Path $LocalRoot "validator-update.lock"
$TransactionID = "{0}-{1}" -f [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ"), ([guid]::NewGuid().ToString("N").Substring(0, 8))
$TransactionRoot = Join-Path $StagingRoot $TransactionID
$ExtractRoot = Join-Path $TransactionRoot "package"
$CommandLogRoot = Join-Path $TransactionRoot "commands"
$BackupDir = ""
$UpdateLock = $null
$Baseline = $null
$BaselineProcess = $null
$BaselineBinaryPath = ""
$BaselineBinaryHash = ""
$BaselineBinaryVersion = $null
$Mode = $null
$RunDir = ""
$ValidatorPidPath = ""
$ValidatorIdentityPath = ""
$TargetBinaryPath = ""
$TargetBinaryHash = ""
$TargetVersionMetadata = $null
$WatchdogWasStopped = $false
$ActivationStarted = $false
$WatchdogRestored = $false
$SignatureVerified = $false
$ReleaseArchiveHash = ""
$HealthUri = $null
$StatusUri = $null
$BackendBaseUrl = ""

$Result = [ordered]@{
    schema = "qsdm.local-validator-update-result.v1"
    transaction_id = $TransactionID
    started_at_utc = [DateTime]::UtcNow.ToString("o")
    completed_at_utc = $null
    status = "initializing"
    success = $false
    verify_only = $VerifyOnly.IsPresent
    signature_verified = $false
    cosign_version = ""
    expected_version = $ExpectedVersion
    expected_revision = $ExpectedRevision.ToLowerInvariant()
    from_version = ""
    from_revision = ""
    to_version = ""
    to_revision = ""
    node_id = ""
    baseline_chain_tip = 0
    final_chain_tip = 0
    peers = 0
    task_actions_ready = $false
    package_archive = [IO.Path]::GetFileName($PackageArchivePath)
    package_sha256 = ""
    binary = ""
    binary_sha256 = ""
    supervisor = ""
    rollback_attempted = $false
    rollback_succeeded = $false
    rollback_directory = ""
    pruned_backups = @()
    error = ""
}

function Test-ReparsePoint {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        return $false
    }
    return [bool]((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)
}

function Assert-LeafFile {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Description
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "$Description is missing: $Path"
    }
    if (Test-ReparsePoint -Path $Path) {
        throw "Refusing a reparse-point $Description`: $Path"
    }
}

function Get-Sha256 {
    param([Parameter(Mandatory)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Test-PathUnderRoot {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Root
    )

    $fullPath = [IO.Path]::GetFullPath($Path)
    $fullRoot = [IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    return $fullPath.StartsWith($fullRoot, [StringComparison]::OrdinalIgnoreCase)
}

function Resolve-LoopbackUri {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Description
    )

    $uri = $null
    if (-not [Uri]::TryCreate($Url, [UriKind]::Absolute, [ref]$uri)) {
        throw "$Description is not a valid absolute URL."
    }
    if ($uri.Scheme -ne "http" -or -not $uri.IsLoopback -or $uri.Port -lt 1 -or
        -not [string]::IsNullOrWhiteSpace($uri.UserInfo) -or
        -not [string]::IsNullOrWhiteSpace($uri.Query) -or
        -not [string]::IsNullOrWhiteSpace($uri.Fragment)) {
        throw "$Description must be an explicit loopback HTTP URL with a port and no credentials, query, or fragment."
    }
    return $uri
}

function Write-JsonAtomic {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][object]$Value
    )

    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $tempPath = "$Path.tmp-$PID"
    [IO.File]::WriteAllText(
        $tempPath,
        ($Value | ConvertTo-Json -Depth 12),
        [Text.UTF8Encoding]::new($false)
    )
    Move-Item -LiteralPath $tempPath -Destination $Path -Force
}

function Write-UpdateResult {
    $Result.signature_verified = $SignatureVerified
    $Result.package_sha256 = $ReleaseArchiveHash
    $Result.rollback_directory = $BackupDir
    Write-JsonAtomic -Path $LatestResultPath -Value $Result
    if (-not [string]::IsNullOrWhiteSpace($BackupDir) -and (Test-Path -LiteralPath $BackupDir -PathType Container)) {
        Write-JsonAtomic -Path (Join-Path $BackupDir "update-result.json") -Value $Result
    }
}

function Acquire-UpdateLock {
    $deadline = [DateTime]::UtcNow.AddSeconds($LockWaitSeconds)
    do {
        try {
            $script:UpdateLock = [IO.File]::Open(
                $UpdateLockPath,
                [IO.FileMode]::OpenOrCreate,
                [IO.FileAccess]::ReadWrite,
                [IO.FileShare]::None
            )
            $lockRecord = [Text.Encoding]::UTF8.GetBytes(
                (@{
                    schema = "qsdm.local-validator-update-lock.v1"
                    pid = $PID
                    transaction_id = $TransactionID
                    acquired_at_utc = [DateTime]::UtcNow.ToString("o")
                } | ConvertTo-Json -Compress)
            )
            $script:UpdateLock.SetLength(0)
            $script:UpdateLock.Write($lockRecord, 0, $lockRecord.Length)
            $script:UpdateLock.Flush($true)
            return
        } catch {
            Start-Sleep -Milliseconds 250
        }
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Another validator update owns $UpdateLockPath; refusing an overlapping update."
}

function ConvertTo-NativeArgument {
    param([AllowEmptyString()][string]$Value)

    if ($null -eq $Value -or $Value.Length -eq 0) {
        return '""'
    }
    if ($Value -notmatch '[\s"]') {
        return $Value
    }

    $builder = New-Object Text.StringBuilder
    [void]$builder.Append('"')
    $backslashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '\') {
            $backslashes++
            continue
        }
        if ($character -eq '"') {
            [void]$builder.Append(('\' * (($backslashes * 2) + 1)))
            [void]$builder.Append('"')
            $backslashes = 0
            continue
        }
        if ($backslashes -gt 0) {
            [void]$builder.Append(('\' * $backslashes))
            $backslashes = 0
        }
        [void]$builder.Append($character)
    }
    if ($backslashes -gt 0) {
        [void]$builder.Append(('\' * ($backslashes * 2)))
    }
    [void]$builder.Append('"')
    return $builder.ToString()
}

function Initialize-BoundedProcessRunner {
    if ("QsdmBoundedProcessRunner" -as [type]) {
        return
    }
    Add-Type -TypeDefinition @"
using System;
using System.Diagnostics;
using System.Text;
using System.Threading;

public sealed class QsdmBoundedProcessResult
{
    public int ExitCode { get; set; }
    public string Stdout { get; set; }
    public string Stderr { get; set; }
    public bool TimedOut { get; set; }
    public bool StoppedAfterTimeout { get; set; }
}

public static class QsdmBoundedProcessRunner
{
    public static QsdmBoundedProcessResult Run(
        string filePath,
        string arguments,
        string workingDirectory,
        int timeoutMilliseconds,
        int killWaitMilliseconds)
    {
        ProcessStartInfo startInfo = new ProcessStartInfo();
        startInfo.FileName = filePath;
        startInfo.Arguments = arguments;
        startInfo.WorkingDirectory = workingDirectory;
        startInfo.UseShellExecute = false;
        startInfo.CreateNoWindow = true;
        startInfo.RedirectStandardOutput = true;
        startInfo.RedirectStandardError = true;

        StringBuilder stdout = new StringBuilder();
        StringBuilder stderr = new StringBuilder();
        object stdoutLock = new object();
        object stderrLock = new object();
        DataReceivedEventHandler stdoutHandler = delegate(object sender, DataReceivedEventArgs eventArgs) {
            if (eventArgs.Data != null) {
                lock (stdoutLock) { stdout.AppendLine(eventArgs.Data); }
            }
        };
        DataReceivedEventHandler stderrHandler = delegate(object sender, DataReceivedEventArgs eventArgs) {
            if (eventArgs.Data != null) {
                lock (stderrLock) { stderr.AppendLine(eventArgs.Data); }
            }
        };

        using (Process process = new Process())
        {
            process.StartInfo = startInfo;
            process.OutputDataReceived += stdoutHandler;
            process.ErrorDataReceived += stderrHandler;
            if (!process.Start()) {
                throw new InvalidOperationException("Process could not be started.");
            }
            process.BeginOutputReadLine();
            process.BeginErrorReadLine();

            bool exited = process.WaitForExit(timeoutMilliseconds);
            bool stoppedAfterTimeout = exited;
            if (!exited) {
                try { process.Kill(); } catch { }
                stoppedAfterTimeout = process.WaitForExit(killWaitMilliseconds);
            }

            // Do not call parameterless WaitForExit: descendants can inherit
            // pipe handles and hold it open after the tracked process exits.
            Thread.Sleep(100);
            try { process.CancelOutputRead(); } catch { }
            try { process.CancelErrorRead(); } catch { }
            process.OutputDataReceived -= stdoutHandler;
            process.ErrorDataReceived -= stderrHandler;

            string capturedStdout;
            string capturedStderr;
            lock (stdoutLock) { capturedStdout = stdout.ToString(); }
            lock (stderrLock) { capturedStderr = stderr.ToString(); }
            return new QsdmBoundedProcessResult {
                ExitCode = process.HasExited ? process.ExitCode : -1,
                Stdout = capturedStdout,
                Stderr = capturedStderr,
                TimedOut = !exited,
                StoppedAfterTimeout = stoppedAfterTimeout
            };
        }
    }
}
"@
}

function Invoke-BoundedProcess {
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [Parameter(Mandatory)][string[]]$Arguments,
        [Parameter(Mandatory)][ValidateRange(1, 1800)][int]$TimeoutSeconds,
        [Parameter(Mandatory)][string]$Name
    )

    New-Item -ItemType Directory -Force -Path $CommandLogRoot | Out-Null
    $safeName = $Name -replace '[^A-Za-z0-9_.-]', '-'
    $stdoutPath = Join-Path $CommandLogRoot "$safeName.stdout.log"
    $stderrPath = Join-Path $CommandLogRoot "$safeName.stderr.log"
    $argumentString = (@($Arguments | ForEach-Object { ConvertTo-NativeArgument -Value $_ })) -join ' '
    Initialize-BoundedProcessRunner
    $nativeResult = [QsdmBoundedProcessRunner]::Run(
        $FilePath,
        $argumentString,
        $QsdmRoot,
        ($TimeoutSeconds * 1000),
        3000
    )
    $stdout = [string]$nativeResult.Stdout
    $stderr = [string]$nativeResult.Stderr
    [IO.File]::WriteAllText($stdoutPath, $stdout, [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($stderrPath, $stderr, [Text.UTF8Encoding]::new($false))
    if ([bool]$nativeResult.TimedOut) {
        if (-not [bool]$nativeResult.StoppedAfterTimeout) {
            throw "$Name exceeded its $TimeoutSeconds second deadline and did not stop within the 3 second kill deadline."
        }
        throw "$Name exceeded its $TimeoutSeconds second deadline."
    }
    return [pscustomobject]@{
        ExitCode = [int]$nativeResult.ExitCode
        Stdout = $stdout
        Stderr = $stderr
        StdoutPath = $stdoutPath
        StderrPath = $stderrPath
    }
}

function Resolve-ExecutableCommand {
    param(
        [Parameter(Mandatory)][string]$Command,
        [Parameter(Mandatory)][string]$Description
    )

    if ([IO.Path]::IsPathRooted($Command)) {
        Assert-LeafFile -Path $Command -Description $Description
        return [IO.Path]::GetFullPath($Command)
    }
    $resolved = Get-Command $Command -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if (-not $resolved) {
        throw "$Description was not found on PATH: $Command"
    }
    return $resolved.Source
}

function Assert-SecureCosignVersion {
    param([Parameter(Mandatory)][string]$ExecutablePath)

    $probe = Invoke-BoundedProcess `
        -FilePath $ExecutablePath `
        -Arguments @("version") `
        -TimeoutSeconds 10 `
        -Name "cosign-version"
    $output = ($probe.Stdout + "`n" + $probe.Stderr).Trim()
    if ($probe.ExitCode -ne 0) {
        throw "Cosign version probe failed with exit code $($probe.ExitCode): $output"
    }
    $match = [regex]::Match(
        $output,
        '(?im)(?:GitVersion\s*:\s*v?|cosign\s+version\s+v?)(?<version>[0-9]+\.[0-9]+\.[0-9]+)'
    )
    if (-not $match.Success) {
        throw "Could not determine the Cosign version from: $output"
    }
    $version = [Version]$match.Groups['version'].Value
    $secure = ($version.Major -eq 2 -and $version -ge [Version]"2.6.2") -or
        ($version.Major -eq 3 -and $version -ge [Version]"3.0.4")
    if (-not $secure) {
        throw "Cosign $version is not accepted. Use patched Cosign 2.6.2+ or 3.0.4+."
    }
    $Result.cosign_version = $version.ToString()
}

function Get-ManifestHash {
    param(
        [Parameter(Mandatory)][string]$ManifestPath,
        [Parameter(Mandatory)][string]$FileName
    )

    $escapedName = [regex]::Escape($FileName)
    $matches = @(
        Get-Content -LiteralPath $ManifestPath |
            Where-Object { $_ -match "^([0-9a-fA-F]{64})\s+\*?(?:\./)?${escapedName}$" }
    )
    if ($matches.Count -ne 1) {
        throw "Expected exactly one SHA-256 row for $FileName in $ManifestPath; found $($matches.Count)."
    }
    return ([regex]::Match($matches[0], '^([0-9a-fA-F]{64})')).Groups[1].Value.ToLowerInvariant()
}

function Assert-RevisionMatches {
    param(
        [Parameter(Mandatory)][string]$Actual,
        [Parameter(Mandatory)][string]$Expected,
        [Parameter(Mandatory)][string]$Description
    )

    $actualLower = $Actual.ToLowerInvariant()
    $expectedLower = $Expected.ToLowerInvariant()
    if (-not ($actualLower.StartsWith($expectedLower) -or $expectedLower.StartsWith($actualLower))) {
        throw "$Description revision is $Actual; expected $Expected."
    }
}

function Get-BinaryVersionMetadata {
    param(
        [Parameter(Mandatory)][string]$BinaryPath,
        [string]$RequiredVersion = "",
        [string]$RequiredRevision = ""
    )

    $probeName = "version-" + ([IO.Path]::GetFileNameWithoutExtension($BinaryPath))
    $probe = Invoke-BoundedProcess -FilePath $BinaryPath -Arguments @("--version") -TimeoutSeconds 10 -Name $probeName
    $output = ($probe.Stdout + "`n" + $probe.Stderr).Trim()
    if ($probe.ExitCode -ne 0) {
        throw "Validator version probe failed with exit code $($probe.ExitCode): $output"
    }
    $match = [regex]::Match(
        $output,
        '^qsdm\s+(?<version>\S+)\s+\((?<revision>[0-9a-fA-F]{7,40}),',
        [Text.RegularExpressions.RegexOptions]::Multiline
    )
    if (-not $match.Success) {
        throw "Validator did not return recognizable QSDM version metadata: $output"
    }
    $metadata = [pscustomobject]@{
        Version = $match.Groups['version'].Value
        Revision = $match.Groups['revision'].Value.ToLowerInvariant()
        Output = $output
    }
    if (-not [string]::IsNullOrWhiteSpace($RequiredVersion) -and $metadata.Version -ne $RequiredVersion) {
        throw "Validator version is $($metadata.Version); expected $RequiredVersion."
    }
    if (-not [string]::IsNullOrWhiteSpace($RequiredRevision)) {
        Assert-RevisionMatches -Actual $metadata.Revision -Expected $RequiredRevision -Description "Validator"
    }
    return $metadata
}

function Expand-VerifiedZip {
    param(
        [Parameter(Mandatory)][string]$ArchivePath,
        [Parameter(Mandatory)][string]$Destination
    )

    Add-Type -AssemblyName System.IO.Compression.FileSystem | Out-Null
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    $destinationPrefix = [IO.Path]::GetFullPath($Destination).TrimEnd('\') + '\'
    $archive = [IO.Compression.ZipFile]::OpenRead($ArchivePath)
    try {
        foreach ($entry in $archive.Entries) {
            $entryName = $entry.FullName.Replace('/', '\')
            if ([string]::IsNullOrWhiteSpace($entryName)) {
                continue
            }
            $attributes = [uint32]([int64]$entry.ExternalAttributes -band 0xffffffffL)
            $unixType = (($attributes -shr 16) -band 0xF000)
            if ($unixType -eq 0xA000) {
                throw "Release archive contains a symbolic link: $($entry.FullName)"
            }
            $destinationPath = [IO.Path]::GetFullPath((Join-Path $Destination $entryName))
            if (-not $destinationPath.StartsWith($destinationPrefix, [StringComparison]::OrdinalIgnoreCase)) {
                throw "Release archive entry escapes the staging directory: $($entry.FullName)"
            }
            if ($entryName.EndsWith('\')) {
                New-Item -ItemType Directory -Force -Path $destinationPath | Out-Null
                continue
            }
            $parent = Split-Path -Parent $destinationPath
            New-Item -ItemType Directory -Force -Path $parent | Out-Null
            [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $destinationPath, $false)
        }
    } finally {
        $archive.Dispose()
    }
}

function Get-ReleasePackage {
    $archiveFullPath = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $PackageArchivePath).Path)
    $manifestFullPath = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $ReleaseManifestPath).Path)
    $signatureFullPath = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $ReleaseManifestSignaturePath).Path)
    $certificateFullPath = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $ReleaseManifestCertificatePath).Path)
    $script:PackageArchivePath = $archiveFullPath
    $script:ReleaseManifestPath = $manifestFullPath
    $script:ReleaseManifestSignaturePath = $signatureFullPath
    $script:ReleaseManifestCertificatePath = $certificateFullPath
    Assert-LeafFile -Path $archiveFullPath -Description "validator release archive"
    Assert-LeafFile -Path $manifestFullPath -Description "release SHA256SUMS"
    Assert-LeafFile -Path $signatureFullPath -Description "release manifest signature"
    Assert-LeafFile -Path $certificateFullPath -Description "release manifest certificate"

    $expectedArchiveName = "qsdm-validator-$ExpectedVersion-windows-amd64.zip"
    if ([IO.Path]::GetFileName($archiveFullPath) -ne $expectedArchiveName) {
        throw "Release archive must be named $expectedArchiveName."
    }

    if ($DevelopmentAllowUnsignedManifest) {
        if (-not $VerifyOnly) {
            throw "-DevelopmentAllowUnsignedManifest is restricted to -VerifyOnly and can never activate a validator."
        }
        if ($env:QSDM_DEV_ALLOW_UNSIGNED_UPDATE_VERIFY -ne "I_UNDERSTAND_THIS_IS_NOT_RELEASE_VERIFICATION") {
            throw "Unsigned verification mode requires the explicit QSDM_DEV_ALLOW_UNSIGNED_UPDATE_VERIFY development acknowledgement."
        }
    } else {
        $cosign = Resolve-ExecutableCommand -Command $CosignPath -Description "cosign verifier"
        Assert-SecureCosignVersion -ExecutablePath $cosign
        $identity = "https://github.com/blackbeardONE/QSDM/.github/workflows/release-container.yml@refs/tags/$ExpectedVersion"
        $verification = Invoke-BoundedProcess `
            -FilePath $cosign `
            -Arguments @(
                "verify-blob",
                "--certificate", $certificateFullPath,
                "--signature", $signatureFullPath,
                "--certificate-identity", $identity,
                "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
                $manifestFullPath
            ) `
            -TimeoutSeconds 45 `
            -Name "cosign-verify-release-manifest"
        if ($verification.ExitCode -ne 0) {
            $detail = ($verification.Stdout + "`n" + $verification.Stderr).Trim()
            throw "Sigstore verification failed for the release manifest: $detail"
        }
        $script:SignatureVerified = $true
    }

    $expectedArchiveHash = Get-ManifestHash -ManifestPath $manifestFullPath -FileName $expectedArchiveName
    $actualArchiveHash = Get-Sha256 -Path $archiveFullPath
    if ($actualArchiveHash -ne $expectedArchiveHash) {
        throw "Validator release archive checksum mismatch."
    }
    $script:ReleaseArchiveHash = $actualArchiveHash

    Expand-VerifiedZip -ArchivePath $archiveFullPath -Destination $ExtractRoot | Out-Null
    $validators = @(
        Get-ChildItem -LiteralPath $ExtractRoot -Filter "qsdm-validator.exe" -File -Recurse
    )
    if ($validators.Count -ne 1) {
        throw "Expected one qsdm-validator.exe in the release archive; found $($validators.Count)."
    }
    $binary = $validators[0].FullName
    $packageDirectory = Split-Path -Parent $binary
    $packageManifest = Join-Path $packageDirectory "SHA256SUMS.txt"
    Assert-LeafFile -Path $packageManifest -Description "package SHA256SUMS"
    $expectedBinaryHash = Get-ManifestHash -ManifestPath $packageManifest -FileName "qsdm-validator.exe"
    $actualBinaryHash = Get-Sha256 -Path $binary
    if ($actualBinaryHash -ne $expectedBinaryHash) {
        throw "Unpacked validator binary checksum mismatch."
    }
    $metadata = Get-BinaryVersionMetadata `
        -BinaryPath $binary `
        -RequiredVersion $ExpectedVersion `
        -RequiredRevision $ExpectedRevision

    return [pscustomobject]@{
        Binary = $binary
        BinaryHash = $actualBinaryHash
        Version = $metadata
        ArchiveHash = $actualArchiveHash
        PackageDirectory = $packageDirectory
    }
}

function Get-ValidatorMode {
    $mode = [ordered]@{
        mode = "solo"
        chainSyncUrls = "https://api.qsdm.tech/api/v1"
        bootstrapPeers = ""
        publicP2P = $false
    }
    if (Test-Path -LiteralPath $ModeConfigPath) {
        $loaded = Get-Content -LiteralPath $ModeConfigPath -Raw | ConvertFrom-Json
        if ([string]$loaded.mode -eq "networked") {
            $mode.mode = "networked"
            if (-not [string]::IsNullOrWhiteSpace([string]$loaded.chainSyncUrls)) {
                $mode.chainSyncUrls = [string]$loaded.chainSyncUrls
            }
            $mode.bootstrapPeers = [string]$loaded.bootstrapPeers
            $mode.publicP2P = [bool]$loaded.publicP2P
        }
    }
    return [pscustomobject]$mode
}

function Get-ApiStatus {
    return Invoke-RestMethod -Uri $StatusUrl -TimeoutSec 5
}

function Test-ApiReady {
    try {
        $response = Invoke-WebRequest -Uri $HealthUrl -UseBasicParsing -TimeoutSec 3
        return ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300)
    } catch {
        return $false
    }
}

function Wait-ReadyStatus {
    param([Parameter(Mandatory)][int]$TimeoutSeconds)

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if (Test-ApiReady) {
            try {
                return Get-ApiStatus
            } catch {}
        }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Validator did not become ready within $TimeoutSeconds seconds."
}

function Read-PidFile {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return 0
    }
    $text = (Get-Content -LiteralPath $Path -Raw).Trim()
    if ($text -notmatch '^[0-9]+$') {
        return 0
    }
    return [int]$text
}

function ConvertTo-UtcTimestamp {
    param(
        [Parameter(Mandatory)]
        [object]$Value
    )

    if ($Value -is [DateTime]) {
        return ([DateTime]$Value).ToUniversalTime()
    }
    if ($Value -is [DateTimeOffset]) {
        return ([DateTimeOffset]$Value).UtcDateTime
    }
    return [DateTime]::Parse(
        [string]$Value,
        [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::RoundtripKind
    ).ToUniversalTime()
}

function Get-NativeProcessStartUtc {
    param([Parameter(Mandatory)][int]$ProcessIdentifier)

    if (-not ("QsdmProcessTimes" -as [type])) {
        Add-Type -TypeDefinition @"
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

public static class QsdmProcessTimes
{
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GetProcessTimes(
        IntPtr processHandle,
        out long creationTime,
        out long exitTime,
        out long kernelTime,
        out long userTime);

    public static DateTime GetStartTimeUtc(int processId)
    {
        using (Process process = Process.GetProcessById(processId))
        {
            long creationTime;
            long exitTime;
            long kernelTime;
            long userTime;
            if (!GetProcessTimes(
                process.Handle,
                out creationTime,
                out exitTime,
                out kernelTime,
                out userTime))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error());
            }
            return DateTime.FromFileTimeUtc(creationTime);
        }
    }
}
"@
    }
    return [QsdmProcessTimes]::GetStartTimeUtc($ProcessIdentifier)
}

function Get-ManagedValidatorProcess {
    $validatorPid = Read-PidFile -Path $ValidatorPidPath
    if ($validatorPid -le 0) {
        return $null
    }
    $process = Get-Process -Id $validatorPid -ErrorAction SilentlyContinue
    if (-not $process) {
        return $null
    }
    $processPath = $process.Path
    if ([string]::IsNullOrWhiteSpace($processPath) -or -not (Test-PathUnderRoot -Path $processPath -Root $LocalRoot)) {
        throw "Validator PID file points outside the managed validator directory; refusing to stop PID $validatorPid."
    }
    if (Test-Path -LiteralPath $ValidatorIdentityPath -PathType Leaf) {
        $identity = Get-Content -LiteralPath $ValidatorIdentityPath -Raw | ConvertFrom-Json
        if ([int]$identity.pid -ne $validatorPid) {
            throw "Validator PID and identity records disagree."
        }
        $recordedBinary = [IO.Path]::GetFullPath([string]$identity.binary)
        if ($recordedBinary -ne [IO.Path]::GetFullPath($processPath)) {
            throw "Validator identity points to a different executable."
        }
        $recordedStart = ConvertTo-UtcTimestamp -Value $identity.process_start_utc
        $actualStart = Get-NativeProcessStartUtc -ProcessIdentifier $validatorPid
        if ([Math]::Abs(($recordedStart - $actualStart).TotalSeconds) -gt 2) {
            throw "Validator PID was reused by another process; refusing to stop it."
        }
        $recordedHash = ([string]$identity.sha256).ToLowerInvariant()
        if ($recordedHash -notmatch '^[0-9a-f]{64}$' -or (Get-Sha256 -Path $processPath) -ne $recordedHash) {
            throw "Validator executable no longer matches its process identity."
        }
    } else {
        $pidFileWrite = (Get-Item -LiteralPath $ValidatorPidPath).LastWriteTimeUtc
        $actualStart = Get-NativeProcessStartUtc -ProcessIdentifier $validatorPid
        if ([Math]::Abs(($pidFileWrite - $actualStart).TotalSeconds) -gt 15) {
            throw "Legacy validator PID file is stale; refusing to stop a possibly unrelated process."
        }
    }
    return $process
}

function Get-ManagedWatchdogProcess {
    $watchdogPid = Read-PidFile -Path $WatchdogPidPath
    if ($watchdogPid -le 0) {
        return $null
    }
    $process = Get-Process -Id $watchdogPid -ErrorAction SilentlyContinue
    if (-not $process) {
        return $null
    }
    if ($process.ProcessName -notin @("powershell", "pwsh")) {
        throw "Watchdog PID file points to unexpected process $($process.ProcessName); refusing to stop PID $watchdogPid."
    }

    if (Test-Path -LiteralPath $WatchdogIdentityPath -PathType Leaf) {
        $identity = Get-Content -LiteralPath $WatchdogIdentityPath -Raw | ConvertFrom-Json
        if ([int]$identity.pid -ne $watchdogPid) {
            throw "Watchdog PID and identity records disagree."
        }
        if ([IO.Path]::GetFullPath([string]$identity.script) -ne [IO.Path]::GetFullPath($WatchdogScript)) {
            throw "Watchdog identity points to a different script."
        }
        $recordedStart = ConvertTo-UtcTimestamp -Value $identity.process_start_utc
        $actualStart = Get-NativeProcessStartUtc -ProcessIdentifier $watchdogPid
        if ([Math]::Abs(($recordedStart - $actualStart).TotalSeconds) -gt 2) {
            throw "Watchdog PID was reused by another process; refusing to stop it."
        }
        $expectedScriptHash = ([string]$identity.script_sha256).ToLowerInvariant()
        if ($expectedScriptHash -notmatch '^[0-9a-f]{64}$' -or (Get-Sha256 -Path $WatchdogScript) -ne $expectedScriptHash) {
            throw "Watchdog script no longer matches its process identity."
        }
    } else {
        $pidFileWrite = (Get-Item -LiteralPath $WatchdogPidPath).LastWriteTimeUtc
        $actualStart = Get-NativeProcessStartUtc -ProcessIdentifier $watchdogPid
        if ([Math]::Abs(($pidFileWrite - $actualStart).TotalSeconds) -gt 15) {
            throw "Legacy watchdog PID file is stale; refusing to stop a possibly unrelated process."
        }
    }
    return $process
}

function Stop-ProcessBounded {
    param(
        [Parameter(Mandatory)][System.Diagnostics.Process]$Process,
        [Parameter(Mandatory)][string]$Description,
        [Parameter(Mandatory)][int]$TimeoutSeconds
    )

    Stop-Process -Id $Process.Id -Force -ErrorAction Stop
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if (-not (Get-Process -Id $Process.Id -ErrorAction SilentlyContinue)) {
            return
        }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Description PID $($Process.Id) did not stop within $TimeoutSeconds seconds."
}

function Stop-Watchdog {
    $watchdog = Get-ManagedWatchdogProcess
    if (-not $watchdog) {
        Remove-Item -LiteralPath $WatchdogPidPath -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $WatchdogIdentityPath -Force -ErrorAction SilentlyContinue
        return $false
    }
    Stop-ProcessBounded -Process $watchdog -Description "Watchdog" -TimeoutSeconds $ProcessStopWaitSeconds
    Remove-Item -LiteralPath $WatchdogPidPath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $WatchdogIdentityPath -Force -ErrorAction SilentlyContinue
    return $true
}

function Stop-Validator {
    $validator = Get-ManagedValidatorProcess
    if (-not $validator) {
        throw "The validator PID file does not identify a running managed validator."
    }
    Stop-ProcessBounded -Process $validator -Description "Validator" -TimeoutSeconds $ProcessStopWaitSeconds
    Remove-Item -LiteralPath $ValidatorPidPath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $ValidatorIdentityPath -Force -ErrorAction SilentlyContinue

    $deadline = [DateTime]::UtcNow.AddSeconds(5)
    while ((Test-ApiReady) -and [DateTime]::UtcNow -lt $deadline) {
        Start-Sleep -Milliseconds 250
    }
    if (Test-ApiReady) {
        throw "The local validator API is still ready after its recorded process stopped; refusing to replace a possibly duplicated stack."
    }
}

function Get-PowerShellHost {
    $pwsh = Get-Command "pwsh.exe" -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($pwsh) {
        return $pwsh.Source
    }
    $windowsPowerShell = Get-Command "powershell.exe" -CommandType Application -ErrorAction Stop |
        Select-Object -First 1
    return $windowsPowerShell.Source
}

function Get-LauncherArguments {
    param([switch]$Restart)

    $arguments = @(
        "-NoProfile",
        "-ExecutionPolicy", "Bypass",
        "-File", $ValidatorLauncher,
        "-QsdmRoot", $QsdmRoot,
        "-HealthUrl", $HealthUrl,
        "-HealthWaitSeconds", [string]$HealthWaitSeconds,
        "-LockWaitSeconds", "5"
    )
    if ($Mode.mode -eq "networked") {
        $arguments += @("-Networked", "-ChainSyncUrls", [string]$Mode.chainSyncUrls)
        if (-not [string]::IsNullOrWhiteSpace([string]$Mode.bootstrapPeers)) {
            $arguments += @("-BootstrapPeers", [string]$Mode.bootstrapPeers)
        }
        if ([bool]$Mode.publicP2P) {
            $arguments += "-PublicP2P"
        }
    }
    if ($Restart) {
        $arguments += "-Restart"
    }
    return $arguments
}

function Start-Validator {
    $powershell = Get-PowerShellHost
    $launch = Invoke-BoundedProcess `
        -FilePath $powershell `
        -Arguments (Get-LauncherArguments) `
        -TimeoutSeconds ($HealthWaitSeconds + 20) `
        -Name "validator-launcher"
    if ($launch.ExitCode -ne 0) {
        $detail = ($launch.Stdout + "`n" + $launch.Stderr).Trim()
        throw "Validator launcher exited with code $($launch.ExitCode): $detail"
    }
}

function Assert-RunningValidator {
    param(
        [Parameter(Mandatory)][string]$ExpectedPath,
        [Parameter(Mandatory)][string]$ExpectedHash,
        [Parameter(Mandatory)][string]$ExpectedVersionValue,
        [Parameter(Mandatory)][string]$ExpectedRevisionValue,
        [Parameter(Mandatory)][string]$ExpectedNodeID,
        [Parameter(Mandatory)][long]$MinimumChainTip
    )

    $status = Wait-ReadyStatus -TimeoutSeconds $HealthWaitSeconds
    if ([string]$status.node_id -ne $ExpectedNodeID) {
        throw "Validator node identity changed during update."
    }
    if ([string]$status.version -ne $ExpectedVersionValue) {
        throw "Validator API reports version $($status.version); expected $ExpectedVersionValue."
    }
    Assert-RevisionMatches -Actual ([string]$status.git_sha) -Expected $ExpectedRevisionValue -Description "Validator API"
    if (-not [bool]$status.task_actions_ready) {
        throw "Validator task actions are not ready."
    }
    $minimumPeers = if ($Mode.mode -eq "networked") { 1 } else { 0 }
    if ([int]$status.peers -lt $minimumPeers) {
        throw "Validator reports $($status.peers) peers; expected at least $minimumPeers."
    }
    if ([long]$status.chain_tip -lt $MinimumChainTip) {
        throw "Validator chain tip regressed from $MinimumChainTip to $($status.chain_tip)."
    }

    $process = Get-ManagedValidatorProcess
    if (-not $process) {
        throw "Validator is ready but its managed PID is missing."
    }
    $actualPath = [IO.Path]::GetFullPath($process.Path)
    if ($actualPath -ne [IO.Path]::GetFullPath($ExpectedPath)) {
        throw "Validator is running from $actualPath instead of $ExpectedPath."
    }
    if ((Get-Sha256 -Path $ExpectedPath) -ne $ExpectedHash) {
        throw "Running validator file checksum changed after launch."
    }
    return $status
}

function Write-ActiveBinaryState {
    param(
        [Parameter(Mandatory)][string]$BinaryPath,
        [Parameter(Mandatory)][string]$Sha256,
        [Parameter(Mandatory)][string]$Version,
        [Parameter(Mandatory)][string]$Revision
    )

    if (-not (Test-PathUnderRoot -Path $BinaryPath -Root $LocalRoot)) {
        throw "Refusing to activate a validator binary outside $LocalRoot."
    }
    $binaryName = [IO.Path]::GetFileName($BinaryPath)
    if ([string]::IsNullOrWhiteSpace($binaryName)) {
        throw "Active validator binary has no file name."
    }
    $state = [ordered]@{
        schema = "qsdm.validator-active.v1"
        binary = $binaryName
        sha256 = $Sha256.ToLowerInvariant()
        version = $Version
        revision = $Revision.ToLowerInvariant()
        release_manifest_sha256 = Get-Sha256 -Path $ReleaseManifestPath
        package_sha256 = $ReleaseArchiveHash
        transaction_id = $TransactionID
        activated_at_utc = [DateTime]::UtcNow.ToString("o")
    }
    Write-JsonAtomic -Path $ActiveBinaryStatePath -Value $state
}

function Start-Watchdog {
    Assert-LeafFile -Path $WatchdogScript -Description "watchdog script"
    $expectedWatchdogHash = Get-Sha256 -Path $WatchdogScript
    $powershell = Get-PowerShellHost
    $arguments = @(
        "-NoProfile",
        "-ExecutionPolicy", "Bypass",
        "-WindowStyle", "Hidden",
        "-File", $WatchdogScript,
        "-QsdmRoot", $QsdmRoot,
        "-Relay", "https://api.qsdm.tech",
        "-Slot", "home-validator",
        "-Backend", $BackendBaseUrl,
        "-IntervalSeconds", "30",
        "-RestartAfterFailures", "10",
        "-GatewayRestartAfterFailures", "3",
        "-CheckPublicGateway"
    )
    $argumentString = (@($arguments | ForEach-Object { ConvertTo-NativeArgument -Value $_ })) -join ' '
    $startedAt = [DateTime]::UtcNow
    $process = Start-Process `
        -FilePath $powershell `
        -ArgumentList $argumentString `
        -WorkingDirectory $QsdmRoot `
        -WindowStyle Hidden `
        -PassThru

    $deadline = [DateTime]::UtcNow.AddSeconds($WatchdogWaitSeconds)
    $lastObservation = "watchdog PID file has not been published"
    do {
        $pidFromFile = Read-PidFile -Path $WatchdogPidPath
        if ($pidFromFile -gt 0) {
            $recordedProcess = Get-Process -Id $pidFromFile -ErrorAction SilentlyContinue
            if (-not $recordedProcess) {
                $lastObservation = "watchdog PID $pidFromFile is not running"
            } elseif (-not (Test-Path -LiteralPath $WatchdogIdentityPath -PathType Leaf)) {
                $lastObservation = "watchdog identity file has not been published"
            } else {
                try {
                    $identity = Get-Content -LiteralPath $WatchdogIdentityPath -Raw | ConvertFrom-Json
                    $identityScript = [IO.Path]::GetFullPath([string]$identity.script)
                    $recordedStart = ConvertTo-UtcTimestamp -Value $identity.process_start_utc
                    $actualStart = Get-NativeProcessStartUtc -ProcessIdentifier $pidFromFile
                    $writtenAt = ConvertTo-UtcTimestamp -Value $identity.written_at_utc
                    if ([int]$identity.pid -ne $pidFromFile) {
                        $lastObservation = "watchdog PID and identity records disagree"
                    } elseif ($identityScript -ne [IO.Path]::GetFullPath($WatchdogScript)) {
                        $lastObservation = "watchdog identity references a different script"
                    } elseif ([string]$identity.script_sha256 -ne $expectedWatchdogHash) {
                        $lastObservation = "watchdog identity has the wrong script checksum"
                    } elseif ([Math]::Abs(($recordedStart - $actualStart).TotalSeconds) -gt 2) {
                        $startDelta = [Math]::Abs(($recordedStart - $actualStart).TotalSeconds)
                        $lastObservation = "watchdog process start time does not match its identity (recorded=$($recordedStart.ToString('o')) actual=$($actualStart.ToString('o')) delta_seconds=$startDelta)"
                    } elseif ($writtenAt -lt $startedAt.AddSeconds(-2)) {
                        $lastObservation = "watchdog identity is stale"
                    } else {
                        return [pscustomobject]@{
                            Mode = "direct-process"
                            Pid = $pidFromFile
                        }
                    }
                } catch {
                    $lastObservation = "watchdog identity could not be validated: $($_.Exception.Message)"
                }
            }
        } else {
            $lastObservation = "watchdog PID file has not been published"
        }
        if ($process.HasExited) {
            throw "Watchdog process exited with code $($process.ExitCode) before publishing its identity. Last observation: $lastObservation."
        }
        Start-Sleep -Milliseconds 250
        $process.Refresh()
    } while ([DateTime]::UtcNow -lt $deadline)
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    throw "Watchdog did not publish a valid process identity within $WatchdogWaitSeconds seconds. Last observation: $lastObservation."
}

function Backup-Baseline {
    $fromVersionSafe = ([string]$Baseline.version) -replace '[^0-9A-Za-z.-]', '-'
    $toVersionSafe = $ExpectedVersion -replace '[^0-9A-Za-z.-]', '-'
    $script:BackupDir = Join-Path $BackupsRoot "$TransactionID-$fromVersionSafe-to-$toVersionSafe"
    New-Item -ItemType Directory -Force -Path $BackupDir | Out-Null
    Copy-Item -LiteralPath $BaselineBinaryPath -Destination (Join-Path $BackupDir ([IO.Path]::GetFileName($BaselineBinaryPath)))
    if ((Get-Sha256 -Path (Join-Path $BackupDir ([IO.Path]::GetFileName($BaselineBinaryPath)))) -ne $BaselineBinaryHash) {
        throw "Rollback binary copy failed checksum verification."
    }
    if (Test-Path -LiteralPath $ActiveBinaryStatePath -PathType Leaf) {
        Copy-Item -LiteralPath $ActiveBinaryStatePath -Destination (Join-Path $BackupDir "validator-active.before-update.json")
    }
    if (Test-Path -LiteralPath $ModeConfigPath -PathType Leaf) {
        Copy-Item -LiteralPath $ModeConfigPath -Destination (Join-Path $BackupDir "validator-mode.before-update.json")
    }
    if (Test-Path -LiteralPath $WatchdogPidPath -PathType Leaf) {
        Copy-Item -LiteralPath $WatchdogPidPath -Destination (Join-Path $BackupDir "watchdog.pid.before-update")
    }
    if (Test-Path -LiteralPath $WatchdogIdentityPath -PathType Leaf) {
        Copy-Item -LiteralPath $WatchdogIdentityPath -Destination (Join-Path $BackupDir "watchdog.process.before-update.json")
    }
    Write-JsonAtomic -Path (Join-Path $BackupDir "update-metadata.json") -Value ([ordered]@{
        schema = "qsdm.local-validator-update.v1"
        transaction_id = $TransactionID
        created_at_utc = [DateTime]::UtcNow.ToString("o")
        node_id = [string]$Baseline.node_id
        baseline_chain_tip = [long]$Baseline.chain_tip
        from_version = [string]$Baseline.version
        from_revision = [string]$Baseline.git_sha
        from_binary = [IO.Path]::GetFullPath($BaselineBinaryPath)
        from_sha256 = $BaselineBinaryHash
        to_version = $ExpectedVersion
        to_revision = $ExpectedRevision.ToLowerInvariant()
        to_sha256 = $TargetBinaryHash
        package_sha256 = $ReleaseArchiveHash
        release_manifest_sha256 = Get-Sha256 -Path $ReleaseManifestPath
    })
}

function Restore-Baseline {
    $Result.rollback_attempted = $true
    try {
        $current = Get-ManagedValidatorProcess
        if ($current) {
            Stop-ProcessBounded -Process $current -Description "Replacement validator" -TimeoutSeconds $ProcessStopWaitSeconds
        }
        Remove-Item -LiteralPath $ValidatorPidPath -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $ValidatorIdentityPath -Force -ErrorAction SilentlyContinue

        $baselinePath = [IO.Path]::GetFullPath($BaselineBinaryPath)
        $backupBinary = Join-Path $BackupDir ([IO.Path]::GetFileName($baselinePath))
        if (-not (Test-Path -LiteralPath $baselinePath -PathType Leaf) -or (Get-Sha256 -Path $baselinePath) -ne $BaselineBinaryHash) {
            Copy-Item -LiteralPath $backupBinary -Destination $baselinePath -Force
        }
        if ((Get-Sha256 -Path $baselinePath) -ne $BaselineBinaryHash) {
            throw "Rollback binary checksum does not match the baseline."
        }
        Write-ActiveBinaryState `
            -BinaryPath $baselinePath `
            -Sha256 $BaselineBinaryHash `
            -Version $BaselineBinaryVersion.Version `
            -Revision $BaselineBinaryVersion.Revision

        Start-Validator
        $rolledBack = Assert-RunningValidator `
            -ExpectedPath $baselinePath `
            -ExpectedHash $BaselineBinaryHash `
            -ExpectedVersionValue ([string]$Baseline.version) `
            -ExpectedRevisionValue ([string]$Baseline.git_sha) `
            -ExpectedNodeID ([string]$Baseline.node_id) `
            -MinimumChainTip ([long]$Baseline.chain_tip)
        $watchdog = Start-Watchdog
        $script:WatchdogRestored = $true
        $Result.rollback_succeeded = $true
        $Result.status = "rolled-back"
        $Result.final_chain_tip = [long]$rolledBack.chain_tip
        $Result.peers = [int]$rolledBack.peers
        $Result.task_actions_ready = [bool]$rolledBack.task_actions_ready
        $Result.supervisor = [string]$watchdog.Mode
    } catch {
        $Result.status = "rollback-failed"
        $Result.rollback_succeeded = $false
        throw
    }
}

function Remove-ExpiredBackups {
    if (-not $PruneExpiredBackups) {
        return @()
    }
    if (-not (Test-Path -LiteralPath $BackupsRoot -PathType Container)) {
        return @()
    }
    $rootFull = [IO.Path]::GetFullPath($BackupsRoot).TrimEnd('\')
    $directories = @(
        Get-ChildItem -LiteralPath $BackupsRoot -Directory |
            Sort-Object LastWriteTimeUtc -Descending
    )
    $keepPaths = @($directories | Select-Object -First $KeepBackups | ForEach-Object { $_.FullName })
    $cutoff = [DateTime]::UtcNow.AddDays(-$BackupRetentionDays)
    $removed = @()
    foreach ($directory in $directories) {
        $fullPath = [IO.Path]::GetFullPath($directory.FullName)
        $parent = [IO.Path]::GetFullPath((Split-Path -Parent $fullPath)).TrimEnd('\')
        if ($parent -ne $rootFull) {
            throw "Refusing to prune a nested path outside the direct backup set: $fullPath"
        }
        if ($fullPath -eq [IO.Path]::GetFullPath($BackupDir) -or $keepPaths -contains $fullPath) {
            continue
        }
        if ($directory.LastWriteTimeUtc -ge $cutoff -or (Test-ReparsePoint -Path $fullPath)) {
            continue
        }
        Remove-Item -LiteralPath $fullPath -Recurse -Force
        $removed += $fullPath
    }
    return @($removed)
}

$HealthUri = Resolve-LoopbackUri -Url $HealthUrl -Description "Health URL"
$StatusUri = Resolve-LoopbackUri -Url $StatusUrl -Description "Status URL"
if ($HealthUri.Scheme -ne $StatusUri.Scheme -or $HealthUri.Authority -ne $StatusUri.Authority) {
    throw "Health URL and status URL must use the same loopback authority."
}
$HealthUrl = $HealthUri.AbsoluteUri
$StatusUrl = $StatusUri.AbsoluteUri
$BackendBaseUrl = "{0}://{1}" -f $HealthUri.Scheme, $HealthUri.Authority

New-Item -ItemType Directory -Force -Path $LocalRoot, $StagingRoot, $ResultsRoot, $TransactionRoot, $CommandLogRoot | Out-Null
Acquire-UpdateLock

try {
    $Result.status = "verifying"
    Write-UpdateResult
    $package = Get-ReleasePackage
    $TargetBinaryHash = $package.BinaryHash
    $TargetVersionMetadata = $package.Version
    $Result.to_version = $TargetVersionMetadata.Version
    $Result.to_revision = $TargetVersionMetadata.Revision
    $Result.binary_sha256 = $TargetBinaryHash
    $Result.status = "verified"
    Write-UpdateResult

    if ($VerifyOnly) {
        $Result.success = $true
        $Result.status = "verified"
        $Result.completed_at_utc = [DateTime]::UtcNow.ToString("o")
        Write-UpdateResult
        [pscustomobject]$Result
        return
    }
    if (-not $PSCmdlet.ShouldProcess($LocalRoot, "replace the active local QSDM validator with $ExpectedVersion")) {
        $Result.status = "cancelled"
        $Result.completed_at_utc = [DateTime]::UtcNow.ToString("o")
        Write-UpdateResult
        [pscustomobject]$Result
        return
    }

    Assert-LeafFile -Path $ValidatorLauncher -Description "validator launcher"
    Assert-LeafFile -Path $WatchdogScript -Description "watchdog script"
    $Mode = Get-ValidatorMode
    $RunDir = Join-Path $LocalRoot $(if ($Mode.mode -eq "networked") { "run-networked" } else { "run-v2" })
    $ValidatorPidPath = Join-Path $RunDir "qsdm.autostart.pid"
    $ValidatorIdentityPath = Join-Path $RunDir "qsdm.autostart.process.json"

    if (-not (Test-ApiReady)) {
        throw "The current validator is not ready; refusing an update until the existing stack is healthy."
    }
    $Baseline = Get-ApiStatus
    $BaselineProcess = Get-ManagedValidatorProcess
    if (-not $BaselineProcess) {
        throw "The healthy validator has no managed PID record; refusing to guess which process to replace."
    }
    $BaselineBinaryPath = [IO.Path]::GetFullPath($BaselineProcess.Path)
    $BaselineBinaryHash = Get-Sha256 -Path $BaselineBinaryPath
    $BaselineBinaryVersion = Get-BinaryVersionMetadata -BinaryPath $BaselineBinaryPath
    if ([string]$Baseline.version -ne $BaselineBinaryVersion.Version) {
        throw "Validator API and running binary report different versions."
    }
    Assert-RevisionMatches `
        -Actual ([string]$Baseline.git_sha) `
        -Expected $BaselineBinaryVersion.Revision `
        -Description "Running validator"

    $Result.from_version = [string]$Baseline.version
    $Result.from_revision = [string]$Baseline.git_sha
    $Result.node_id = [string]$Baseline.node_id
    $Result.baseline_chain_tip = [long]$Baseline.chain_tip

    if ([string]$Baseline.version -eq $ExpectedVersion -and $BaselineBinaryHash -eq $TargetBinaryHash) {
        Write-ActiveBinaryState `
            -BinaryPath $BaselineBinaryPath `
            -Sha256 $BaselineBinaryHash `
            -Version $BaselineBinaryVersion.Version `
            -Revision $BaselineBinaryVersion.Revision
        $Result.success = $true
        $Result.status = "already-current"
        $Result.final_chain_tip = [long]$Baseline.chain_tip
        $Result.peers = [int]$Baseline.peers
        $Result.task_actions_ready = [bool]$Baseline.task_actions_ready
        $Result.binary = $BaselineBinaryPath
        $Result.completed_at_utc = [DateTime]::UtcNow.ToString("o")
        Write-UpdateResult
        [pscustomobject]$Result
        return
    }

    $TargetBinaryPath = Join-Path $LocalRoot "qsdm-local-validator-sqlite.$ExpectedVersion.exe"
    $stagedTargetPath = Join-Path $LocalRoot ".qsdm-local-validator-sqlite.$ExpectedVersion.$TransactionID.tmp.exe"
    Copy-Item -LiteralPath $package.Binary -Destination $stagedTargetPath
    if ((Get-Sha256 -Path $stagedTargetPath) -ne $TargetBinaryHash) {
        throw "Managed staging copy failed checksum verification."
    }

    Backup-Baseline
    $Result.status = "updating"
    Write-UpdateResult
    $WatchdogWasStopped = Stop-Watchdog
    $ActivationStarted = $true
    Stop-Validator

    if (Test-Path -LiteralPath $TargetBinaryPath) {
        $existingTargetHash = Get-Sha256 -Path $TargetBinaryPath
        if ($existingTargetHash -ne $TargetBinaryHash) {
            Move-Item `
                -LiteralPath $TargetBinaryPath `
                -Destination (Join-Path $BackupDir ("preexisting-" + [IO.Path]::GetFileName($TargetBinaryPath)))
        } else {
            Remove-Item -LiteralPath $stagedTargetPath -Force
            $stagedTargetPath = ""
        }
    }
    if (-not [string]::IsNullOrWhiteSpace($stagedTargetPath)) {
        Move-Item -LiteralPath $stagedTargetPath -Destination $TargetBinaryPath
    }
    if ((Get-Sha256 -Path $TargetBinaryPath) -ne $TargetBinaryHash) {
        throw "Activated validator binary failed checksum verification."
    }
    Write-ActiveBinaryState `
        -BinaryPath $TargetBinaryPath `
        -Sha256 $TargetBinaryHash `
        -Version $TargetVersionMetadata.Version `
        -Revision $TargetVersionMetadata.Revision

    Start-Validator
    $updated = Assert-RunningValidator `
        -ExpectedPath $TargetBinaryPath `
        -ExpectedHash $TargetBinaryHash `
        -ExpectedVersionValue $ExpectedVersion `
        -ExpectedRevisionValue $ExpectedRevision `
        -ExpectedNodeID ([string]$Baseline.node_id) `
        -MinimumChainTip ([long]$Baseline.chain_tip)
    $watchdog = Start-Watchdog
    $WatchdogRestored = $true

    $Result.success = $true
    $Result.status = "succeeded"
    $Result.final_chain_tip = [long]$updated.chain_tip
    $Result.peers = [int]$updated.peers
    $Result.task_actions_ready = [bool]$updated.task_actions_ready
    $Result.binary = [IO.Path]::GetFullPath($TargetBinaryPath)
    $Result.supervisor = [string]$watchdog.Mode
    $Result.pruned_backups = @(Remove-ExpiredBackups)
    $Result.completed_at_utc = [DateTime]::UtcNow.ToString("o")
    Write-UpdateResult
    [pscustomobject]$Result
} catch {
    $originalFailure = $_
    $Result.error = $originalFailure.Exception.Message
    if ($ActivationStarted -and $null -ne $Baseline -and -not [string]::IsNullOrWhiteSpace($BackupDir)) {
        try {
            Restore-Baseline
        } catch {
            $Result.error = "$($originalFailure.Exception.Message) Rollback also failed: $($_.Exception.Message)"
        }
    } elseif ($WatchdogWasStopped -and -not $WatchdogRestored) {
        try {
            $watchdog = Start-Watchdog
            $WatchdogRestored = $true
            $Result.supervisor = [string]$watchdog.Mode
        } catch {
            $Result.error = "$($originalFailure.Exception.Message) Watchdog restoration also failed: $($_.Exception.Message)"
        }
    }
    if ($Result.status -notin @("rolled-back", "rollback-failed")) {
        $Result.status = "failed"
    }
    $Result.completed_at_utc = [DateTime]::UtcNow.ToString("o")
    Write-UpdateResult
    throw $Result.error
} finally {
    if ($null -ne $UpdateLock) {
        $UpdateLock.Dispose()
        $UpdateLock = $null
    }
    if (Test-Path -LiteralPath $TransactionRoot -PathType Container) {
        if (-not [string]::IsNullOrWhiteSpace($BackupDir) -and (Test-Path -LiteralPath $BackupDir -PathType Container)) {
            $commandLogs = Join-Path $BackupDir "command-logs"
            if (Test-Path -LiteralPath $CommandLogRoot -PathType Container) {
                Copy-Item -LiteralPath $CommandLogRoot -Destination $commandLogs -Recurse -Force
            }
        }
        if (Test-PathUnderRoot -Path $TransactionRoot -Root $StagingRoot) {
            Remove-Item -LiteralPath $TransactionRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}
