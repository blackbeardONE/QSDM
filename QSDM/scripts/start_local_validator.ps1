param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$HealthUrl = "http://127.0.0.1:8080/api/v1/health/ready",
    [string]$TaskRegistryPath = "",
    [string]$TaskActionLogPath = "",
    [switch]$Networked,
    [string]$BootstrapPeers = "",
    [string]$ChainSyncUrls = "https://api.qsdm.tech/api/v1",
    [switch]$PublicP2P,
    [switch]$Restart,
    [string]$TreasuryConfigPath = "",
    [int]$HealthWaitSeconds = 15,
    [int]$LockWaitSeconds = 5
)

$ErrorActionPreference = "Stop"

$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$ModeConfigPath = Join-Path $LocalRoot "validator-mode.json"
$RunDirName = if ($Networked) { "run-networked" } else { "run-v2" }
$RunDir = Join-Path $LocalRoot $RunDirName
$NetworkHostKeyPath = Join-Path $RunDir "qsdm_network_host.key"
$DefaultBootstrapPeer = "/dns4/api.qsdm.tech/tcp/4001/p2p/12D3KooWRH4MGiaRYMZEr9LvdxYrpePT5LPbNqLTMGukD32yhkZ8"
$PrimaryExePath = Join-Path $LocalRoot "qsdm.exe"
$CandidateExePath = Join-Path $LocalRoot "qsdm-new.exe"
$LocalValidatorSQLiteHotfixExePath = Join-Path $LocalRoot "qsdm-local-validator-sqlite.hotfix.exe"
$LocalValidatorSQLiteCandidateExePath = Join-Path $LocalRoot "qsdm-local-validator-sqlite.candidate.exe"
$LocalValidatorSQLiteNewExePath = Join-Path $LocalRoot "qsdm-local-validator-sqlite.new.exe"
$LocalValidatorTaskCatalogExePath = Join-Path $LocalRoot "qsdm-local-validator-task-catalog.exe"
$LocalValidatorTreasuryExePath = Join-Path $LocalRoot "qsdm-local-validator-treasury.exe"
$LocalValidatorSQLiteExePath = Join-Path $LocalRoot "qsdm-local-validator-sqlite.exe"
$LocalValidatorHiveNewExePath = Join-Path $LocalRoot "qsdm-local-validator-hive.new.exe"
$LocalValidatorHiveExePath = Join-Path $LocalRoot "qsdm-local-validator-hive.exe"
$LocalValidatorExePath = Join-Path $LocalRoot "qsdm-local-validator.exe"
$SQLiteNextExePath = Join-Path $LocalRoot "qsdm-sqlite-next.exe"
$SQLiteExePath = Join-Path $LocalRoot "qsdm-sqlite.exe"

function Select-NewestExistingPath {
    param([string[]]$Paths)

    $items = foreach ($path in $Paths) {
        if (Test-Path -LiteralPath $path) {
            Get-Item -LiteralPath $path
        }
    }

    $items |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First 1 -ExpandProperty FullName
}

$ValidatorProcessNames = @(
    "qsdm-local-validator",
    "qsdm-local-validator-sqlite*",
    "qsdm-local-validator-task-catalog",
    "qsdm-local-validator-treasury",
    "qsdm-local-validator-hive",
    "qsdm-local-validator-hive.new",
    "qsdm-local-validator-next",
    "qsdm-sqlite-next",
    "qsdm-sqlite",
    "qsdm-new",
    "qsdm"
)
$DiscoveredSQLiteExePaths = @(
    Get-ChildItem -LiteralPath $LocalRoot -Filter "qsdm-local-validator-sqlite*.exe" -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -notlike "*.tmp.exe" } |
        Select-Object -ExpandProperty FullName
)
$ExePath = Select-NewestExistingPath -Paths (@(
    $LocalValidatorTreasuryExePath,
    $LocalValidatorTaskCatalogExePath,
    $LocalValidatorSQLiteHotfixExePath,
    $LocalValidatorSQLiteCandidateExePath,
    $LocalValidatorSQLiteNewExePath,
    $LocalValidatorSQLiteExePath
) + $DiscoveredSQLiteExePaths)
if (-not $ExePath) {
    $ExePath = Select-NewestExistingPath @(
        $LocalValidatorHiveNewExePath,
        $LocalValidatorHiveExePath,
        $LocalValidatorExePath,
        $SQLiteNextExePath,
        $SQLiteExePath
    )
}
if (-not $ExePath) {
    $ExePath = Select-NewestExistingPath @($CandidateExePath, $PrimaryExePath)
}
if (-not $ExePath) {
    $ExePath = $PrimaryExePath
}
$ConfigPath = Join-Path $QsdmRoot "qsdm.yaml"
$LauncherLog = Join-Path $RunDir "launcher.log"
$LauncherLockPath = Join-Path $RunDir "launcher.lock"
$StdoutLog = Join-Path $RunDir "stdout.autostart.log"
$StderrLog = Join-Path $RunDir "stderr.autostart.log"
$PidFile = Join-Path $RunDir "qsdm.autostart.pid"
$PrefundAccountsPath = Join-Path $RunDir "qsdm-prefund-accounts.txt"
$FaucetTokenPath = Join-Path $RunDir "qsdm-local-faucet.token"
$ReferralLedgerPath = Join-Path $RunDir "qsdm-referral-ledger.json"
if ([string]::IsNullOrWhiteSpace($TreasuryConfigPath)) {
    $TreasuryConfigPath = Join-Path $RunDir "qsdm-treasury.json"
}
if ([string]::IsNullOrWhiteSpace($TaskRegistryPath)) {
    $TaskRegistryPath = Join-Path $RunDir "qsdm-hive-tasks.json"
}
if ([string]::IsNullOrWhiteSpace($TaskActionLogPath)) {
    $TaskActionLogPath = Join-Path $RunDir "qsdm-hive-task-actions.jsonl"
}

