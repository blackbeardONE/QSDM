[CmdletBinding()]
param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$OutputDirectory = (Join-Path $HOME ".qsdm\ecosystem-wallets")
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function New-RandomPassphrase {
    param([int]$ByteCount = 48)

    $bytes = [byte[]]::new($ByteCount)
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($bytes)
        return [Convert]::ToBase64String($bytes)
    } finally {
        [Array]::Clear($bytes, 0, $bytes.Length)
        $rng.Dispose()
    }
}

function Set-RestrictedDirectoryAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ($env:OS -ne "Windows_NT") {
        & chmod 700 $Path
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to restrict directory permissions on $Path"
        }
        return
    }

    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $inheritance = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $propagation = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow
    $fullControl = [Security.AccessControl.FileSystemRights]::FullControl
    $identities = @(
        [Security.Principal.WindowsIdentity]::GetCurrent().User,
        [Security.Principal.SecurityIdentifier]::new("S-1-5-18"),
        [Security.Principal.SecurityIdentifier]::new("S-1-5-32-544")
    )
    foreach ($identity in $identities) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            $identity,
            $fullControl,
            $inheritance,
            $propagation,
            $allow
        )
        [void]$acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Set-RestrictedFileAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ($env:OS -ne "Windows_NT") {
        & chmod 600 $Path
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to restrict file permissions on $Path"
        }
        return
    }

    $acl = [Security.AccessControl.FileSecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $allow = [Security.AccessControl.AccessControlType]::Allow
    $fullControl = [Security.AccessControl.FileSystemRights]::FullControl
    $identities = @(
        [Security.Principal.WindowsIdentity]::GetCurrent().User,
        [Security.Principal.SecurityIdentifier]::new("S-1-5-18"),
        [Security.Principal.SecurityIdentifier]::new("S-1-5-32-544")
    )
    foreach ($identity in $identities) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            $identity,
            $fullControl,
            $allow
        )
        [void]$acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Write-Utf8SecretFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Value
    )

    [IO.File]::WriteAllText($Path, $Value, [Text.UTF8Encoding]::new($false))
    Set-RestrictedFileAcl -Path $Path
}

$qsdmCliCandidates = if ($env:OS -eq "Windows_NT") {
    @(
        (Join-Path $QsdmRoot "source\qsdmcli.exe"),
        (Join-Path $QsdmRoot "source\qsdmcli")
    )
} else {
    @(
        (Join-Path $QsdmRoot "source/qsdmcli"),
        (Join-Path $QsdmRoot "source/qsdmcli.exe")
    )
}
$qsdmCli = $qsdmCliCandidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
if (-not $qsdmCli) {
    throw "Missing qsdmcli binary under $QsdmRoot/source. Build qsdmcli before creating ecosystem wallets."
}

$roles = @(
    [ordered]@{
        id = "protocol-reserve-vault"
        purpose = "Provisional cold protocol reserve; not a genesis allocation or multisig until Core implements and audits those controls."
        custody = "offline-manual-multi-person-approval"
    },
    [ordered]@{
        id = "operations-treasury"
        purpose = "Governance-approved operating budgets and bounded Tier 2 refills."
        custody = "offline-or-warm-manual-multi-person-approval"
    },
    [ordered]@{
        id = "referral-payout"
        purpose = "Qualified referral payouts only."
        custody = "isolated-loopback-signer"
    },
    [ordered]@{
        id = "onboarding-payout"
        purpose = "One-time eligible starter CELL grants only."
        custody = "separate-isolated-loopback-signer"
    },
    [ordered]@{
        id = "skyfang-integration"
        purpose = "Verified Sky Fang integration and task-pool funding only."
        custody = "isolated-integration-signer"
    },
    [ordered]@{
        id = "edge-task-funding"
        purpose = "Funds separate CPU, GPU, and RAM reward pools; it does not pay protocol mining emission."
        custody = "isolated-task-budget-signer"
    },
    [ordered]@{
        id = "pooled-compute-ecosystem-reserve"
        purpose = "Receives the 15 percent ecosystem share from settled Agent and Relay workloads."
        custody = "public-reserve-manual-governance"
    }
)

$resolvedOutput = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $resolvedOutput) {
    $existing = @(Get-ChildItem -LiteralPath $resolvedOutput -Force -ErrorAction Stop)
    if ($existing.Count -gt 0) {
        throw "Refusing to write into non-empty wallet directory: $resolvedOutput"
    }
} else {
    [void](New-Item -ItemType Directory -Path $resolvedOutput)
}
Set-RestrictedDirectoryAcl -Path $resolvedOutput

$createdAt = (Get-Date).ToUniversalTime().ToString("o")
$addresses = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
$publicWallets = [Collections.Generic.List[object]]::new()
$privateWallets = [Collections.Generic.List[object]]::new()

