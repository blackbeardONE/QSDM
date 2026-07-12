# PowerShell companion to govulncheck-filter.sh.
#
# Runs `govulncheck -json ./...` from the current directory, then fails
# only when an imported-package or reachable-symbol finding is not in the
# tracked allowlist.
[CmdletBinding()]
param(
	[string]$GoExe = ''
)

$ErrorActionPreference = 'Stop'
$GovulncheckVersion = 'v1.6.0'

# Intentionally empty. GO-2024-3218 was removed from the reachable graph by
# replacing Kad-DHT discovery with explicit bootstrap-peer dialing.
$Allowlist = @()

if (-not $GoExe) {
	$candidates = @(
		"${env:ProgramFiles}\Go\bin\go.exe",
		"${env:ProgramFiles(x86)}\Go\bin\go.exe"
	)
	foreach ($candidate in $candidates) {
		if (Test-Path $candidate) {
			$GoExe = $candidate
			break
		}
	}
	if (-not $GoExe) {
		$goCommand = Get-Command go -ErrorAction SilentlyContinue
		if ($goCommand) {
			$GoExe = $goCommand.Source
		}
	}
}
if (-not $GoExe) {
	throw 'go.exe not found; install Go or add it to PATH.'
}

$gorootCandidate = Split-Path (Split-Path $GoExe -Parent) -Parent
if (Test-Path (Join-Path $gorootCandidate 'src\internal')) {
	$env:GOROOT = $gorootCandidate
}

$outFile = Join-Path ([System.IO.Path]::GetTempPath()) "qsdm-govulncheck-$([guid]::NewGuid()).jsonl"
$errFile = Join-Path ([System.IO.Path]::GetTempPath()) "qsdm-govulncheck-$([guid]::NewGuid()).stderr"

try {
	$env:CGO_ENABLED = '0'
	Remove-Item Env:CGO_CFLAGS -ErrorAction SilentlyContinue
	Remove-Item Env:CGO_LDFLAGS -ErrorAction SilentlyContinue
	$env:QSDM_METRICS_REGISTER_STRICT = '1'

	& $GoExe run "golang.org/x/vuln/cmd/govulncheck@$GovulncheckVersion" -json ./... > $outFile 2> $errFile
	$rc = $LASTEXITCODE

	if ((Test-Path $errFile) -and (Get-Item $errFile).Length -gt 0) {
		[Console]::Error.WriteLine('==== govulncheck stderr ====')
		[Console]::Error.WriteLine([System.IO.File]::ReadAllText($errFile))
	}

	if ($rc -ne 0 -and $rc -ne 3) {
		throw "govulncheck failed with exit code $rc (not a vulnerability report)"
	}

	$content = [System.IO.File]::ReadAllText($outFile)
	$reported = New-Object 'System.Collections.Generic.HashSet[string]'
	# Newer govulncheck versions emit a one-frame module notice for every
	# affected module version. Such a frame has module/version but no package.
	# Capture each finding's trace and count it only when a package path proves
	# that affected code is imported (symbol findings include package too).
	$findingPattern = '(?s)"finding"\s*:\s*\{\s*"osv"\s*:\s*"(GO-[0-9]{4}-[0-9]+)"(?:(?!"finding"\s*:).)*?"trace"\s*:\s*\[(?<trace>.*?)\]\s*\}'
	foreach ($match in [regex]::Matches($content, $findingPattern, [System.Text.RegularExpressions.RegexOptions]::Singleline)) {
		if ($match.Groups['trace'].Value -match '"package"\s*:\s*"[^"\s]+"') {
			[void]$reported.Add($match.Groups[1].Value)
		}
	}

	Write-Host '==== govulncheck affected package/symbol findings ===='
	foreach ($id in ($reported | Sort-Object)) {
		Write-Host $id
	}
	Write-Host '========================================'

	if ($reported.Count -eq 0) {
		Write-Host 'govulncheck: no vulnerabilities reported. CLEAN.'
		exit 0
	}

	$unexpected = @($reported | Where-Object { $Allowlist -notcontains $_ } | Sort-Object)
	if ($unexpected.Count -gt 0) {
		[Console]::Error.WriteLine("govulncheck: UNEXPECTED vulnerabilities (not in allowlist): $($unexpected -join ', ')")
		exit 1
	}

	Write-Host 'govulncheck: all reported vulnerabilities are allowlisted; accepting.'
	exit 0
} finally {
	Remove-Item -LiteralPath $outFile -Force -ErrorAction SilentlyContinue
	Remove-Item -LiteralPath $errFile -Force -ErrorAction SilentlyContinue
}
