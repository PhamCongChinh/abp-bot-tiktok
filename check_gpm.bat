@echo off
echo Checking GPM connection...
curl -s http://127.0.0.1:19995/api/v3/profiles
if %errorlevel% neq 0 (
    echo.
    echo [ERROR] Cannot connect to GPM!
    echo Please open GPM Login app first.
) else (
    echo.
    echo [OK] GPM is running!
)
pause
