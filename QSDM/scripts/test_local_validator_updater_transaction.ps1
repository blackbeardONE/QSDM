#requires -Version 5.1

[CmdletBinding()]
param(
    [string]$QsdmRoot = "",
    [string]$TestRoot = "",
    [switch]$KeepArtifacts
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
if ([string]::IsNullOrWhiteSpace($TestRoot)) {
    $TestRoot = Join-Path ([IO.Path]::GetTempPath()) "qsdm-updater-transaction-$TestID"
}
$TestRoot = [IO.Path]::GetFullPath($TestRoot)
$FakeRoot = Join-Path $TestRoot "qsdm"
$FakeScripts = Join-Path $FakeRoot "scripts"
$LocalRoot = Join-Path $FakeRoot "source\.cache\local-validator"
$RunDir = Join-Path $LocalRoot "run-v2"
$PidPath = Join-Path $RunDir "qsdm.autostart.pid"
$WatchdogPidPath = Join-Path $LocalRoot "watchdog.pid"
$StatusUrl = ""
$HealthUrl = ""
$FakeCosign = Join-Path $TestRoot "cosign.exe"
$NodeID = "12D3KooWUpdaterTransactionFixture111111111111111111111"
$baseline = $null

function Assert-True {
    param(
        [Parameter(Mandatory)][bool]$Value,
        [Parameter(Mandatory)][string]$Message
    )
    if (-not $Value) {
        throw $Message
    }
}

function Get-FreeLoopbackPort {
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try {
        return ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    } finally {
        $listener.Stop()
    }
}

function Get-Compiler {
    $compiler = @(
        (Join-Path $env:SystemRoot "Microsoft.NET\Framework64\v4.0.30319\csc.exe"),
        (Join-Path $env:SystemRoot "Microsoft.NET\Framework\v4.0.30319\csc.exe")
    ) | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    if (-not $compiler) {
        throw "Windows C# compiler was not found."
    }
    return $compiler
}

function New-FakeValidator {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Version,
        [Parameter(Mandatory)][string]$Revision,
        [Parameter(Mandatory)][string]$ReportedNodeID,
        [Parameter(Mandatory)][long]$ChainTip,
        [Parameter(Mandatory)][int]$Port
    )

    $className = "QsdmValidator$([guid]::NewGuid().ToString('N'))"
    $escapedNode = $ReportedNodeID.Replace('\', '\\').Replace('"', '\"')
    $source = @"
using System;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;

public static class $className
{
    private static void Reply(NetworkStream stream, string body)
    {
        byte[] payload = Encoding.UTF8.GetBytes(body);
        string headers = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
            payload.Length + "\r\nConnection: close\r\n\r\n";
        byte[] prefix = Encoding.ASCII.GetBytes(headers);
        stream.Write(prefix, 0, prefix.Length);
        stream.Write(payload, 0, payload.Length);
        stream.Flush();
    }

    public static int Main(string[] args)
    {
        if (args.Length == 1 && args[0] == "--version")
        {
            Console.WriteLine("qsdm $Version ($Revision, 2026-07-31T00:00:00Z, fixture, windows/amd64)");
            return 0;
        }

        TcpListener listener = new TcpListener(IPAddress.Loopback, $Port);
        listener.Start();
        while (true)
        {
            try
            {
                using (TcpClient client = listener.AcceptTcpClient())
                using (NetworkStream stream = client.GetStream())
                using (StreamReader reader = new StreamReader(stream, Encoding.ASCII, false, 1024, true))
                {
                    string request = reader.ReadLine() ?? "";
                    string line;
                    do { line = reader.ReadLine(); } while (!string.IsNullOrEmpty(line));
                    if (request.Contains("/api/v1/status"))
                    {
                        Reply(stream, "{\"node_id\":\"$escapedNode\",\"version\":\"$Version\",\"git_sha\":\"$Revision\",\"chain_tip\":$ChainTip,\"peers\":1,\"task_actions_ready\":true}");
                    }
                    else
                    {
                        Reply(stream, "{\"status\":\"ready\"}");
                    }
                }
            }
            catch (IOException) { }
            catch (SocketException) { }
        }
    }
}
"@
    $sourcePath = "$Path.cs"
    [IO.File]::WriteAllText($sourcePath, $source, [Text.UTF8Encoding]::new($false))
    $compiler = Get-Compiler
    & $compiler /nologo /target:exe "/out:$Path" $sourcePath
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Could not build fake validator $Version."
    }
}