New-Item -ItemType Directory -Force -Path $RunDir | Out-Null
if ($Networked) {
    $modeConfig = [ordered]@{
        mode = "networked"
        chainSyncUrls = $ChainSyncUrls
        bootstrapPeers = $BootstrapPeers
        publicP2P = $PublicP2P.IsPresent
        updatedAtUtc = [DateTime]::UtcNow.ToString("o")
    }
    $modeJson = $modeConfig | ConvertTo-Json
    [IO.File]::WriteAllText($ModeConfigPath, $modeJson, [Text.UTF8Encoding]::new($false))
}

function Write-LauncherLog {
    param([string]$Message)
    $stamp = Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"
    Add-Content -LiteralPath $LauncherLog -Value "$stamp $Message"
}

function Import-TreasuryConfig {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) {
        return
    }
    $cfg = Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
    if ($cfg.referral) {
        $env:QSDM_REFERRAL_REWARD_POOL_ENABLED = if ($cfg.referral.enabled) { "1" } else { "0" }
        $env:QSDM_REFERRAL_CLAIMS_ENABLED = if ($cfg.referral.claimsEnabled) { "1" } else { "0" }
        $env:QSDM_REFERRAL_TREASURY_SIGNER_URL = [string]$cfg.referral.signerUrl
        if ($cfg.referral.signerTokenFile) {
            $env:QSDM_REFERRAL_TREASURY_SIGNER_TOKEN_FILE = [string]$cfg.referral.signerTokenFile
            Remove-Item Env:QSDM_REFERRAL_TREASURY_SIGNER_TOKEN -ErrorAction SilentlyContinue
        } else {
            $env:QSDM_REFERRAL_TREASURY_SIGNER_TOKEN = [string]$cfg.referral.signerToken
            Remove-Item Env:QSDM_REFERRAL_TREASURY_SIGNER_TOKEN_FILE -ErrorAction SilentlyContinue
        }
        $env:QSDM_REFERRAL_REWARD_POOL_ADDRESS = [string]$cfg.referral.expectedAddress
        if ($cfg.referral.rewardCell) {
            $env:QSDM_REFERRAL_REWARD_CELL = [string]$cfg.referral.rewardCell
        }
        $env:QSDM_REFERRAL_LEDGER_PATH = $ReferralLedgerPath
    }
    if ($cfg.faucet) {
        $env:QSDM_LOCAL_CELL_FAUCET = if ($cfg.faucet.enabled) { "1" } else { "0" }
        $env:QSDM_FAUCET_TREASURY_SIGNER_URL = [string]$cfg.faucet.signerUrl
        if ($cfg.faucet.signerTokenFile) {
            $env:QSDM_FAUCET_TREASURY_SIGNER_TOKEN_FILE = [string]$cfg.faucet.signerTokenFile
            Remove-Item Env:QSDM_FAUCET_TREASURY_SIGNER_TOKEN -ErrorAction SilentlyContinue
        } else {
            $env:QSDM_FAUCET_TREASURY_SIGNER_TOKEN = [string]$cfg.faucet.signerToken
            Remove-Item Env:QSDM_FAUCET_TREASURY_SIGNER_TOKEN_FILE -ErrorAction SilentlyContinue
        }
        $env:QSDM_FAUCET_TREASURY_ADDRESS = [string]$cfg.faucet.expectedAddress
        if ($cfg.faucet.targetBalance) {
            $env:QSDM_LOCAL_CELL_FAUCET_TARGET_BALANCE = [string]$cfg.faucet.targetBalance
        }
        if ($cfg.faucet.maxGrant) {
            $env:QSDM_LOCAL_CELL_FAUCET_MAX_GRANT = [string]$cfg.faucet.maxGrant
        }
    }
    Write-LauncherLog "loaded production treasury configuration path=$Path"
}

