[CmdletBinding()]
param(
    [string]$BaseUrl = "http://127.0.0.1:8080",
    [ValidateRange(1, 120)]
    [int]$TimeoutSec = 10
)

$check = Join-Path $PSScriptRoot "check_validator_status.ps1"
& $check -BaseUrl $BaseUrl -TimeoutSec $TimeoutSec
exit $LASTEXITCODE