function New-FakeCosign {
    $className = "QsdmCosign$([guid]::NewGuid().ToString('N'))"
    $source = @"
using System;
public static class $className
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
    $sourcePath = "$FakeCosign.cs"
    [IO.File]::WriteAllText($sourcePath, $source, [Text.UTF8Encoding]::new($false))
    $compiler = Get-Compiler
    & $compiler /nologo /target:exe "/out:$FakeCosign" $sourcePath
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $FakeCosign -PathType Leaf)) {
        throw "Could not build fake Cosign verifier."
    }
}

function New-FakeWatchdog {
    param([Parameter(Mandatory)][string]$Path)

    $source = @'
param(
    [string]$QsdmRoot,
    [string]$Relay,
    [string]$Slot,
    [string]$Backend,
    [int]$IntervalSeconds,
    [int]$RestartAfterFailures,
    [int]$GatewayRestartAfterFailures,
    [switch]$CheckPublicGateway
)

$ErrorActionPreference = "Stop"
$localRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$pidPath = Join-Path $localRoot "watchdog.pid"
$identityPath = Join-Path $localRoot "watchdog.process.json"
New-Item -ItemType Directory -Force -Path $localRoot | Out-Null
$process = Get-Process -Id $PID -ErrorAction Stop
Add-Type -TypeDefinition @"
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
public static class QsdmFixtureProcessTimes
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
            if (!GetProcessTimes(process.Handle, out creationTime, out exitTime, out kernelTime, out userTime))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error());
            }
            return DateTime.FromFileTimeUtc(creationTime);
        }
    }
}
"@
$identity = [ordered]@{
    schema = "qsdm.watchdog-process.v1"
    pid = $PID
    process_start_utc = [QsdmFixtureProcessTimes]::GetStartTimeUtc($PID).ToString("o")
    process_path = $process.Path
    script = [IO.Path]::GetFullPath($PSCommandPath)
    script_sha256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()
    qsdm_root = [IO.Path]::GetFullPath($QsdmRoot)
    written_at_utc = [DateTime]::UtcNow.ToString("o")
}
[IO.File]::WriteAllText($pidPath, [string]$PID, [Text.UTF8Encoding]::new($false))
$tempPath = "$identityPath.tmp-$PID"
[IO.File]::WriteAllText(
    $tempPath,
    ($identity | ConvertTo-Json),
    [Text.UTF8Encoding]::new($false)
)
Move-Item -LiteralPath $tempPath -Destination $identityPath -Force
try {
    while ($true) {
        Start-Sleep -Seconds 1
    }
} finally {
    Remove-Item -LiteralPath $pidPath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $identityPath -Force -ErrorAction SilentlyContinue
}
'@
    [IO.File]::WriteAllText($Path, $source, [Text.UTF8Encoding]::new($false))
}