function Ensure-TreasurySigners {
    if (-not (Test-Path -LiteralPath $TreasuryConfigPath)) {
        return
    }

    $cfg = Get-Content -LiteralPath $TreasuryConfigPath -Raw | ConvertFrom-Json
    $signerLauncher = Join-Path $PSScriptRoot "start_treasury_signer.ps1"
    if (-not (Test-Path -LiteralPath $signerLauncher)) {
        throw "Missing treasury signer launcher: $signerLauncher"
    }

    $definitions = @(
        @{ Role = "referral"; Config = $cfg.referral },
        @{ Role = "faucet"; Config = $cfg.faucet }
    )
    foreach ($definition in $definitions) {
        $role = [string]$definition.Role
        $entry = $definition.Config
        if ($null -eq $entry -or -not $entry.autoStart) {
            continue
        }

        $signerUri = [Uri]([string]$entry.signerUrl)
        if ($signerUri.Scheme -ne "http" -or $signerUri.Host -notin @("127.0.0.1", "localhost", "::1")) {
            throw "$role treasury signer auto-start requires a loopback HTTP signerUrl"
        }
        $expectedAddress = ([string]$entry.expectedAddress).Trim().ToLowerInvariant()
        if ($expectedAddress -notmatch '^[0-9a-f]{64}$') {
            throw "$role treasury expectedAddress is invalid"
        }

        $healthy = $false
        try {
            $health = Invoke-RestMethod -Uri "$($signerUri.Scheme)://$($signerUri.Authority)/healthz" -TimeoutSec 2
            $healthy = ($health.status -eq "ok" -and $health.role -eq $role -and ([string]$health.address).ToLowerInvariant() -eq $expectedAddress)
        } catch {
            $healthy = $false
        }
        if ($healthy) {
            Write-LauncherLog "$role treasury signer already healthy address=$expectedAddress port=$($signerUri.Port)"
            continue
        }

        $requiredPaths = @(
            [string]$entry.keystorePath,
            [string]$entry.passphraseFile,
            [string]$entry.signerTokenFile
        )
        foreach ($requiredPath in $requiredPaths) {
            if ([string]::IsNullOrWhiteSpace($requiredPath) -or -not (Test-Path -LiteralPath $requiredPath)) {
                throw "$role treasury signer required file is missing: $requiredPath"
            }
        }

        $maxPayout = if ([double]$entry.maxPayout -gt 0) { [double]$entry.maxPayout } elseif ($role -eq "referral") { 5 } else { 1 }
        $minimumReserve = if ([double]$entry.minimumReserve -ge 0) { [double]$entry.minimumReserve } elseif ($role -eq "referral") { 25 } else { 10 }
        $feeCell = if ([double]$entry.feeCell -ge 0) { [double]$entry.feeCell } else { 0.001 }

        & $signerLauncher `
            -Role $role `
            -KeystorePath ([string]$entry.keystorePath) `
            -PassphraseFile ([string]$entry.passphraseFile) `
            -TokenFile ([string]$entry.signerTokenFile) `
            -ApiUrl "http://127.0.0.1:8080" `
            -Port $signerUri.Port `
            -MaxPayout $maxPayout `
            -MinimumReserve $minimumReserve `
            -FeeCell $feeCell `
            -Restart

        $health = Invoke-RestMethod -Uri "$($signerUri.Scheme)://$($signerUri.Authority)/healthz" -TimeoutSec 5
        if ($health.status -ne "ok" -or $health.role -ne $role -or ([string]$health.address).ToLowerInvariant() -ne $expectedAddress) {
            throw "$role treasury signer health identity did not match configuration"
        }
        Write-LauncherLog "started $role treasury signer address=$expectedAddress port=$($signerUri.Port)"
    }
}

function Reset-OversizedDuplicateLog {
    param(
        [string]$Path,
        [long]$MaxBytes = 10MB
    )

    if (-not (Test-Path -LiteralPath $Path)) {
        return
    }
    $item = Get-Item -LiteralPath $Path
    if ($item.Length -le $MaxBytes) {
        return
    }
    try {
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText($Path, "", $utf8NoBom)
        Write-LauncherLog "cleared duplicate stream log path=$Path previous_bytes=$($item.Length)"
    } catch {
        Write-LauncherLog "could not clear duplicate stream log path=$Path error=$($_.Exception.Message)"
    }
}

$script:LauncherLock = $null
function Acquire-LauncherLock {
    $deadline = (Get-Date).AddSeconds([Math]::Max(1, $LockWaitSeconds))
    while ($null -eq $script:LauncherLock -and (Get-Date) -lt $deadline) {
        try {
            $script:LauncherLock = [System.IO.File]::Open(
                $LauncherLockPath,
                [System.IO.FileMode]::OpenOrCreate,
                [System.IO.FileAccess]::ReadWrite,
                [System.IO.FileShare]::None
            )
        } catch {
            Start-Sleep -Milliseconds 250
        }
    }
    if ($null -eq $script:LauncherLock) {
        Write-LauncherLog "another validator launcher is already running; refusing overlapping restart"
        Write-Host "Another validator launcher is already running. Try again in a few seconds."
        exit 2
    }
}

function Release-LauncherLock {
    if ($null -ne $script:LauncherLock) {
        $script:LauncherLock.Dispose()
        $script:LauncherLock = $null
    }
}

function Test-QsdmReady {
    try {
        $response = Invoke-WebRequest -Uri $HealthUrl -UseBasicParsing -TimeoutSec 1
        return $response.StatusCode -eq 200
    } catch {
        return $false
    }
}

