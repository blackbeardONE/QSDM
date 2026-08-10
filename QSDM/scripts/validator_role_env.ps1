function Resolve-QsdmValidatorRoleEnvironment {
    [CmdletBinding()]
    param(
        [switch]$Networked,
        [switch]$BlockProducer
    )

    if ($BlockProducer -and -not $Networked) {
        throw "-BlockProducer requires -Networked. Solo mode already owns local block production."
    }

    return [ordered]@{
        QSDM_SOLO_VALIDATOR_MODE      = if ($Networked) { "0" } else { "1" }
        QSDM_NETWORK_BLOCK_PRODUCER   = if ($Networked -and $BlockProducer) { "1" } else { "0" }
        QSDM_NETWORKED_CATCHUP_MODE   = if ($Networked -and -not $BlockProducer) { "1" } else { "0" }
    }
}
