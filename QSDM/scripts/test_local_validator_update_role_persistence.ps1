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

$updater = Join-Path $QsdmRoot "scripts\update_local_validator.ps1"
$testRoot = Join-Path ([IO.Path]::GetTempPath()) "qsdm-update-role-$([guid]::NewGuid().ToString('N'))"

function Assert-True {
    param(
        [Parameter(Mandatory)][bool]$Value,
        [Parameter(Mandatory)][string]$Message
    )
    if (-not $Value) {
        throw $Message
    }
}

function Import-UpdaterFunction {
    param([Parameter(Mandatory)][string]$Name)

    $tokens = $null
    $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile(
        (Resolve-Path -LiteralPath $updater),
        [ref]$tokens,
        [ref]$errors
    )
    if ($errors.Count -gt 0) {
        throw "Updater has parser errors: $($errors.Message -join ' | ')"
    }
    $definition = $ast.Find({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
            $node.Name -eq $Name
    }, $true)
    if ($null -eq $definition) {
        throw "Updater function $Name was not found."
    }
    return $definition.Body.GetScriptBlock()
}

try {
    New-Item -ItemType Directory -Force -Path $testRoot | Out-Null
    $script:ModeConfigPath = Join-Path $testRoot "validator-mode.json"

    $getValidatorMode = Import-UpdaterFunction -Name "Get-ValidatorMode"
    $getLauncherArguments = Import-UpdaterFunction -Name "Get-LauncherArguments"

    $modeFixture = [ordered]@{
        mode = "networked"
        chainSyncUrls = "https://api.qsdm.tech/api/v1"
        bootstrapPeers = "/dns4/api.qsdm.tech/tcp/4001/p2p/test"
        publicP2P = $false
        blockProducer = $true
    }
    [IO.File]::WriteAllText(
        $script:ModeConfigPath,
        ($modeFixture | ConvertTo-Json),
        [Text.UTF8Encoding]::new($false)
    )

    $script:Mode = & $getValidatorMode
    Assert-True -Value ([bool]$script:Mode.blockProducer) `
        -Message "Updater did not restore blockProducer from validator-mode.json."

    $script:ValidatorLauncher = Join-Path $QsdmRoot "scripts\start_local_validator.ps1"
    $script:HealthUrl = "http://127.0.0.1:8080/api/v1/health/ready"
    $script:HealthWaitSeconds = 120
    $launcherArgs = @(& $getLauncherArguments)
    Assert-True -Value ($launcherArgs -contains "-Networked") `
        -Message "Updater did not retain networked mode."
    Assert-True -Value ($launcherArgs -contains "-BlockProducer") `
        -Message "Updater did not forward the network producer role to the validator launcher."

    $modeFixture.Remove("blockProducer")
    [IO.File]::WriteAllText(
        $script:ModeConfigPath,
        ($modeFixture | ConvertTo-Json),
        [Text.UTF8Encoding]::new($false)
    )
    $script:Mode = & $getValidatorMode
    $legacyArgs = @(& $getLauncherArguments)
    Assert-True -Value (-not [bool]$script:Mode.blockProducer) `
        -Message "Older mode files must default to follower behavior."
    Assert-True -Value ($legacyArgs -notcontains "-BlockProducer") `
        -Message "Updater enabled network production without an explicit persisted role."

    [pscustomobject]@{
        schema = "qsdm.local-validator-update-role-test.v1"
        success = $true
        producer_role_restored = $true
        producer_argument_forwarded = $true
        legacy_mode_defaults_to_follower = $true
    } | ConvertTo-Json -Compress
} finally {
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