function New-QsdmHiveLocalTask {
    return @{
        task_id = "qsdm-hive-local-task"
        task_name = "QSDM Hive Local Task"
        task_manager = "qsdm-local-validator"
        is_allowlisted = $true
        is_active = $true
        task_audit_program = "qsdm-hive-local-task"
        task_metadata = "qsdm-hive-local-task"
        task_description = "Local QSDM-native task for QSDM Hive integration."
        total_bounty_amount = 100.0
        bounty_amount_per_round = 1.0
        minimum_stake_amount = 1000000000.0
        round_time = 6
        starting_slot = 0
        submission_window = 3
        audit_window = 3
        task_executable_network = "IPFS"
        task_type = "KOII"
    }
}

function New-QsdmSystemMinerTask {
    return @{
        task_id = "qsdm-system-miner"
        task_name = "QSDM Miner"
        task_manager = "qsdm-system-miner-manager"
        is_allowlisted = $true
        is_active = $true
        task_audit_program = "qsdm-system-miner"
        task_metadata = "qsdm-system-miner-metadata"
        task_description = "Built-in QSDM system task for running the local NVIDIA miner. No separate Hive task stake is required. A new signer may start with 0 CELL and fill the slashable protocol bond from accepted mining rewards. Sky Fang is a separate integration and is not required for mining."
        total_bounty_amount = 0.0
        bounty_amount_per_round = 0.0
        # The validator-advertised mining bond is enforced by qsdm/enroll/v2.
        # Requiring another Hive catalog stake here would deadlock a new
        # zero-balance miner before it can earn the bond.
        minimum_stake_amount = 0.0
        round_time = 60
        starting_slot = 0
        submission_window = 0
        audit_window = 0
        task_executable_network = "ARWEAVE"
        task_type = "CELL"
        task_vars = '{"qsdm_system_task":true,"no_expiry":true,"reward_source":"protocol-mining-emission","hive_task_bounty":false,"separate_hive_stake":false,"protocol_bond_from_rewards":true}'
    }
}

function New-QsdmEdgeWorkerTask {
    return @{
        task_id = "qsdm-edge-worker"
        task_name = "QSDM Edge Worker CPU"
        task_manager = "qsdm-edge-worker-manager"
        is_allowlisted = $true
        is_active = $true
        task_audit_program = "qsdm-edge-worker"
        task_metadata = "qsdm-edge-worker-metadata"
        task_description = "Share bounded CPU capacity directly or through an authenticated laboratory coordinator. Verified completed jobs can earn CELL from a sponsor-funded task pool; participant self-funding is disabled by default."
        total_bounty_amount = 1.0
        bounty_amount_per_round = 0.05
        minimum_stake_amount = 1000000000.0
        round_time = 60
        starting_slot = 0
        submission_window = 0
        audit_window = 0
        task_executable_network = "ARWEAVE"
        task_type = "CELL"
        task_vars = '{"qsdm_system_task":true,"no_expiry":true,"resource_worker":"cpu","cpu_worker":true,"pooled_compute":true,"coordinator_receipts":true,"reward_source":"funded-pool","reward_per_round_cell":0.05,"reward_pool_target_cell":1}'
    }
}

function New-QsdmEdgeWorkerGpuTask {
    return @{
        task_id = "qsdm-edge-worker-gpu"
        task_name = "QSDM Edge Worker GPU"
        task_manager = "qsdm-edge-worker-gpu-manager"
        is_allowlisted = $true
        is_active = $true
        task_audit_program = "qsdm-edge-worker-gpu"
        task_metadata = "qsdm-edge-worker-gpu-metadata"
        task_description = "Share bounded NVIDIA CUDA capacity directly or through an authenticated laboratory coordinator. This is pooled compute, not QSDM protocol mining, and pays only verified jobs from a sponsor-funded task pool."
        total_bounty_amount = 2.0
        bounty_amount_per_round = 0.1
        minimum_stake_amount = 1000000000.0
        round_time = 60
        starting_slot = 0
        submission_window = 0
        audit_window = 0
        task_executable_network = "ARWEAVE"
        task_type = "CELL"
        task_vars = '{"qsdm_system_task":true,"no_expiry":true,"resource_worker":"gpu","cpu_worker":false,"pooled_compute":true,"coordinator_receipts":true,"reward_source":"funded-pool","reward_per_round_cell":0.1,"reward_pool_target_cell":2}'
    }
}

function New-QsdmEdgeWorkerRamTask {
    return @{
        task_id = "qsdm-edge-worker-ram"
        task_name = "QSDM Edge Worker RAM"
        task_manager = "qsdm-edge-worker-ram-manager"
        is_allowlisted = $true
        is_active = $true
        task_audit_program = "qsdm-edge-worker-ram"
        task_metadata = "qsdm-edge-worker-ram-metadata"
        task_description = "Share a bounded amount of RAM directly or through an authenticated laboratory coordinator. The agent runs fixed jobs only, wipes buffers after use, and pays verified jobs from a sponsor-funded task pool."
        total_bounty_amount = 1.0
        bounty_amount_per_round = 0.05
        minimum_stake_amount = 1000000000.0
        round_time = 60
        starting_slot = 0
        submission_window = 0
        audit_window = 0
        task_executable_network = "ARWEAVE"
        task_type = "CELL"
        task_vars = '{"qsdm_system_task":true,"no_expiry":true,"resource_worker":"ram","cpu_worker":false,"pooled_compute":true,"coordinator_receipts":true,"reward_source":"funded-pool","reward_per_round_cell":0.05,"reward_pool_target_cell":1}'
    }
}