function New-ReleaseFixture {
    param(
        [Parameter(Mandatory)][string]$Version,
        [Parameter(Mandatory)][string]$Revision,
        [Parameter(Mandatory)][string]$ReportedNodeID,
        [Parameter(Mandatory)][long]$ChainTip,
        [Parameter(Mandatory)][int]$Port
    )

    $releaseRoot = Join-Path $TestRoot ("release-" + ($Version -replace '[^0-9A-Za-z.-]', '-'))
    $sourceRoot = Join-Path $releaseRoot "source"
    $packageRoot = Join-Path $sourceRoot "qsdm-validator-$Version-windows-amd64"
    New-Item -ItemType Directory -Force -Path $packageRoot | Out-Null
    $binary = Join-Path $packageRoot "qsdm-validator.exe"
    New-FakeValidator `
        -Path $binary `
        -Version $Version `
        -Revision $Revision `
        -ReportedNodeID $ReportedNodeID `
        -ChainTip $ChainTip `
        -Port $Port
    $binaryHash = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(
        (Join-Path $packageRoot "SHA256SUMS.txt"),
        "$binaryHash  qsdm-validator.exe`n",
        [Text.UTF8Encoding]::new($false)
    )

    Add-Type -AssemblyName System.IO.Compression.FileSystem | Out-Null
    $archive = Join-Path $releaseRoot "qsdm-validator-$Version-windows-amd64.zip"
    [IO.Compression.ZipFile]::CreateFromDirectory(
        $sourceRoot,
        $archive,
        [IO.Compression.CompressionLevel]::Optimal,
        $false
    )
    $archiveHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    $manifest = Join-Path $releaseRoot "SHA256SUMS"
    [IO.File]::WriteAllText(
        $manifest,
        "$archiveHash  $([IO.Path]::GetFileName($archive))`n",
        [Text.UTF8Encoding]::new($false)
    )
    $signature = Join-Path $releaseRoot "SHA256SUMS.sig"
    $certificate = Join-Path $releaseRoot "SHA256SUMS.cert.pem"
    [IO.File]::WriteAllText($signature, "fixture", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($certificate, "fixture", [Text.UTF8Encoding]::new($false))

    return [pscustomobject]@{
        Version = $Version
        Revision = $Revision
        BinaryHash = $binaryHash
        Archive = $archive
        Manifest = $manifest
        Signature = $signature
        Certificate = $certificate
    }
}

function Invoke-Update {
    param([Parameter(Mandatory)][pscustomobject]$Release)
    return & $Updater `
        -QsdmRoot $FakeRoot `
        -PackageArchivePath $Release.Archive `
        -ReleaseManifestPath $Release.Manifest `
        -ReleaseManifestSignaturePath $Release.Signature `
        -ReleaseManifestCertificatePath $Release.Certificate `
        -ExpectedVersion $Release.Version `
        -ExpectedRevision $Release.Revision `
        -CosignPath $FakeCosign `
        -HealthUrl $HealthUrl `
        -StatusUrl $StatusUrl `
        -HealthWaitSeconds 15 `
        -ProcessStopWaitSeconds 5 `
        -WatchdogWaitSeconds 10 `
        -Confirm:$false
}

function Wait-Status {
    param(
        [Parameter(Mandatory)][string]$Version,
        [int]$Seconds = 10
    )
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    do {
        try {
            $status = Invoke-RestMethod -Uri $StatusUrl -TimeoutSec 2
            if ([string]$status.version -eq $Version) {
                return $status
            }
        } catch {}
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Fake validator $Version did not become ready."
}

function Stop-TestProcessFromPid {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return
    }
    $text = (Get-Content -LiteralPath $Path -Raw).Trim()
    if ($text -notmatch '^[0-9]+$') {
        return
    }
    Stop-Process -Id ([int]$text) -Force -ErrorAction SilentlyContinue
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

try {
    Write-Host "phase=fixture-setup"
    New-Item -ItemType Directory -Force -Path $FakeScripts, $RunDir | Out-Null
    Copy-Item -LiteralPath $Launcher -Destination (Join-Path $FakeScripts "start_local_validator.ps1")
    New-FakeWatchdog -Path (Join-Path $FakeScripts "watch_local_stack.ps1")
    [IO.File]::WriteAllText((Join-Path $FakeRoot "qsdm.yaml"), "{}`n", [Text.UTF8Encoding]::new($false))
    New-FakeCosign

    $port = Get-FreeLoopbackPort
    $HealthUrl = "http://127.0.0.1:$port/api/v1/health/ready"
    $StatusUrl = "http://127.0.0.1:$port/api/v1/status"
    $baselineVersion = "v9.8.7-rc.5"
    $baselineRevision = "1111111"
    $baselinePath = Join-Path $LocalRoot "qsdm-local-validator-sqlite.$baselineVersion.exe"
    New-FakeValidator `
        -Path $baselinePath `
        -Version $baselineVersion `
        -Revision $baselineRevision `
        -ReportedNodeID $NodeID `
        -ChainTip 100 `
        -Port $port
    $baselineHash = (Get-FileHash -LiteralPath $baselinePath -Algorithm SHA256).Hash.ToLowerInvariant()
    $activeState = [ordered]@{
        schema = "qsdm.validator-active.v1"
        binary = [IO.Path]::GetFileName($baselinePath)
        sha256 = $baselineHash
        version = $baselineVersion
        revision = $baselineRevision
        activated_at_utc = [DateTime]::UtcNow.ToString("o")
    }
    [IO.File]::WriteAllText(
        (Join-Path $LocalRoot "validator-active.json"),
        ($activeState | ConvertTo-Json),
        [Text.UTF8Encoding]::new($false)
    )

    $baseline = Start-Process `
        -FilePath $baselinePath `
        -WorkingDirectory $RunDir `
        -WindowStyle Hidden `
        -PassThru
    [IO.File]::WriteAllText($PidPath, [string]$baseline.Id, [Text.UTF8Encoding]::new($false))
    [void](Wait-Status -Version $baselineVersion)

    Write-Host "phase=good-upgrade"
    $goodRelease = New-ReleaseFixture `
        -Version "v9.8.7-rc.6" `
        -Revision "2222222" `
        -ReportedNodeID $NodeID `
        -ChainTip 101 `
        -Port $port
    $updated = Invoke-Update -Release $goodRelease
    Assert-True -Value ([bool]$updated.success) -Message "Good update did not succeed."
    Assert-True -Value ([string]$updated.status -eq "succeeded") -Message "Good update returned an unexpected status."
    Assert-True -Value ([bool]$updated.signature_verified) -Message "Signed-manifest gate was not exercised."
    $goodStatus = Wait-Status -Version $goodRelease.Version
    Assert-True -Value ([string]$goodStatus.node_id -eq $NodeID) -Message "Node identity changed after the good update."
    Assert-True -Value ((Read-PidFile -Path $WatchdogPidPath) -gt 0) -Message "Watchdog was not restored after the good update."

    Write-Host "phase=bad-upgrade-and-rollback"
    $badRelease = New-ReleaseFixture `
        -Version "v9.8.7-rc.7" `
        -Revision "3333333" `
        -ReportedNodeID "12D3KooWWrongIdentityFixture22222222222222222222222" `
        -ChainTip 102 `
        -Port $port
    $badRejected = $false
    try {
        Invoke-Update -Release $badRelease | Out-Null
    } catch {
        $badRejected = $_.Exception.Message -like "*identity changed*"
    }
    Assert-True -Value $badRejected -Message "Bad replacement identity was not rejected."

    $latestResultPath = Join-Path $LocalRoot "update-results\latest.json"
    $rollbackResult = Get-Content -LiteralPath $latestResultPath -Raw | ConvertFrom-Json
    Assert-True -Value ([string]$rollbackResult.status -eq "rolled-back") -Message "Failed update did not report a completed rollback."
    Assert-True -Value ([bool]$rollbackResult.rollback_succeeded) -Message "Failed update did not restore the previous validator."
    $rolledBackStatus = Wait-Status -Version $goodRelease.Version
    Assert-True -Value ([string]$rolledBackStatus.node_id -eq $NodeID) -Message "Rollback did not restore the original node identity."
    $activeAfterRollback = Get-Content -LiteralPath (Join-Path $LocalRoot "validator-active.json") -Raw | ConvertFrom-Json
    Assert-True -Value ([string]$activeAfterRollback.version -eq $goodRelease.Version) -Message "Active binary record was not restored."

    Write-Host "phase=complete"
    [pscustomobject]@{
        schema = "qsdm.local-validator-updater-transaction-test.v1"
        success = $true
        update_succeeded = $true
        watchdog_restored = $true
        bad_identity_rejected = $true
        rollback_succeeded = $true
    } | ConvertTo-Json -Compress
} finally {
    Stop-TestProcessFromPid -Path $WatchdogPidPath
    Stop-TestProcessFromPid -Path $PidPath
    foreach ($process in @($baseline)) {
        if ($null -ne $process) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    if ($KeepArtifacts) {
        Write-Host "Transaction test artifacts retained at $TestRoot"
    } else {
        $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
        $resolvedTestRoot = [IO.Path]::GetFullPath($TestRoot)
        if ($resolvedTestRoot.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -and
            [IO.Path]::GetFileName($resolvedTestRoot) -like "qsdm-updater-transaction-*") {
            Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}
