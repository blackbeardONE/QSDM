param()

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$signerLauncher = Join-Path $scriptRoot "start_treasury_signer.ps1"
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("qsdm-signer-pid-test-" + [guid]::NewGuid().ToString("N"))
$sourceRoot = Join-Path $testRoot "source"
$roleRoot = Join-Path $sourceRoot ".cache\treasury\faucet"
$unrelated = $null
$startedSignerPid = 0

try {
    New-Item -ItemType Directory -Force -Path $sourceRoot, $roleRoot | Out-Null
    Copy-Item -LiteralPath "$env:SystemRoot\System32\cmd.exe" -Destination (Join-Path $sourceRoot "qsdm-treasury-signer.exe")

    $keystore = Join-Path $testRoot "wallet.json"
    $passphrase = Join-Path $testRoot "passphrase.txt"
    $token = Join-Path $testRoot "token.txt"
    Set-Content -LiteralPath $keystore -Value "{}" -NoNewline -Encoding ASCII
    Set-Content -LiteralPath $passphrase -Value "test-only" -NoNewline -Encoding ASCII
    Set-Content -LiteralPath $token -Value ("a" * 64) -NoNewline -Encoding ASCII

    $unrelated = Start-Process `
        -FilePath "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" `
        -ArgumentList @("-NoProfile", "-Command", "Start-Sleep -Seconds 30") `
        -WindowStyle Hidden `
        -PassThru
    $pidFile = Join-Path $roleRoot "signer.pid"
    Set-Content -LiteralPath $pidFile -Value $unrelated.Id -NoNewline -Encoding ASCII
    (Get-Item -LiteralPath $pidFile).LastWriteTimeUtc = [DateTime]::UtcNow.AddMinutes(-5)

    $threw = $false
    try {
        & $signerLauncher `
            -QsdmRoot $testRoot `
            -Role faucet `
            -KeystorePath $keystore `
            -PassphraseFile $passphrase `
            -TokenFile $token `
            -ApiUrl "http://127.0.0.1:1" `
            -Port 39898 `
            -HealthWaitSeconds 1 `
            -Restart
    } catch {
        $threw = $true
    }
    if (-not $threw) {
        throw "The invalid test signer unexpectedly became healthy."
    }
    if (-not (Get-Process -Id $unrelated.Id -ErrorAction SilentlyContinue)) {
        throw "The stale signer PID caused an unrelated process to be stopped."
    }
    if (Test-Path -LiteralPath $pidFile) {
        $startedSignerPid = [int](Get-Content -LiteralPath $pidFile -Raw).Trim()
        throw "Signer PID state was not cleared after the failed replacement process ($startedSignerPid)."
    }
    if (Test-Path -LiteralPath (Join-Path $roleRoot "signer.process.json")) {
        throw "Signer identity state was not cleared after failed startup."
    }

    Write-Host "Treasury signer stale-PID regression passed."
} finally {
    if ($startedSignerPid -gt 0) {
        Stop-Process -Id $startedSignerPid -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $unrelated) {
        Stop-Process -Id $unrelated.Id -Force -ErrorAction SilentlyContinue
    }
    if (Test-Path -LiteralPath $testRoot) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
