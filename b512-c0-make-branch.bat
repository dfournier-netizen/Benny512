@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul && (echo feat/console-lite already exists. Go on to b512-c1-console-lite.bat. & goto :done)
git rev-parse --verify --quiet fix/rdm-unresponsive-device >nul || (echo Branch fix/rdm-unresponsive-device not found. Stopping. & goto :fail)
git branch feat/console-lite fix/rdm-unresponsive-device || goto :fail
echo Created feat/console-lite from fix/rdm-unresponsive-device. Nothing was pushed.
:done
echo Next: run b512-c1-console-lite.bat
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
