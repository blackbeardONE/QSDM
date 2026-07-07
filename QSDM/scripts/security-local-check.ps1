# Lightweight local checks (go mod verify + optional govulncheck). Run from QSDM/source.
$ErrorActionPreference = 'Stop'
$SourceDir = Resolve-Path (Join-Path $PSScriptRoot '..\source')

$GoExe = $null
$goCandidates = @(
	"${env:ProgramFiles}\Go\bin\go.exe",
	"${env:ProgramFiles(x86)}\Go\bin\go.exe"
)
foreach ($candidate in $goCandidates) {
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
if (-not $GoExe) {
	throw 'go.exe not found; install Go or add it to PATH.'
}

$gorootCandidate = Split-Path (Split-Path $GoExe -Parent) -Parent
if (Test-Path (Join-Path $gorootCandidate 'src\internal')) {
	$env:GOROOT = $gorootCandidate
}

Push-Location $SourceDir
try {
	Write-Host '==> go mod verify'
	& $GoExe mod verify
	if ($LASTEXITCODE -ne 0) {
		throw "go mod verify failed ($LASTEXITCODE)"
	}
	if ($env:SKIP_GOVULNCHECK -eq '1') {
		Write-Host 'SKIP: govulncheck (SKIP_GOVULNCHECK=1)'
	} else {
		Write-Host '==> govulncheck (set SKIP_GOVULNCHECK=1 to skip)'
		$env:CGO_ENABLED = '0'
		Remove-Item Env:CGO_CFLAGS -ErrorAction SilentlyContinue
		Remove-Item Env:CGO_LDFLAGS -ErrorAction SilentlyContinue
		$env:QSDM_METRICS_REGISTER_STRICT = '1'
		& pwsh -NoProfile -File (Join-Path $PSScriptRoot 'govulncheck-filter.ps1') -GoExe $GoExe
		if ($LASTEXITCODE -ne 0) {
			exit $LASTEXITCODE
		}
	}
} finally {
	Pop-Location
}
Write-Host 'OK: security-local-check finished'
