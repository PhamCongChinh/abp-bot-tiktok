@echo off
echo Installing Playwright driver v1.57...
go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install chromium
echo.
echo Done! Now you can run: go run cmd/main.go
pause
