# Example: fetch dashboard /api/metrics/prometheus and list qsdm_submesh_* lines.
# Usage:
#   .\scripts\verify-submesh-metrics.example.ps1
#   $env:DASHBOARD_URL = 'http://127.0.0.1:8081'; $env:METRICS_SECRET = '...'; .\scripts\verify-submesh-metrics.example.ps1

$base = if ($env:DASHBOARD_URL) { $env:DASHBOARD_URL.TrimEnd('/') } else { 'http://127.0.0.1:8081' }
$uri = "$base/api/metrics/prometheus"
$headers = @{}
if ($env:METRICS_SECRET) {
    $headers['Authorization'] = "Bearer $($env:METRICS_SECRET)"
}
try {
    $resp = Invoke-WebRequest -Uri $uri -Headers $headers -Method Get -UseBasicParsing
    $text = $resp.Content
} catch {
    Write-Error "Request failed: $_"
    exit 1
}
$lines = ($text -split "`r?`n") | Where-Object { $_ -match '^qsdm_submesh_' }
if (-not $lines) {
    Write-Error "No qsdm_submesh_* lines (JWT or METRICS_SECRET may be required)."
    exit 1
}
$lines | ForEach-Object { Write-Output $_ }
Write-Output "OK: submesh metrics visible in exposition."
