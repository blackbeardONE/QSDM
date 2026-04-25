@echo off
REM Quick test script for dashboard on Windows

echo Testing QSDM Dashboard...

REM Test dashboard endpoint
echo.
echo Testing dashboard endpoint...
curl -s -o nul -w "%%{http_code}" http://localhost:8081/ > temp_code.txt
set /p HTTP_CODE=<temp_code.txt
del temp_code.txt

if "%HTTP_CODE%"=="200" (
    echo Dashboard endpoint responding (HTTP %HTTP_CODE%)
) else (
    echo Dashboard endpoint failed (HTTP %HTTP_CODE%)
    echo Make sure the node is running: qsdm.exe
    exit /b 1
)

REM Test metrics API
echo.
echo Testing metrics API...
curl -s http://localhost:8081/api/metrics > nul
if %errorlevel%==0 (
    echo Metrics API responding
    echo.
    echo Sample metrics:
    curl -s http://localhost:8081/api/metrics
) else (
    echo Metrics API failed
    exit /b 1
)

REM Test health API
echo.
echo Testing health API...
curl -s http://localhost:8081/api/health > nul
if %errorlevel%==0 (
    echo Health API responding
) else (
    echo Health API failed
    exit /b 1
)

echo.
echo All tests passed! Dashboard should be working.
echo Open http://localhost:8081 in your browser
pause

