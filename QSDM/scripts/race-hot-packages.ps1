# go test -race on hot packages. Race detector requires CGO + a C toolchain. Run from monorepo root.
$ErrorActionPreference = 'Stop'
$SourceDir = Resolve-Path (Join-Path $PSScriptRoot '..\source')
$env:CGO_ENABLED = '1'
Remove-Item Env:CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:CGO_LDFLAGS -ErrorAction SilentlyContinue
$env:QSDMPLUS_METRICS_REGISTER_STRICT = '1'

Write-Host '==> go test -race -short (mempool, networking, alerting, contracts, state, reputation)'
Push-Location $SourceDir
try {
	& go test -race -short -count=1 -timeout 45m `
		./pkg/mempool/... `
		./pkg/networking/... `
		./internal/alerting/... `
		./pkg/contracts/... `
		./pkg/state/... `
		./pkg/reputation/...
	if ($LASTEXITCODE -ne 0) {
		throw "go test -race failed ($LASTEXITCODE)"
	}
} finally {
	Pop-Location
}
Write-Host 'OK: race-hot-packages finished'
