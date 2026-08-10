$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "validator_role_env.ps1")

function Assert-Role {
    param(
        [Parameter(Mandatory)][string]$Name,
        [switch]$Networked,
        [switch]$BlockProducer,
        [Parameter(Mandatory)][string]$Solo,
        [Parameter(Mandatory)][string]$Producer,
        [Parameter(Mandatory)][string]$Catchup
    )

    $actual = Resolve-QsdmValidatorRoleEnvironment `
        -Networked:$Networked `
        -BlockProducer:$BlockProducer
    foreach ($entry in @{
        QSDM_SOLO_VALIDATOR_MODE = $Solo
        QSDM_NETWORK_BLOCK_PRODUCER = $Producer
        QSDM_NETWORKED_CATCHUP_MODE = $Catchup
    }.GetEnumerator()) {
        if ($actual[$entry.Key] -ne $entry.Value) {
            throw "$Name expected $($entry.Key)=$($entry.Value), got $($actual[$entry.Key])"
        }
    }
}

Assert-Role -Name "solo" -Solo "1" -Producer "0" -Catchup "0"
Assert-Role -Name "network follower" -Networked -Solo "0" -Producer "0" -Catchup "1"
Assert-Role -Name "network producer" -Networked -BlockProducer -Solo "0" -Producer "1" -Catchup "0"

$rejected = $false
try {
    Resolve-QsdmValidatorRoleEnvironment -BlockProducer | Out-Null
} catch {
    $rejected = $true
}
if (-not $rejected) {
    throw "BlockProducer without Networked must be rejected"
}

Write-Output "validator role environment tests passed"
