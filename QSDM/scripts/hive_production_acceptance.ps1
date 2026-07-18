param(
    [string]$ExpectedVersion = "",
    [string]$ExpectedCommit = "",
    [string]$ReleaseBaseUrl = "https://qsdm.tech/downloads",
    [string[]]$CoreApiBases = @(
        "http://127.0.0.1:8080/api/v1",
        "https://api.qsdm.tech/attest/home-validator/api/v1",
        "https://api.qsdm.tech/api/v1"
    ),
    [string]$WalletAddress = "",
    [string]$HiveExecutable = "",
    [string]$QsdmCliPath = "",
    [string]$OutputPath = "",
    [ValidateRange(1, 60)]
    [int]$GpuSampleSeconds = 5,
    [ValidateRange(1, 1440)]
    [int]$LogWindowMinutes = 30,
    [switch]$RequireGpuMining,
    [switch]$StrictWarnings,
    [switch]$SkipGpu,
    [switch]$SkipLogScan
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
Set-StrictMode -Version Latest

Add-Type -AssemblyName System.Net.Http -ErrorAction Stop

$script:checks = [Collections.Generic.List[object]]::new()
$script:http = [Net.Http.HttpClient]::new()
$script:http.Timeout = [TimeSpan]::FromSeconds(15)
$startedAt = (Get-Date).ToUniversalTime()

function Add-AcceptanceCheck {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,
        [Parameter(Mandatory = $true)]
        [ValidateSet("pass", "warn", "fail", "skip")]
        [string]$Status,
        [Parameter(Mandatory = $true)]
        [string]$Summary,
        [hashtable]$Data = @{}
    )

    $script:checks.Add([ordered]@{
        name = $Name
        status = $Status
        summary = $Summary
        data = $Data
    })

    $color = switch ($Status) {
        "pass" { "Green" }
        "warn" { "Yellow" }
        "fail" { "Red" }
        default { "DarkGray" }
    }
    Write-Host ("[{0}] {1}: {2}" -f $Status.ToUpperInvariant(), $Name, $Summary) `
        -ForegroundColor $color
}

function Get-HttpPayload {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Uri,
        [ValidateSet("GET", "HEAD")]
        [string]$Method = "GET"
    )

    $request = [Net.Http.HttpRequestMessage]::new(
        [Net.Http.HttpMethod]::new($Method),
        $Uri
    )
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $response = $null
    try {
        $response = $script:http.SendAsync($request).GetAwaiter().GetResult()
        $watch.Stop()
        if ($Method -eq "HEAD") {
            $bytes = [byte[]]::new(0)
            $text = ""
        } else {
            $bytes = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
            $text = [Text.Encoding]::UTF8.GetString($bytes)
        }
        if (-not $response.IsSuccessStatusCode) {
            throw "HTTP $([int]$response.StatusCode) from $Uri"
        }
        return [pscustomobject]@{
            Uri = $Uri
            Bytes = $bytes
            Text = $text
            DurationMs = [long]$watch.ElapsedMilliseconds
            ContentLength = $response.Content.Headers.ContentLength
        }
    } finally {
        if ($null -ne $response) {
            $response.Dispose()
        }
        $request.Dispose()
    }
}

function Get-Sha256Hex {
    param([byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return [BitConverter]::ToString($sha.ComputeHash($Bytes)).Replace("-", "").ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Get-ObjectProperty {
    param(
        [AllowNull()]
        [object]$Object,
        [Parameter(Mandatory = $true)]
        [string]$Name,
        [AllowNull()]
        [object]$Default = $null
    )

    if ($null -eq $Object) {
        return $Default
    }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value) {
        return $Default
    }
    return $property.Value
}

function Get-ManifestVersion {
    param([string]$ManifestText)
    $match = [regex]::Match($ManifestText, '(?m)^version:\s*[''\"]?([^''\"\r\n]+)')
    if (-not $match.Success) {
        throw "Updater manifest does not contain a version"
    }
    return $match.Groups[1].Value.Trim()
}

function Get-ManifestPackageName {
    param([string]$ManifestText)
    $match = [regex]::Match($ManifestText, '(?m)^path:\s*[''\"]?([^''\"\r\n]+)')
    if (-not $match.Success) {
        $match = [regex]::Match(
            $ManifestText,
            '(?m)^\s*-\s*url:\s*[''\"]?([^''\"\r\n]+)'
        )
    }
    if (-not $match.Success) {
        throw "Updater manifest does not contain a package path"
    }
    return $match.Groups[1].Value.Trim()
}

function Find-HiveExecutable {
    if ($HiveExecutable -and (Test-Path -LiteralPath $HiveExecutable -PathType Leaf)) {
        return (Resolve-Path -LiteralPath $HiveExecutable).Path
    }

    $running = Get-Process -Name "QSDM Hive" -ErrorAction SilentlyContinue |
        Where-Object { $_.Path } |
        Select-Object -First 1
    if ($running) {
        return $running.Path
    }

    $candidates = @()
    if ($env:LOCALAPPDATA) {
        $candidates += Join-Path $env:LOCALAPPDATA `
            "Programs\qsdm-hive-runtime\QSDM Hive.exe"
    }
    return $candidates |
        Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } |
        Select-Object -First 1
}