function New-QsdmMotherHiveTask {
    return @{
        task_id = "qsdm-mother-hive"
        task_name = "Mother Hive Task"
        task_manager = "qsdm-mother-hive-manager"
        is_allowlisted = $true
        is_active = $true
        task_audit_program = "qsdm-mother-hive"
        task_metadata = "qsdm-mother-hive-metadata"
        task_description = "Run this QSDM Hive in Mother Hive mode for a paired Relay. It inventories pooled CPU, NVIDIA GPU, and RAM for QSDM-approved distributed jobs. The target revenue split is 70% contributors, 15% Mother Hive operator, and 15% CELL ecosystem reserve. Automatic settlement remains disabled until worker wallets and Relay receipts are chain-verifiable."
        total_bounty_amount = 0.0
        bounty_amount_per_round = 0.0
        minimum_stake_amount = 1000000000.0
        round_time = 60
        starting_slot = 0
        submission_window = 0
        audit_window = 0
        task_executable_network = "ARWEAVE"
        task_type = "CELL"
        task_vars = '{"qsdm_system_task":true,"no_expiry":true,"mother_hive_role":true,"qsdm_hive_only":true,"pooled_compute_consumer":true,"workload_mode":"qsdm-approved-distributed-jobs","contributor_share_percent":70,"mother_hive_share_percent":15,"ecosystem_share_percent":15,"ecosystem_treasury_address":"","settlement_active":false,"settlement_requirement":"wallet-bound workers, chain-verifiable Relay receipts, a published ecosystem treasury address, and funded workload escrow"}'
    }
}

function New-QsdmSkyFangLinkTask {
    return @{
        task_id = "qsdm-skyfang-wallet-link"
        task_name = "QSDM Sky Fang Link"
        task_manager = "qsdm-skyfang-link-manager"
        is_allowlisted = $true
        is_active = $true
        task_audit_program = "qsdm-skyfang-wallet-link"
        task_metadata = "qsdm-skyfang-wallet-link-metadata"
        task_description = "One-time QSDM Hive task that verifies your active QSDM signer is linked to a Sky Fang account. Link through QSDM Hive or log in at skyfang.xyz/dashboard/qsdm and Hive will detect the linked wallet automatically."
        total_bounty_amount = 25.0
        bounty_amount_per_round = 1.0
        minimum_stake_amount = 1000000000.0
        round_time = 3600
        starting_slot = 0
        submission_window = 0
        audit_window = 0
        task_executable_network = "ARWEAVE"
        task_type = "KOII"
        task_vars = '{"qsdm_system_task":true,"no_expiry":true,"skyfang_wallet_link":true,"one_time_reward":true,"reward_source":"funded-pool","reward_per_link_cell":1,"reward_pool_target_cell":25,"skyfang_base_url":"https://skyfang.xyz"}'
    }
}

function Upsert-QsdmHiveTask {
    param(
        [object[]]$Tasks,
        [hashtable]$Task,
        [switch]$ReplaceExisting
    )

    $updated = @()
    $found = $false
    foreach ($existingTask in @($Tasks)) {
        if ($existingTask.task_id -eq $Task.task_id) {
            $found = $true
            if ($ReplaceExisting) {
                $updated += $Task
            } else {
                $updated += $existingTask
            }
        } else {
            $updated += $existingTask
        }
    }

    if (-not $found) {
        $updated += $Task
    }

    return @($updated)
}

