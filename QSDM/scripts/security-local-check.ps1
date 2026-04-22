# Lightweight local checks (go mod verify + optional govulncheck). Run from QSDM/source.
$ErrorActionPreference = 'Stop'
$SourceDir = Resolve-Path (Join-Path $PSScriptRoot '..\source')
Push-Location $SourceDir
try {
	Write-Host '==> go mod verify'
	& go mod verify
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
		$env:QSDMPLUS_METRICS_REGISTER_STRICT = '1'
		& go run golang.org/x/vuln/cmd/govulncheck@latest ./...
		if ($LASTEXITCODE -ne 0) {
			exit $LASTEXITCODE
		}
	}
} finally {
	Pop-Location
}
Write-Host 'OK: security-local-check finished'
