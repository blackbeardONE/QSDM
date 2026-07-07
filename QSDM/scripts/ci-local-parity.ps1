# Local parity with QSDM Go + Validate deploy workflows. Run from monorepo root (folder that contains QSDM/).
# Requires: go. Optional: docker.
# Usage: pwsh -File QSDM/scripts/ci-local-parity.ps1
$ErrorActionPreference = 'Stop'

$QsdmRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$RepoRoot = Resolve-Path (Join-Path $QsdmRoot '..')
$SourceDir = Join-Path $QsdmRoot 'source'

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

Write-Host "==> Repo root: $RepoRoot"
Write-Host "==> QSDM root: $QsdmRoot"
Write-Host "==> Go: $GoExe"

if (Get-Command docker -ErrorAction SilentlyContinue) {
	Write-Host '==> docker compose config (cluster)'
	docker compose -f (Join-Path $RepoRoot 'QSDM/deploy/docker-compose.cluster.yml') config -q
	Write-Host '==> docker compose config (single)'
	docker compose -f (Join-Path $RepoRoot 'QSDM/deploy/docker-compose.single.yml') config -q
} else {
	Write-Host 'SKIP: docker not in PATH'
}

$env:CGO_ENABLED = '0'
Remove-Item Env:CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:CGO_LDFLAGS -ErrorAction SilentlyContinue
$env:QSDM_METRICS_REGISTER_STRICT = '1'

$outExe = Join-Path $env:TEMP 'qsdm-ci-local.exe'
Write-Host '==> go build (no CGO)'
Push-Location $SourceDir
try {
	& $GoExe build -o $outExe ./cmd/qsdm
	if ($LASTEXITCODE -ne 0) {
		throw "go build failed (exit $LASTEXITCODE)"
	}
	Write-Host '==> go test -short (no CGO)'
	& $GoExe test ./... -short -count=1 -timeout 15m
	if ($LASTEXITCODE -ne 0) {
		throw "go test failed (exit $LASTEXITCODE)"
	}

	if ($env:CI_LOCAL_PARITY_CGO_MIGRATE -eq '1') {
		Write-Host '==> go test ./cmd/migrate (CGO + liboqs — requires QSDM/liboqs_install; CI_LOCAL_PARITY_CGO_MIGRATE=1)'
		& pwsh -NoProfile -File (Join-Path $QsdmRoot 'scripts/go-test-migrate-cgo.ps1')
	}
} finally {
	Pop-Location
}

Write-Host 'NOTE: Kubernetes manifest dry-run runs in CI (validate-deploy.yml); local kubectl often needs a cluster context.'
Write-Host 'NOTE: Optional migrate CGO tests: CI_LOCAL_PARITY_CGO_MIGRATE=1 or pwsh -File QSDM/scripts/go-test-migrate-cgo.ps1'

if ($env:SKIP_GOVULNCHECK -eq '1') {
	Write-Host 'SKIP: govulncheck (SKIP_GOVULNCHECK=1)'
} else {
	Write-Host '==> govulncheck (set SKIP_GOVULNCHECK=1 to skip, e.g. known transitive advisories)'
	Push-Location $SourceDir
	try {
		& pwsh -NoProfile -File (Join-Path $QsdmRoot 'scripts/govulncheck-filter.ps1') -GoExe $GoExe
		$gv = $LASTEXITCODE
		if ($gv -ne 0) {
			Write-Host "govulncheck exited $gv (see findings above). CI treats this as a failing job."
			exit $gv
		}
	} finally {
		Pop-Location
	}
}

Write-Host 'OK: ci-local-parity finished'
