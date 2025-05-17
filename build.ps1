# PowerShell script to build QSDM project with correct CGO environment variables

$env:CGO_CFLAGS = "-IC:\liboqs\include -IC:\CUDA\include"
$env:CGO_LDFLAGS = "-LC:\liboqs\lib -LC:\CUDA\lib\x64"
$env:CGO_ENABLED = "1"

go clean -cache -modcache -testcache
go build -o qsdm.exe ./cmd/qsdm

if ($LASTEXITCODE -eq 0) {
    Write-Host "Build succeeded. Executable: qsdm.exe"
} else {
    Write-Host "Build failed."
    exit $LASTEXITCODE
}
