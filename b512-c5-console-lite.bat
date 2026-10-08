@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run the earlier scripts first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\c5\files\*" "." || goto :fail
git add -- "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/patch/patternfade.go" "internal/patch/patternfade_test.go" "internal/patch/rigcheck.go" "internal/patch/rigcheck_base_test.go" "internal/patch/rigcheckout.go" "internal/patch/testpattern.go" "internal/patch/teststargets.go" "internal/session/dmxout.go" "internal/web/console_c6a_test.go" "internal/web/programmer.go" "internal/web/programmer_c4a_test.go" "internal/web/server.go" "internal/web/showguard.go" "internal/web/static/css/screens-console.css" "internal/web/static/index.html" "internal/web/static/js/api.js" "internal/web/static/js/app.js" "internal/web/static/js/console.js" "internal/web/static/js/testdata/console_test.js" "internal/web/static/js/testdata/minidom.js" "internal/web/static/js/testdata/tests_sync_test.js" "internal/web/tests.go" "internal/web/tests_c5_test.go" "internal/web/workspace.go" || goto :fail
git commit -F "dist\_incoming\c5\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo C5 committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