function Ensure-QsdmHiveTaskRegistry {
    $registryDir = Split-Path -Parent $TaskRegistryPath
    New-Item -ItemType Directory -Force -Path $registryDir | Out-Null

    $tasks = @()
    if (Test-Path -LiteralPath $TaskRegistryPath) {
        try {
            $existing = Get-Content -LiteralPath $TaskRegistryPath -Raw | ConvertFrom-Json
            if ($null -ne $existing.tasks) {
                $tasks = @($existing.tasks)
            } elseif ($null -ne $existing.task_id) {
                $tasks = @($existing)
            }
        } catch {
            Write-LauncherLog "could not parse task registry; recreating default registry at $TaskRegistryPath error=$($_.Exception.Message)"
        }
    }

    $beforeJson = @{ tasks = $tasks } | ConvertTo-Json -Depth 8
    $tasks = Upsert-QsdmHiveTask -Tasks $tasks -Task (New-QsdmHiveLocalTask)
    $tasks = Upsert-QsdmHiveTask -Tasks $tasks -Task (New-QsdmSystemMinerTask) -ReplaceExisting
    $tasks = Upsert-QsdmHiveTask -Tasks $tasks -Task (New-QsdmEdgeWorkerTask) -ReplaceExisting
    $tasks = Upsert-QsdmHiveTask -Tasks $tasks -Task (New-QsdmEdgeWorkerGpuTask) -ReplaceExisting
    $tasks = Upsert-QsdmHiveTask -Tasks $tasks -Task (New-QsdmEdgeWorkerRamTask) -ReplaceExisting
    $tasks = Upsert-QsdmHiveTask -Tasks $tasks -Task (New-QsdmMotherHiveTask) -ReplaceExisting
    $tasks = Upsert-QsdmHiveTask -Tasks $tasks -Task (New-QsdmSkyFangLinkTask) -ReplaceExisting
    $afterJson = @{ tasks = $tasks } | ConvertTo-Json -Depth 8
    $changed = ($beforeJson -ne $afterJson)

    if ($changed -or -not (Test-Path -LiteralPath $TaskRegistryPath)) {
        $registry = @{ tasks = $tasks } | ConvertTo-Json -Depth 8
        $registryTempPath = "$TaskRegistryPath.tmp-$PID"
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText($registryTempPath, $registry, $utf8NoBom)
        try {
            Move-Item -LiteralPath $registryTempPath -Destination $TaskRegistryPath -Force
        } catch {
            # Windows can deny rename-over-existing while the validator has the
            # registry open. A bounded in-place retry updates the catalog
            # without turning a metadata refresh into a validator restart.
            $writeError = $_.Exception.Message
            $updatedInPlace = $false
            for ($attempt = 1; $attempt -le 5 -and -not $updatedInPlace; $attempt++) {
                try {
                    [System.IO.File]::WriteAllText($TaskRegistryPath, $registry, $utf8NoBom)
                    $updatedInPlace = $true
                } catch {
                    $writeError = $_.Exception.Message
                    Start-Sleep -Milliseconds (100 * $attempt)
                }
            }
            if (-not $updatedInPlace) {
                throw "Could not update task registry after 5 bounded retries: $writeError"
            }
            Remove-Item -LiteralPath $registryTempPath -Force -ErrorAction SilentlyContinue
            Write-LauncherLog "updated QSDM Hive task registry in place because atomic replacement was unavailable"
        }
        Write-LauncherLog "ensured QSDM Hive task registry at $TaskRegistryPath"
    }
}

function Stop-ExistingValidator {
    if (Test-Path -LiteralPath $PidFile) {
        $pidText = (Get-Content -LiteralPath $PidFile -Raw).Trim()
        if ($pidText -match '^\d+$') {
            Stop-Process -Id ([int]$pidText) -Force -ErrorAction SilentlyContinue
        }
    }
    foreach ($name in $ValidatorProcessNames) {
        Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
            Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
        }
    }
}

function Test-ValidGovernanceSnapshot {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) {
        return $false
    }
    try {
        $bytes = [System.IO.File]::ReadAllBytes($Path)
        if ($bytes.Length -eq 0 -or [Array]::IndexOf($bytes, [byte]0) -ge 0) {
            return $false
        }
        $doc = [System.Text.Encoding]::UTF8.GetString($bytes) | ConvertFrom-Json -ErrorAction Stop
        return ($null -ne $doc.version -and $null -ne $doc.active)
    } catch {
        return $false
    }
}

function Repair-GovernanceSnapshot {
    $snapshot = Join-Path $RunDir "qsdm_governance.json"
    $lastGood = "$snapshot.last-good"
    if (-not (Test-Path -LiteralPath $snapshot)) {
        return
    }
    if (Test-ValidGovernanceSnapshot -Path $snapshot) {
        if (-not (Test-ValidGovernanceSnapshot -Path $lastGood)) {
            [System.IO.File]::Copy($snapshot, $lastGood, $true)
            Write-LauncherLog "seeded last-good governance snapshot"
        }
        return
    }
    if (-not (Test-ValidGovernanceSnapshot -Path $lastGood)) {
        throw "Governance snapshot is corrupt and no valid last-good copy exists: $snapshot"
    }

    $stamp = [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ")
    $quarantine = "$snapshot.corrupt-$stamp.bak"
    [System.IO.File]::Copy($snapshot, $quarantine, $false)
    [System.IO.File]::Copy($lastGood, $snapshot, $true)
    Write-LauncherLog "restored corrupt governance snapshot from last-good backup=$quarantine"
}

function Repair-ZeroFilledJournalTail {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) {
        return
    }
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $originalLength = $stream.Length
        if ($originalLength -eq 0) {
            return
        }
        $zeroStart = $originalLength
        $cursor = $originalLength
        $foundNonZero = $false
        while ($cursor -gt 0 -and -not $foundNonZero) {
            $size = [int][Math]::Min(65536, $cursor)
            $start = $cursor - $size
            $buffer = [byte[]]::new($size)
            $stream.Seek($start, [IO.SeekOrigin]::Begin) | Out-Null
            $read = $stream.Read($buffer, 0, $size)
            for ($i = $read - 1; $i -ge 0; $i--) {
                if ($buffer[$i] -eq 0) {
                    $zeroStart = $start + $i
                    continue
                }
                $foundNonZero = $true
                break
            }
            $cursor = $start
        }
        if ($zeroStart -eq $originalLength) {
            return
        }

        $truncateAt = $zeroStart
        if ($zeroStart -gt 0) {
            $stream.Seek($zeroStart - 1, [IO.SeekOrigin]::Begin) | Out-Null
            $previous = $stream.ReadByte()
            if ($previous -ne 10) {
                $cursor = $zeroStart
                $foundNewline = $false
                while ($cursor -gt 0 -and -not $foundNewline) {
                    $size = [int][Math]::Min(65536, $cursor)
                    $start = $cursor - $size
                    $buffer = [byte[]]::new($size)
                    $stream.Seek($start, [IO.SeekOrigin]::Begin) | Out-Null
                    $read = $stream.Read($buffer, 0, $size)
                    for ($i = $read - 1; $i -ge 0; $i--) {
                        if ($buffer[$i] -eq 10) {
                            $truncateAt = $start + $i + 1
                            $foundNewline = $true
                            break
                        }
                    }
                    $cursor = $start
                }
                if (-not $foundNewline) {
                    throw "Journal contains a zero-filled tail but no complete line to recover: $Path"
                }
            }
        }
    } finally {
        $stream.Dispose()
    }

    $stamp = [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ")
    $backup = "$Path.corrupt-tail-$stamp.bak"
    [IO.File]::Copy($Path, $backup, $false)
    $writer = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $writer.SetLength($truncateAt)
        $writer.Flush($true)
    } finally {
        $writer.Dispose()
    }
    Write-LauncherLog "trimmed zero-filled journal tail path=$Path original_bytes=$originalLength recovered_bytes=$truncateAt backup=$backup"
}

