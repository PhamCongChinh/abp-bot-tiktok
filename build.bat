@echo off
echo ========================================
echo Building ABP Bot TikTok...
echo ========================================
echo.

REM Build with optimization
go build -ldflags="-s -w" -o bot.exe cmd/main.go

if %ERRORLEVEL% EQU 0 (
    echo.
    echo ========================================
    echo ✅ Build successful!
    echo ========================================
    echo.
    echo Binary: bot.exe
    echo Size: 
    dir bot.exe | findstr bot.exe
    echo.
    echo To run: bot.exe
    echo.
) else (
    echo.
    echo ========================================
    echo ❌ Build failed!
    echo ========================================
    echo.
)

pause