function Find-QsdmCli {
    if ($QsdmCliPath -and (Test-Path -LiteralPath $QsdmCliPath -PathType Leaf)) {
        return (Resolve-Path -LiteralPath $QsdmCliPath).Path
    }

    $candidates = [Collections.Generic.List[string]]::new()
    $hive = Find-HiveExecutable
    if ($hive) {
        $candidates.Add((Join-Path (Split-Path -Parent $hive) `
            "resources\native\qsdmcli.exe"))
    }
    $candidates.Add((Join-Path $PSScriptRoot `
        "..\source\.cache\local-validator\qsdmcli.exe"))
    $candidates.Add((Join-Path $PSScriptRoot `
        "..\..\apps\qsdm-hive\qsdm-hive-main\native\windows\x64\qsdmcli.exe"))

    return $candidates |
        Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } |
        Select-Object -First 1
}

function Test-ReleaseChannel {
    param(
        [ValidateSet("windows", "linux")]
        [string]$Platform,
        [string]$UpdaterName,
        [string]$ReleaseManifestName
    )

    try {
        $updater = Get-HttpPayload "$ReleaseBaseUrl/$UpdaterName"
        $release = Get-HttpPayload "$ReleaseBaseUrl/$ReleaseManifestName"
        $version = Get-ManifestVersion $updater.Text
        $packageName = Get-ManifestPackageName $updater.Text
        $envelope = $release.Text | ConvertFrom-Json
        $manifestBytes = [Convert]::FromBase64String(
            [string]$envelope.manifest_base64
        )
        $manifest = [Text.Encoding]::UTF8.GetString($manifestBytes) |
            ConvertFrom-Json

        if ($envelope.schema -ne "qsdm.signed-release.v1" -or
            $envelope.algorithm -ne "ML-DSA-87") {
            throw "release envelope schema or algorithm is invalid"
        }
        if ($manifest.platform -ne $Platform -or $manifest.version -ne $version) {
            throw "signed release identity does not match the updater manifest"
        }
        if ($ExpectedVersion -and $version -ne $ExpectedVersion) {
            throw "published version $version does not equal expected $ExpectedVersion"
        }
        if ($ExpectedCommit -and $manifest.commit -ne $ExpectedCommit) {
            throw "signed commit $($manifest.commit) does not equal expected $ExpectedCommit"
        }
        if ([DateTimeOffset]::Parse([string]$manifest.expires_at) -le
            [DateTimeOffset]::UtcNow) {
            throw "signed release manifest is expired"
        }

        $updaterArtifact = @($manifest.artifacts | Where-Object {
            $_.name -eq $UpdaterName
        }) | Select-Object -First 1
        $packageArtifact = @($manifest.artifacts | Where-Object {
            $_.name -eq $packageName
        }) | Select-Object -First 1
        if (-not $updaterArtifact -or -not $packageArtifact) {
            throw "signed release omits updater metadata or package"
        }
        if ((Get-Sha256Hex $updater.Bytes) -ne $updaterArtifact.sha256 -or
            [long]$updater.Bytes.Length -ne [long]$updaterArtifact.size) {
            throw "live updater metadata does not match the signed artifact"
        }

        $head = Get-HttpPayload "$ReleaseBaseUrl/$packageName" -Method HEAD
        if ($null -ne $head.ContentLength -and
            [long]$head.ContentLength -ne [long]$packageArtifact.size) {
            throw "public package size does not match the signed artifact"
        }

        $trustPath = Join-Path $PSScriptRoot `
            "..\deploy\release-trust\qsdm-hive-release-key.json"
        $cli = Find-QsdmCli
        $signatureVerified = $false
        if ((Test-Path -LiteralPath $trustPath -PathType Leaf) -and $cli) {
            $trust = Get-Content -Raw -LiteralPath $trustPath | ConvertFrom-Json
            if ($trust.key_id -ne $envelope.key_id -or
                $trust.key_id -ne $manifest.key_id) {
                throw "release key ID does not match the pinned trust root"
            }
            $temporaryPayload = Join-Path ([IO.Path]::GetTempPath()) `
                ("qsdm-hive-acceptance-{0}.json" -f [Guid]::NewGuid().ToString("N"))
            try {
                [IO.File]::WriteAllBytes($temporaryPayload, $manifestBytes)
                & $cli wallet verify `
                    --public-key $trust.public_key `
                    --message-file $temporaryPayload `
                    --signature $envelope.signature 2>$null | Out-Null
                if ($LASTEXITCODE -ne 0) {
                    throw "ML-DSA release signature verification failed"
                }
                $signatureVerified = $true
            } finally {
                Remove-Item -LiteralPath $temporaryPayload -Force `
                    -ErrorAction SilentlyContinue
            }
        }

        $status = if ($signatureVerified) { "pass" } else { "warn" }
        $summary = if ($signatureVerified) {
            "$version is current; signed metadata and package size verified"
        } else {
            "$version metadata is internally consistent; signature verifier unavailable"
        }
        Add-AcceptanceCheck "Release channel ($Platform)" $status $summary @{
            version = $version
            commit = [string]$manifest.commit
            package = $packageName
            package_size = [long]$packageArtifact.size
            metadata_ms = [long]($updater.DurationMs + $release.DurationMs)
            signature_verified = $signatureVerified
        }
        return $version
    } catch {
        Add-AcceptanceCheck "Release channel ($Platform)" "fail" `
            $_.Exception.Message
        return $null
    }
}

function Test-HiveRuntime {
    $hiveProcesses = @(Get-Process -Name "QSDM Hive" `
        -ErrorAction SilentlyContinue)
    $hive = Find-HiveExecutable
    if (-not $hive -or $hiveProcesses.Count -eq 0) {
        Add-AcceptanceCheck "Hive runtime" "fail" "QSDM Hive is not running"
        return
    }

    $productVersion = (Get-Item -LiteralPath $hive).VersionInfo.ProductVersion
    $normalizedVersion = [regex]::Replace(
        [string]$productVersion,
        '^(\d+\.\d+\.\d+)\.0$',
        '$1'
    )
    if ($ExpectedVersion -and $normalizedVersion -ne $ExpectedVersion) {
        Add-AcceptanceCheck "Hive runtime" "fail" `
            "installed $normalizedVersion; required $ExpectedVersion" @{
                version = $normalizedVersion
                process_count = $hiveProcesses.Count
            }
    } else {
        Add-AcceptanceCheck "Hive runtime" "pass" `
            "Hive $normalizedVersion is running" @{
                version = $normalizedVersion
                process_count = $hiveProcesses.Count
            }
    }

    try {
        $records = @(Get-CimInstance Win32_Process -ErrorAction Stop |
            Where-Object { $_.Name -eq "QSDM Hive.exe" })
        $ids = @($records.ProcessId)
        $roots = @($records | Where-Object {
            $_.ParentProcessId -notin $ids
        })
        if ($roots.Count -gt 1) {
            Add-AcceptanceCheck "Hive process topology" "fail" `
                "$($roots.Count) independent Hive browser roots are running" @{
                    root_count = $roots.Count
                    process_count = $records.Count
                }
        } else {
            Add-AcceptanceCheck "Hive process topology" "pass" `
                "one browser root with $($records.Count - 1) normal child processes" @{
                    root_count = $roots.Count
                    process_count = $records.Count
                }
        }
    } catch {
        Add-AcceptanceCheck "Hive process topology" "warn" `
            "browser-root count requires process-query permission" @{
                process_count = $hiveProcesses.Count
            }
    }
}

function Test-CoreConnectivity {
    $reachable = [Collections.Generic.List[object]]::new()
    foreach ($base in $CoreApiBases) {
        try {
            $response = Get-HttpPayload "$($base.TrimEnd('/'))/status"
            $status = $response.Text | ConvertFrom-Json
            $tip = [long](Get-ObjectProperty $status "chain_tip" 0)
            if ($tip -le 0) {
                throw "status returned a non-positive chain tip"
            }
            $reachable.Add([pscustomobject]@{
                Base = $base.TrimEnd('/')
                Tip = $tip
                Peers = Get-ObjectProperty $status "peers"
                Role = [string](Get-ObjectProperty $status "node_role" "")
                DurationMs = $response.DurationMs
                Public = $base.StartsWith("https://")
            })
        } catch {
            Add-AcceptanceCheck "Core endpoint" "warn" `
                "$base is unavailable" @{ endpoint = $base }
        }
    }

    if ($reachable.Count -eq 0) {
        Add-AcceptanceCheck "Core connectivity" "fail" `
            "no configured QSDM Core endpoint answered"
        return $null
    }
    if (-not @($reachable | Where-Object Public)) {
        Add-AcceptanceCheck "Canonical gateway" "fail" `
            "local Core answered, but no HTTPS canonical gateway answered"
    } else {
        Add-AcceptanceCheck "Canonical gateway" "pass" `
            "canonical QSDM Core is reachable"
    }

    $tips = @($reachable.Tip)
    $spread = ($tips | Measure-Object -Maximum).Maximum -
        ($tips | Measure-Object -Minimum).Minimum
    $syncStatus = if ($spread -le 50) { "pass" } else { "warn" }
    Add-AcceptanceCheck "Chain synchronization" $syncStatus `
        "$($reachable.Count) endpoint(s), height spread $spread" @{
            endpoint_count = $reachable.Count
            minimum_height = ($tips | Measure-Object -Minimum).Minimum
            maximum_height = ($tips | Measure-Object -Maximum).Maximum
            height_spread = $spread
        }

    $selected = @($reachable | Where-Object Public | Sort-Object DurationMs |
        Select-Object -First 1)
    if ($selected.Count -eq 0) {
        $selected = @($reachable | Sort-Object DurationMs | Select-Object -First 1)
    }
    return $selected[0].Base
}

function Test-TaskAndWalletReads {
    param([string]$CoreBase)
    if (-not $CoreBase) {
        Add-AcceptanceCheck "Task catalog" "skip" "Core is unavailable"
        Add-AcceptanceCheck "Signer wallet" "skip" "Core is unavailable"
        return
    }

    try {
        $tasksResponse = Get-HttpPayload "$CoreBase/tasks"
        $tasksJson = $tasksResponse.Text | ConvertFrom-Json
        $taskList = if ($tasksJson -is [Array]) {
            @($tasksJson)
        } elseif ($null -ne (Get-ObjectProperty $tasksJson "tasks")) {
            @((Get-ObjectProperty $tasksJson "tasks"))
        } else {
            @()
        }
        if ($taskList.Count -eq 0) {
            throw "task catalog is empty"
        }
        $taskIds = @($taskList | ForEach-Object {
            $taskId = [string](Get-ObjectProperty $_ "task_id" "")
            if (-not $taskId) {
                $taskId = [string](Get-ObjectProperty $_ "id" "")
            }
            if ($taskId) {
                $taskId
            }
        } | Sort-Object -Unique)
        $requiredTaskIds = @(
            "qsdm-edge-worker",
            "qsdm-edge-worker-gpu",
            "qsdm-edge-worker-ram",
            "qsdm-mother-hive",
            "qsdm-skyfang-wallet-link",
            "qsdm-system-miner"
        )
        $missingTaskIds = @($requiredTaskIds | Where-Object {
            $_ -notin $taskIds
        })
        if ($missingTaskIds.Count -gt 0) {
            throw "task catalog omits required production tasks: $($missingTaskIds -join ', ')"
        }
        Add-AcceptanceCheck "Task catalog" "pass" `
            "$($taskList.Count) task(s); required production set is present" @{
                task_count = $taskList.Count
                required_task_count = $requiredTaskIds.Count
                latency_ms = $tasksResponse.DurationMs
            }
    } catch {
        Add-AcceptanceCheck "Task catalog" "fail" $_.Exception.Message
    }

    try {
        $motherResponse = Get-HttpPayload `
            "$CoreBase/tasks/qsdm-mother-hive/state"
        $motherState = $motherResponse.Text | ConvertFrom-Json
        $configured = [bool](Get-ObjectProperty $motherState "configured" $false)
        $motherTask = Get-ObjectProperty $motherState "task"
        if (-not $configured -or $null -eq $motherTask) {
            throw "Mother Hive protocol task is not configured"
        }
        $runningCount = [int](Get-ObjectProperty $motherTask "running_count" 0)
        Add-AcceptanceCheck "Mother Hive protocol" "pass" `
            "chain task is configured; $runningCount active participant(s)" @{
                running_count = $runningCount
                latency_ms = $motherResponse.DurationMs
            }
    } catch {
        Add-AcceptanceCheck "Mother Hive protocol" "fail" `
            $_.Exception.Message
    }

    if (-not $WalletAddress -and $env:APPDATA) {
        $walletPath = Join-Path $env:APPDATA "qsdm-hive\hive-signer\wallet.json"
        if (Test-Path -LiteralPath $walletPath -PathType Leaf) {
            try {
                $wallet = Get-Content -Raw -LiteralPath $walletPath |
                    ConvertFrom-Json
                $WalletAddress = [string](Get-ObjectProperty $wallet "address" "")
            } catch {
            }
        }
    }
    if (-not $WalletAddress -or $WalletAddress -notmatch '^[0-9a-fA-F]{64}$') {
        Add-AcceptanceCheck "Signer wallet" "warn" `
            "no valid QSDM signer address was discovered"
        return
    }

    try {
        $encoded = [Uri]::EscapeDataString($WalletAddress)
        $balanceResponse = Get-HttpPayload `
            "$CoreBase/wallet/balance?address=$encoded"
        $nonceResponse = Get-HttpPayload "$CoreBase/wallet/nonce?sender=$encoded"
        $balance = $balanceResponse.Text | ConvertFrom-Json
        $nonce = $nonceResponse.Text | ConvertFrom-Json
        $balanceCell = Get-ObjectProperty $balance "balance_cell"
        $balanceFallback = Get-ObjectProperty $balance "balance"
        $nonceValue = Get-ObjectProperty $nonce "nonce"
        $balanceValue = if ($null -ne $balanceCell) {
            [string]$balanceCell
        } elseif ($null -ne $balanceFallback) {
            [string]$balanceFallback
        } else {
            "available"
        }
        Add-AcceptanceCheck "Signer wallet" "pass" `
            "wallet reads succeeded without exposing key material" @{
                address = "$($WalletAddress.Substring(0, 8))...$($WalletAddress.Substring(56))"
                balance_cell = $balanceValue
                nonce = if ($null -ne $nonceValue) { [long]$nonceValue } else { $null }
            }
    } catch {
        Add-AcceptanceCheck "Signer wallet" "fail" $_.Exception.Message
    }
}

function Test-GpuMining {
    if ($SkipGpu) {
        Add-AcceptanceCheck "NVIDIA mining" "skip" "GPU checks were disabled"
        return
    }
    $nvidiaSmi = Get-Command nvidia-smi.exe, nvidia-smi `
        -ErrorAction SilentlyContinue | Select-Object -First 1
    $miner = @(Get-Process -Name "qsdmminer-console" `
        -ErrorAction SilentlyContinue)
    $solver = @(Get-Process -Name "qsdm-miner-cuda-solver" `
        -ErrorAction SilentlyContinue)

    if (-not $nvidiaSmi) {
        $status = if ($RequireGpuMining) { "fail" } else { "skip" }
        Add-AcceptanceCheck "NVIDIA mining" $status `
            "nvidia-smi is not available"
        return
    }
    if ($miner.Count -eq 0) {
        $status = if ($RequireGpuMining) { "fail" } else { "warn" }
        Add-AcceptanceCheck "NVIDIA mining" $status `
            "NVIDIA is available, but the QSDM miner task is not running"
        return
    }
    if ($solver.Count -eq 0) {
        Add-AcceptanceCheck "NVIDIA mining" "fail" `
            "miner is running without the CUDA solver"
        return
    }

    $maximumUtilization = 0
    $maximumMemory = 0
    $gpuName = "NVIDIA GPU"
    for ($sample = 0; $sample -lt $GpuSampleSeconds; $sample++) {
        $rows = & $nvidiaSmi.Source `
            --query-gpu=name,utilization.gpu,memory.used `
            --format=csv,noheader,nounits 2>$null
        foreach ($row in @($rows)) {
            $parts = @($row -split ',' | ForEach-Object { $_.Trim() })
            if ($parts.Count -ge 3) {
                $gpuName = $parts[0]
                $utilization = 0
                $memory = 0
                [void][int]::TryParse($parts[1], [ref]$utilization)
                [void][int]::TryParse($parts[2], [ref]$memory)
                $maximumUtilization = [Math]::Max($maximumUtilization, $utilization)
                $maximumMemory = [Math]::Max($maximumMemory, $memory)
            }
        }
        if ($sample + 1 -lt $GpuSampleSeconds) {
            Start-Sleep -Seconds 1
        }
    }

    $computeApps = (& $nvidiaSmi.Source `
        --query-compute-apps=process_name `
        --format=csv,noheader 2>$null | Out-String)
    $solverVisible = $computeApps -match 'qsdm-miner-cuda-solver'
    if (-not $solverVisible) {
        Add-AcceptanceCheck "NVIDIA mining" "fail" `
            "CUDA solver process is not visible to the NVIDIA driver" @{
                gpu = $gpuName
                maximum_utilization_percent = $maximumUtilization
            }
        return
    }

    $status = if ($maximumUtilization -gt 0) { "pass" } else { "warn" }
    $summary = if ($maximumUtilization -gt 0) {
        "CUDA solver is active; observed up to $maximumUtilization% GPU use"
    } else {
        "CUDA solver is registered, but this short sample caught an idle interval"
    }
    Add-AcceptanceCheck "NVIDIA mining" $status $summary @{
        gpu = $gpuName
        miner_processes = $miner.Count
        solver_processes = $solver.Count
        solver_visible_to_driver = $solverVisible
        maximum_utilization_percent = $maximumUtilization
        maximum_memory_mib = $maximumMemory
        sample_seconds = $GpuSampleSeconds
    }
}

function Test-EdgeRuntime {
    $agents = @(Get-Process -Name "qsdm-edge-agent" `
        -ErrorAction SilentlyContinue)
    $controls = @(Get-Process -Name "qsdm-edge-control" `
        -ErrorAction SilentlyContinue)
    if ($agents.Count -gt 0) {
        Add-AcceptanceCheck "Edge Agent" "pass" `
            "$($agents.Count) Edge Agent process(es) running" @{
                process_count = $agents.Count
            }
    } else {
        Add-AcceptanceCheck "Edge Agent" "warn" `
            "Edge Agent is not running; pooled-resource tests are inactive"
    }

    if ($controls.Count -eq 0) {
        Add-AcceptanceCheck "Edge Control" "skip" `
            "Edge Control is not running on this computer"
        return
    }
    $client = [Net.Sockets.TcpClient]::new()
    try {
        $connect = $client.ConnectAsync("127.0.0.1", 7741)
        if (-not $connect.Wait([TimeSpan]::FromSeconds(3)) -or
            -not $client.Connected) {
            throw "port 7741 did not accept a connection"
        }
        Add-AcceptanceCheck "Edge Control" "pass" `
            "local authenticated control service is listening"
    } catch {
        Add-AcceptanceCheck "Edge Control" "fail" $_.Exception.Message
    } finally {
        $client.Dispose()
    }
}

function Test-MotherHiveRuntime {
    if (-not $env:APPDATA) {
        Add-AcceptanceCheck "Mother Hive runtime" "skip" `
            "APPDATA is unavailable"
        return
    }

    $tokenCandidates = @(
        (Join-Path $env:APPDATA `
            "qsdm-hive\namespace\qsdm-mother-hive\compute-gateway.token"),
        (Join-Path $env:APPDATA `
            "QSDM-Hive\namespace\qsdm-mother-hive\compute-gateway.token")
    )
    $tokenConfigured = @($tokenCandidates | Where-Object {
        Test-Path -LiteralPath $_ -PathType Leaf
    }).Count -gt 0
    $relayConfig = Join-Path $env:APPDATA "QSDM\edge-pool\mother-hive.json"
    $relayPaired = Test-Path -LiteralPath $relayConfig -PathType Leaf

    if (-not $tokenConfigured -and -not $relayPaired) {
        Add-AcceptanceCheck "Mother Hive runtime" "skip" `
            "Mother Hive is not configured on this computer"
        return
    }

    $client = [Net.Sockets.TcpClient]::new()
    $gatewayOnline = $false
    try {
        $connect = $client.ConnectAsync("127.0.0.1", 7742)
        $gatewayOnline = $connect.Wait([TimeSpan]::FromSeconds(3)) -and
            $client.Connected
    } catch {
        $gatewayOnline = $false
    } finally {
        $client.Dispose()
    }

    if ($tokenConfigured -and $gatewayOnline) {
        Add-AcceptanceCheck "Mother Hive runtime" "pass" `
            "local compute gateway is online" @{
                relay_paired = $relayPaired
                compute_gateway_online = $true
            }
    } else {
        Add-AcceptanceCheck "Mother Hive runtime" "warn" `
            "Mother Hive is configured but its compute gateway is not running" @{
                relay_paired = $relayPaired
                compute_gateway_online = $false
            }
    }
}

function Test-RecentHiveLogs {
    if ($SkipLogScan) {
        Add-AcceptanceCheck "Recent Hive logs" "skip" "log scan was disabled"
        return
    }
    if (-not $env:APPDATA) {
        Add-AcceptanceCheck "Recent Hive logs" "skip" `
            "APPDATA is unavailable"
        return
    }
    $logPath = Join-Path $env:APPDATA "qsdm-hive\logs\main.log"
    if (-not (Test-Path -LiteralPath $logPath -PathType Leaf)) {
        Add-AcceptanceCheck "Recent Hive logs" "warn" `
            "Hive main.log was not found"
        return
    }

    $cutoff = (Get-Date).AddMinutes(-$LogWindowMinutes)
    $errorCount = 0
    $timeoutCount = 0
    $disconnectCount = 0
    $fatalCount = 0
    $lineTimestamp = [DateTime]::MinValue
    foreach ($line in @(Get-Content -LiteralPath $logPath -Tail 5000)) {
        $match = [regex]::Match(
            $line,
            '^\[?(?<time>\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2})'
        )
        if ($match.Success) {
            [void][DateTime]::TryParse($match.Groups['time'].Value, [ref]$lineTimestamp)
        }
        if ($lineTimestamp -lt $cutoff) {
            continue
        }
        if ($line -match '(?i)\[(error|fatal)\]|uncaught runtime|unhandled') {
            $errorCount++
        }
        if ($line -match '(?i)fatal|uncaught runtime|unhandled rejection') {
            $fatalCount++
        }
        if ($line -match '(?i)timeout|ECONNABORTED|ETIMEDOUT') {
            $timeoutCount++
        }
        if ($line -match '(?i)disconnect|ECONNRESET|ECONNREFUSED') {
            $disconnectCount++
        }
    }

    $status = if ($fatalCount -gt 0) {
        "fail"
    } elseif ($errorCount -gt 0 -or $timeoutCount -gt 5 -or
        $disconnectCount -gt 5) {
        "warn"
    } else {
        "pass"
    }
    Add-AcceptanceCheck "Recent Hive logs" $status `
        "$errorCount error(s), $timeoutCount timeout(s), $disconnectCount disconnect(s)" @{
            window_minutes = $LogWindowMinutes
            errors = $errorCount
            fatal_or_uncaught = $fatalCount
            timeouts = $timeoutCount
            disconnects = $disconnectCount
        }
}

try {
    Write-Host "QSDM Hive production acceptance" -ForegroundColor Cyan
    Write-Host "Read-only checks; no wallet or task state will be changed.`n"

    $windowsVersion = Test-ReleaseChannel `
        -Platform windows `
        -UpdaterName "latest.yml" `
        -ReleaseManifestName "qsdm-hive-release-windows.json"
    $linuxVersion = Test-ReleaseChannel `
        -Platform linux `
        -UpdaterName "latest-linux.yml" `
        -ReleaseManifestName "qsdm-hive-release-linux.json"
    if (-not $ExpectedVersion) {
        if ($windowsVersion -and $windowsVersion -eq $linuxVersion) {
            $ExpectedVersion = $windowsVersion
        } else {
            Add-AcceptanceCheck "Cross-platform release parity" "fail" `
                "Windows and Linux updater versions differ"
        }
    } elseif ($windowsVersion -and $linuxVersion -and
        $windowsVersion -eq $linuxVersion) {
        Add-AcceptanceCheck "Cross-platform release parity" "pass" `
            "both updater channels require $ExpectedVersion"
    } else {
        Add-AcceptanceCheck "Cross-platform release parity" "fail" `
            "Windows and Linux release channels did not both validate"
    }

    Test-HiveRuntime
    $selectedCore = Test-CoreConnectivity
    Test-TaskAndWalletReads $selectedCore
    Test-GpuMining
    Test-EdgeRuntime
    Test-MotherHiveRuntime
    Test-RecentHiveLogs
} finally {
    $script:http.Dispose()
}

$counts = [ordered]@{
    passed = @($script:checks | Where-Object status -eq "pass").Count
    warnings = @($script:checks | Where-Object status -eq "warn").Count
    failed = @($script:checks | Where-Object status -eq "fail").Count
    skipped = @($script:checks | Where-Object status -eq "skip").Count
}
$finishedAt = (Get-Date).ToUniversalTime()
$report = [ordered]@{
    schema = "qsdm.hive.production-acceptance.v1"
    generated_at = $finishedAt.ToString("o")
    expected_version = $ExpectedVersion
    expected_commit = $ExpectedCommit
    operating_system = [Environment]::OSVersion.VersionString
    architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    duration_ms = [long]($finishedAt - $startedAt).TotalMilliseconds
    read_only = $true
    summary = $counts
    checks = $script:checks
}

if (-not $OutputPath) {
    $timestamp = $finishedAt.ToString("yyyyMMdd-HHmmss")
    $OutputPath = Join-Path (Get-Location) `
        "qsdm-hive-acceptance-$timestamp.json"
}
$outputDirectory = Split-Path -Parent $OutputPath
if ($outputDirectory -and -not (Test-Path -LiteralPath $outputDirectory)) {
    New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
}
$report | ConvertTo-Json -Depth 12 |
    Set-Content -LiteralPath $OutputPath -Encoding UTF8

Write-Host "`nEvidence: $OutputPath" -ForegroundColor Cyan
Write-Host ("Passed {0}; warnings {1}; failed {2}; skipped {3}" -f `
    $counts.passed, $counts.warnings, $counts.failed, $counts.skipped)

if ($counts.failed -gt 0 -or ($StrictWarnings -and $counts.warnings -gt 0)) {
    exit 1
}
