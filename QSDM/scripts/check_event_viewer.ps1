# Check Windows Event Viewer for qsdm.exe crash details
Write-Host "Checking Event Viewer for qsdm.exe crashes..." -ForegroundColor Cyan
Write-Host ""

# Get recent application errors for qsdm.exe
$events = Get-WinEvent -FilterHashtable @{
    LogName = 'Application'
    ProviderName = 'Application Error'
    StartTime = (Get-Date).AddHours(-1)
} -ErrorAction SilentlyContinue | Where-Object {
    $_.Message -like '*qsdm.exe*'
} | Select-Object -First 5

if ($events) {
    Write-Host "Found crash events:" -ForegroundColor Yellow
    Write-Host ""
    foreach ($event in $events) {
        Write-Host "Time: $($event.TimeCreated)" -ForegroundColor Cyan
        Write-Host "Event ID: $($event.Id)" -ForegroundColor Gray
        Write-Host "Message:" -ForegroundColor Gray
        $event.Message -split "`n" | Select-Object -First 20 | ForEach-Object {
            Write-Host "  $_" -ForegroundColor White
        }
        Write-Host ""
        Write-Host ("-" * 70) -ForegroundColor Gray
        Write-Host ""
    }
} else {
    Write-Host "No recent crash events found in Event Viewer" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "To check manually:" -ForegroundColor Cyan
    Write-Host "  1. Open Event Viewer (eventvwr.msc)" -ForegroundColor Gray
    Write-Host "  2. Go to Windows Logs > Application" -ForegroundColor Gray
    Write-Host "  3. Look for errors related to qsdm.exe" -ForegroundColor Gray
    Write-Host "  4. Check the 'Faulting Module' field" -ForegroundColor Gray
}