Ensure-QsdmHiveTaskRegistry
Acquire-LauncherLock

if ($Networked -and (Test-QsdmReady) -and -not $Restart) {
    Write-LauncherLog "networked validator requested but $HealthUrl is already occupied; switching to restart so networked run can bind local API ports"
    $Restart = $true
}

if ((Test-QsdmReady) -and -not $Restart) {
    Write-LauncherLog "validator already healthy at $HealthUrl"
    Ensure-TreasurySigners
    Release-LauncherLock
    exit 0
}

if ($Restart) {
    Write-LauncherLog "restart requested; stopping existing local validator"
    Stop-ExistingValidator
    Start-Sleep -Seconds 2
}

Repair-GovernanceSnapshot
Repair-ZeroFilledJournalTail -Path (Join-Path $RunDir "qsdm_receipts.ndjson")

if (-not (Test-Path -LiteralPath $ExePath)) {
    Write-LauncherLog "missing validator binary: $ExePath"
    throw "Missing validator binary: $ExePath"
}

if (-not (Test-Path -LiteralPath $ConfigPath)) {
    Write-LauncherLog "missing validator config: $ConfigPath"
    throw "Missing validator config: $ConfigPath"
}

$env:QSDM_SOLO_VALIDATOR_MODE = if ($Networked) { "0" } else { "1" }
Import-TreasuryConfig -Path $TreasuryConfigPath