foreach ($role in $roles) {
    $roleDirectory = Join-Path $resolvedOutput $role.id
    [void](New-Item -ItemType Directory -Path $roleDirectory)
    Set-RestrictedDirectoryAcl -Path $roleDirectory

    $walletPath = Join-Path $roleDirectory "wallet.json"
    $passphrasePath = Join-Path $roleDirectory "wallet.passphrase"
    $passphrase = New-RandomPassphrase
    try {
        Write-Utf8SecretFile -Path $passphrasePath -Value $passphrase

        $cliOutput = @($passphrase | & $qsdmCli wallet new --out $walletPath --passphrase-file - 2>&1)
        if ($LASTEXITCODE -ne 0) {
            throw "qsdmcli failed while creating $($role.id): $($cliOutput -join ' ')"
        }
        Set-RestrictedFileAcl -Path $walletPath

        $address = $cliOutput |
            ForEach-Object { ([string]$_).Trim() } |
            Where-Object { $_ -match '^[0-9a-fA-F]{64}$' } |
            Select-Object -Last 1
        if (-not $address) {
            throw "qsdmcli did not return a valid address for $($role.id)"
        }
        $address = $address.ToLowerInvariant()
        if (-not $addresses.Add($address)) {
            throw "Duplicate wallet address generated for $($role.id): $address"
        }

        & $qsdmCli wallet inspect --in $walletPath --passphrase-file $passphrasePath | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "Keystore integrity verification failed for $($role.id)"
        }
        $walletInfo = (& $qsdmCli wallet show --json --in $walletPath) | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or ([string]$walletInfo.address).ToLowerInvariant() -ne $address) {
            throw "Keystore address verification failed for $($role.id)"
        }

        $publicWallets.Add([ordered]@{
            role = $role.id
            address = $address
            purpose = $role.purpose
            custody = $role.custody
            activation_status = "pending-backup-approval-funding-and-readiness"
        })
        $privateWallets.Add([ordered]@{
            role = $role.id
            address = $address
            keystore_file = "$($role.id)/wallet.json"
            passphrase_file = "$($role.id)/wallet.passphrase"
            keystore_sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $walletPath).Hash.ToLowerInvariant()
        })
    } finally {
        $passphrase = $null
    }
}

$publicRegistry = [ordered]@{
    schema_version = 1
    status = "pending-custody-and-rollout-approval"
    network = "qsdm-canonical-production-pilot"
    created_at = $createdAt
    wallets = $publicWallets
    consensus_accounts = @(
        [ordered]@{
            id = "edge-cpu-task-reward-pool"
            kind = "task-reward-pool"
            funding_wallet_role = "edge-task-funding"
            status = "must-be-funded-on-chain-before-rewards"
        },
        [ordered]@{
            id = "edge-gpu-task-reward-pool"
            kind = "task-reward-pool"
            funding_wallet_role = "edge-task-funding"
            status = "must-be-funded-on-chain-before-rewards"
        },
        [ordered]@{
            id = "edge-ram-task-reward-pool"
            kind = "task-reward-pool"
            funding_wallet_role = "edge-task-funding"
            status = "must-be-funded-on-chain-before-rewards"
        },
        [ordered]@{
            id = "mother-hive-workload-escrow"
            kind = "consensus-controlled-per-workload-escrow"
            status = "not-implemented-do-not-substitute-a-hot-wallet"
        }
    )
}

$privateInventory = [ordered]@{
    schema_version = 1
    warning = "PRIVATE INVENTORY. It contains file locations but no passphrase values. Keep it with the encrypted wallet set."
    created_at = $createdAt
    wallets = $privateWallets
}

$publicRegistryPath = Join-Path $resolvedOutput "ecosystem-wallets.public.pending.json"
$privateInventoryPath = Join-Path $resolvedOutput "ecosystem-wallets.private.inventory.json"
$readmePath = Join-Path $resolvedOutput "README-SECURITY.txt"
Write-Utf8SecretFile -Path $publicRegistryPath -Value ($publicRegistry | ConvertTo-Json -Depth 8)
Write-Utf8SecretFile -Path $privateInventoryPath -Value ($privateInventory | ConvertTo-Json -Depth 6)
Write-Utf8SecretFile -Path $readmePath -Value @"
QSDM ECOSYSTEM WALLET SET - PENDING ACTIVATION

These wallets have zero CELL until funded by ordinary signed transfers.
No wallet in this directory is active merely because it exists.

Before funding or deployment:
1. Make two encrypted offline backups and verify every keystore with its matching passphrase.
2. Separate cold-wallet keystores from their passphrase backups.
3. Assign named human custodians and record approval policy.
4. Publish only ecosystem-wallets.public.pending.json after custody approval.
5. Run QSDM/scripts/test_treasury_readiness.ps1 against the canonical network.
6. Keep referral, onboarding, integration, task, and reserve wallets separate.
7. Do not treat protocol-reserve-vault as a genesis allocation or multisig.
8. Do not enable Mother Hive settlement until consensus escrow and wallet-bound Relay receipts exist.

Never commit this directory, keystores, passphrases, or signer tokens to source control.
"@

Write-Host "Created and verified $($publicWallets.Count) QSDM ecosystem wallets."
Write-Host "Secure directory: $resolvedOutput"
Write-Host "Public pending registry: $publicRegistryPath"
foreach ($wallet in $publicWallets) {
    Write-Host "  $($wallet.role): $($wallet.address)"
}
Write-Host "No CELL was transferred and no payout service was enabled."
