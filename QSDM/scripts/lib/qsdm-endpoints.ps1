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

function Normalize-QsdmEndpointHost {
    param([string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value)) {
        return ""
    }
    return $Value.Trim().Trim('[', ']').TrimEnd('.').ToLowerInvariant()
}

function Get-QsdmBootstrapPeerHost {
    param([string]$Multiaddr)

    if ([string]::IsNullOrWhiteSpace($Multiaddr)) {
        return ""
    }

    $parts = @($Multiaddr.Trim().Split('/') | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    for ($index = 0; $index -lt ($parts.Count - 1); $index += 2) {
        $protocol = $parts[$index].ToLowerInvariant()
        if ($protocol -in @('dns', 'dns4', 'dns6', 'dnsaddr', 'ip4', 'ip6')) {
            return Normalize-QsdmEndpointHost -Value $parts[$index + 1]
        }
    }
    return ""
}

function Get-QsdmSshTargetHost {
    param([string]$Target)

    if ([string]::IsNullOrWhiteSpace($Target)) {
        return ""
    }

    $value = $Target.Trim()
    if ($value -match '^[A-Za-z][A-Za-z0-9+.-]*://') {
        return Normalize-QsdmEndpointHost -Value (Get-QsdmEndpointHost -Value $value)
    }

    $atIndex = $value.LastIndexOf('@')
    if ($atIndex -ge 0) {
        $value = $value.Substring($atIndex + 1)
    }
    if ($value.StartsWith('[')) {
        $closingIndex = $value.IndexOf(']')
        if ($closingIndex -gt 1) {
            return Normalize-QsdmEndpointHost -Value $value.Substring(1, $closingIndex - 1)
        }
    }
    if ($value -match '^(?<host>[^:]+):\d+$') {
        return Normalize-QsdmEndpointHost -Value $Matches.host
    }
    return Normalize-QsdmEndpointHost -Value $value
}

function Get-QsdmReferenceEndpointHosts {
    param(
        [string[]]$CoreApiBases = @(),
        [string[]]$Relays = @(),
        [string[]]$BootstrapPeers = @(),
        [string[]]$SshTargets = @()
    )

    $hosts = @()
    foreach ($value in $CoreApiBases + $Relays) {
        $hostName = Normalize-QsdmEndpointHost -Value (Get-QsdmEndpointHost -Value $value)
        if (-not [string]::IsNullOrWhiteSpace($hostName)) {
            $hosts += $hostName
        }
    }
    foreach ($value in $BootstrapPeers) {
        $hostName = Get-QsdmBootstrapPeerHost -Multiaddr $value
        if (-not [string]::IsNullOrWhiteSpace($hostName)) {
            $hosts += $hostName
        }
    }
    foreach ($value in $SshTargets) {
        $hostName = Get-QsdmSshTargetHost -Target $value
        if (-not [string]::IsNullOrWhiteSpace($hostName)) {
            $hosts += $hostName
        }
    }
    return @($hosts | Sort-Object -Unique)
}

function Test-QsdmNonReferenceEndpointHost {
    param(
        [string]$CandidateHost,
        [string[]]$ReferenceHosts = @()
    )

    $candidate = Normalize-QsdmEndpointHost -Value $CandidateHost
    if ([string]::IsNullOrWhiteSpace($candidate)) {
        return $false
    }
    $normalizedReferences = @($ReferenceHosts | ForEach-Object {
        Normalize-QsdmEndpointHost -Value $_
    } | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    return ($normalizedReferences -notcontains $candidate)
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