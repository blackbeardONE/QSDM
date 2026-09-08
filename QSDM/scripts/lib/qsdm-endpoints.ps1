# Shared public endpoint defaults for local operator scripts.
#
# Keep qsdm.tech as the live product domain, but do not bake one VPS into
# every launcher. A new VPS migration should update config/public-endpoints.json
# or set the matching QSDM_* environment variables, then restart the scripts.

$script:QsdmEndpointDefaults = [ordered]@{
    public_site = "https://qsdm.tech"
    public_api_base = "https://api.qsdm.tech"
    core_api_base = "https://api.qsdm.tech/api/v1"
    home_gateway_relay = "https://api.qsdm.tech"
    home_gateway_slot = "home-validator"
    reference_bootstrap_peer = "/dns4/api.qsdm.tech/tcp/4001/p2p/12D3KooWRH4MGiaRYMZEr9LvdxYrpePT5LPbNqLTMGukD32yhkZ8"
    vps_ssh_target = "root@node.qsdm.tech"
}

function Get-QsdmEndpointConfigPath {
    if (-not [string]::IsNullOrWhiteSpace($env:QSDM_ENDPOINTS_FILE)) {
        return $env:QSDM_ENDPOINTS_FILE
    }
    $scriptsDir = Split-Path -Parent $PSScriptRoot
    $root = Split-Path -Parent $scriptsDir
    return (Join-Path $root "config\public-endpoints.json")
}

function Read-QsdmEndpointConfig {
    $path = Get-QsdmEndpointConfigPath
    if ([string]::IsNullOrWhiteSpace($path) -or -not (Test-Path -LiteralPath $path -PathType Leaf)) {
        return $null
    }
    try {
        return Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
    } catch {
        throw "Invalid QSDM endpoint config at ${path}: $($_.Exception.Message)"
    }
}

function Get-QsdmEndpointValue {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [string]$EnvVar = ""
    )

    if (-not [string]::IsNullOrWhiteSpace($EnvVar)) {
        $envValue = [Environment]::GetEnvironmentVariable($EnvVar)
        if (-not [string]::IsNullOrWhiteSpace($envValue)) {
            return $envValue.Trim()
        }
    }

    $config = Read-QsdmEndpointConfig
    if ($null -ne $config) {
        $property = $config.PSObject.Properties[$Name]
        if ($null -ne $property -and -not [string]::IsNullOrWhiteSpace([string]$property.Value)) {
            return ([string]$property.Value).Trim()
        }
    }

    if ($script:QsdmEndpointDefaults.Contains($Name)) {
        return [string]$script:QsdmEndpointDefaults[$Name]
    }
    throw "Unknown QSDM endpoint default: $Name"
}

function Resolve-QsdmEndpointValue {
    param(
        [string]$Value,
        [Parameter(Mandatory = $true)][string]$Name,
        [string]$EnvVar = ""
    )

    if (-not [string]::IsNullOrWhiteSpace($Value)) {
        return $Value.Trim()
    }
    return Get-QsdmEndpointValue -Name $Name -EnvVar $EnvVar
}

function Get-QsdmEndpointHost {
    param([string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value)) {
        return ""
    }
    try {
        return ([Uri]$Value).Host
    } catch {
        return ""
    }
}

function Get-QsdmNoProxyList {
    param([string[]]$EndpointValues = @())

    $hosts = @("127.0.0.1", "localhost")
    foreach ($value in $EndpointValues) {
        $hostName = Get-QsdmEndpointHost -Value $value
        if (-not [string]::IsNullOrWhiteSpace($hostName)) {
            $hosts += $hostName
        }
    }
    return (($hosts | Select-Object -Unique) -join ',')
}