$env:QSDM_NETWORKED_CATCHUP_MODE = if ($Networked) { "1" } else { "0" }
$env:QSDM_PRODUCTION_MODE = "true"
$env:QSDM_V2_ACTIVE = "1"
$env:QSDM_REQUIRE_SQLITE_STORAGE = "1"
$requiredNoProxy = @("127.0.0.1", "localhost")
if ($Networked -and -not [string]::IsNullOrWhiteSpace($ChainSyncUrls)) {
    foreach ($syncUrl in @($ChainSyncUrls -split ',')) {
        try {
            $syncHost = ([Uri]$syncUrl.Trim()).Host
            if (-not [string]::IsNullOrWhiteSpace($syncHost)) {
                $requiredNoProxy += $syncHost
            }
        } catch {
            Write-LauncherLog "ignored invalid chain sync URL while building NO_PROXY: $syncUrl"
        }
    }
}
$existingNoProxy = @($env:NO_PROXY -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
$env:NO_PROXY = (@($existingNoProxy + $requiredNoProxy) | Sort-Object -Unique) -join ','
$env:no_proxy = $env:NO_PROXY
$env:CONFIG_FILE = $ConfigPath
$env:QSDM_TASK_REGISTRY_PATH = $TaskRegistryPath
$env:QSDM_TASK_ACTION_LOG_PATH = $TaskActionLogPath
$env:QSDM_LOG_STDOUT = "0"
$env:QSDM_NETWORK_HOST_KEY_PATH = $NetworkHostKeyPath
# Retired direct-credit controls are scrubbed unconditionally. Production
# rewards must come from separately funded signer wallets.
Remove-Item Env:QSDM_REFERRAL_REWARD_POOL_SEED_CELL -ErrorAction SilentlyContinue
Remove-Item Env:QSDM_REFERRAL_REWARD_POOL_ALLOW_LOCAL_SEED -ErrorAction SilentlyContinue
Remove-Item Env:QSDM_PREFUND_ACCOUNTS -ErrorAction SilentlyContinue
Remove-Item Env:QSDM_ALLOW_DEVELOPMENT_PREFUND -ErrorAction SilentlyContinue
Remove-Item Env:QSDM_GENESIS_PREFUND_ADDR -ErrorAction SilentlyContinue
Remove-Item Env:QSDM_GENESIS_PREFUND_AMOUNT_CELL -ErrorAction SilentlyContinue
if ($Networked) {
    $resolvedBootstrapPeers = $BootstrapPeers
    if ([string]::IsNullOrWhiteSpace($resolvedBootstrapPeers)) {
        $resolvedBootstrapPeers = $DefaultBootstrapPeer
    }
    $env:BOOTSTRAP_PEERS = $resolvedBootstrapPeers
    $env:NETWORK_BIND_ADDRESS = if ($PublicP2P) { "0.0.0.0" } else { "127.0.0.1" }
    if (-not [string]::IsNullOrWhiteSpace($ChainSyncUrls)) {
        $env:QSDM_CHAIN_SYNC_URLS = $ChainSyncUrls
    } else {
        Remove-Item Env:QSDM_CHAIN_SYNC_URLS -ErrorAction SilentlyContinue
    }
    Remove-Item Env:QSDM_PREFUND_ACCOUNTS -ErrorAction SilentlyContinue
    Remove-Item Env:QSDM_GENESIS_PREFUND_ADDR -ErrorAction SilentlyContinue
    Remove-Item Env:QSDM_GENESIS_PREFUND_AMOUNT_CELL -ErrorAction SilentlyContinue
    Write-LauncherLog "networked validator mode enabled run_dir=$RunDir bootstrap_peers=$resolvedBootstrapPeers chain_sync_urls=$ChainSyncUrls public_p2p=$($PublicP2P.IsPresent)"
} else {
    Remove-Item Env:QSDM_CHAIN_SYNC_URLS -ErrorAction SilentlyContinue
    if ($env:QSDM_LOCAL_CELL_FAUCET -eq "1") {
        if (-not (Test-Path -LiteralPath $FaucetTokenPath)) {
            $faucetToken = "$(([guid]::NewGuid()).ToString("N"))$(([guid]::NewGuid()).ToString("N"))"
            Set-Content -LiteralPath $FaucetTokenPath -Value $faucetToken -NoNewline -Encoding UTF8
            Write-LauncherLog "created starter-grant access token at $FaucetTokenPath"
        }
        if ([string]::IsNullOrWhiteSpace($env:QSDM_LOCAL_CELL_FAUCET_TOKEN)) {
            $env:QSDM_LOCAL_CELL_FAUCET_TOKEN = (Get-Content -LiteralPath $FaucetTokenPath -Raw).Trim()
        }
        if ([string]::IsNullOrWhiteSpace($env:QSDM_LOCAL_CELL_FAUCET_TARGET_BALANCE)) {
            $env:QSDM_LOCAL_CELL_FAUCET_TARGET_BALANCE = "1"
        }
        if ([string]::IsNullOrWhiteSpace($env:QSDM_LOCAL_CELL_FAUCET_MAX_GRANT)) {
            $env:QSDM_LOCAL_CELL_FAUCET_MAX_GRANT = "1"
        }
    }
    if ($env:QSDM_REFERRAL_REWARD_POOL_ENABLED -eq "1" -and [string]::IsNullOrWhiteSpace($env:QSDM_REFERRAL_LEDGER_PATH)) {
        $env:QSDM_REFERRAL_LEDGER_PATH = $ReferralLedgerPath
    }
    if (Test-Path -LiteralPath $PrefundAccountsPath) {
        Write-LauncherLog "ignored retired development prefund file at $PrefundAccountsPath"
    }
}
Write-LauncherLog "task registry path=$TaskRegistryPath task action log path=$TaskActionLogPath network_host_key=$NetworkHostKeyPath run_dir=$RunDir networked=$($Networked.IsPresent) health_wait_seconds=$HealthWaitSeconds"
Write-LauncherLog "selected validator binary=$ExePath"
Reset-OversizedDuplicateLog -Path $StdoutLog

$process = Start-Process `
    -FilePath $ExePath `
    -WorkingDirectory $RunDir `
    -WindowStyle Hidden `
    -RedirectStandardOutput $StdoutLog `
    -RedirectStandardError $StderrLog `
    -PassThru

Set-Content -LiteralPath $PidFile -Value $process.Id
Write-LauncherLog "started validator pid=$($process.Id)"

$boundedHealthWait = [Math]::Max(3, [Math]::Min($HealthWaitSeconds, 30))
$healthDeadline = (Get-Date).AddSeconds($boundedHealthWait)
while ((Get-Date) -lt $healthDeadline) {
    Start-Sleep -Milliseconds 500
    if ($process.HasExited) {
        Write-LauncherLog "validator exited before readiness pid=$($process.Id) exit_code=$($process.ExitCode)"
        Release-LauncherLock
        exit 1
    }
    if (Test-QsdmReady) {
        Write-LauncherLog "validator ready at $HealthUrl"
        Ensure-TreasurySigners
        Release-LauncherLock
        exit 0
    }
}

Write-LauncherLog "validator did not become ready within $boundedHealthWait seconds"
Release-LauncherLock
exit 1
