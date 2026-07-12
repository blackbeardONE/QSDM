param(
    [string]$EnvFile,
    [string]$TaskName = 'QSDM-NGC-Attest',
    [ValidateRange(1, 60)]
    [int]$IntervalMinutes = 10,
    [string]$LogPath = (Join-Path $env:LOCALAPPDATA 'QSDM\ngc-attest.log'),
    [switch]$StartNow,
    [switch]$ProtectCredentialOnly
)

$ErrorActionPreference = 'Stop'

$appRoot = Split-Path $PSScriptRoot -Parent
$runner = Join-Path $PSScriptRoot 'attest-from-env-file.ps1'
if (-not $EnvFile) {
    $EnvFile = Join-Path $appRoot 'ngc.local.env'
}

$runner = (Resolve-Path -LiteralPath $runner).Path
$EnvFile = (Resolve-Path -LiteralPath $EnvFile).Path

function Protect-CredentialFile {
    param([Parameter(Mandatory = $true)][string]$Path)

    $acl = Get-Acl -LiteralPath $Path
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($existingRule in @($acl.Access)) {
        $null = $acl.RemoveAccessRuleSpecific($existingRule)
    }

    $identities = @(
        [Security.Principal.WindowsIdentity]::GetCurrent().User
        [Security.Principal.SecurityIdentifier]::new(
            [Security.Principal.WellKnownSidType]::LocalSystemSid,
            $null
        )
        [Security.Principal.SecurityIdentifier]::new(
            [Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid,
            $null
        )
    )
    foreach ($identity in $identities) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            $identity,
            [Security.AccessControl.FileSystemRights]::FullControl,
            [Security.AccessControl.AccessControlType]::Allow
        )
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

Protect-CredentialFile -Path $EnvFile

if ($ProtectCredentialOnly) {
    [pscustomobject]@{
        EnvFile = $EnvFile
        CredentialFileAclProtected = (Get-Acl -LiteralPath $EnvFile).AreAccessRulesProtected
    }
    return
}

if ([string]::IsNullOrWhiteSpace($LogPath)) {
    throw 'LogPath must not be empty.'
}
$logDirectory = Split-Path -Parent $LogPath
New-Item -ItemType Directory -Force -Path $logDirectory | Out-Null

$argumentList = @(
    '-NoProfile'
    '-ExecutionPolicy Bypass'
    '-WindowStyle Hidden'
    "-File `"$runner`""
    "-EnvFile `"$EnvFile`""
    '-Quiet'
    "-LogPath `"$LogPath`""
    '-LogMaxBytes 10485760'
    '-LogKeep 3'
) -join ' '

$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $argumentList
$trigger = New-ScheduledTaskTrigger `
    -Once `
    -At (Get-Date).AddMinutes(1) `
    -RepetitionInterval (New-TimeSpan -Minutes $IntervalMinutes) `
    -RepetitionDuration (New-TimeSpan -Days 3650)
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -MultipleInstances IgnoreNew `
    -ExecutionTimeLimit (New-TimeSpan -Minutes 5)
$principal = New-ScheduledTaskPrincipal `
    -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) `
    -LogonType Interactive `
    -RunLevel Limited

Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $action `
    -Trigger $trigger `
    -Settings $settings `
    -Principal $principal `
    -Force | Out-Null

if ($StartNow) {
    Start-ScheduledTask -TaskName $TaskName
}

$registered = Get-ScheduledTask -TaskName $TaskName
[pscustomobject]@{
    TaskName = $registered.TaskName
    State = $registered.State
    Runner = $runner
    EnvFile = $EnvFile
    IntervalMinutes = $IntervalMinutes
    Started = [bool]$StartNow
    CredentialFileAclProtected = (Get-Acl -LiteralPath $EnvFile).AreAccessRulesProtected
}
