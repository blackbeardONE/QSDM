@echo off
REM Simple HTTP server to serve the tests directory on port 8000 using Python if available

where python >nul 2>&1
if %errorlevel%==0 (
    echo Serving tests directory at http://localhost:8000 using python
    cd tests
    python -m http.server 8000
    exit /b
)

where python3 >nul 2>&1
if %errorlevel%==0 (
    echo Serving tests directory at http://localhost:8000 using python3
    cd tests
    python3 -m http.server 8000
    exit /b
)

echo Python is not installed or not in PATH. Please install Python to use this script.
pause
