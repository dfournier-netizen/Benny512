@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run the earlier scripts first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\g\files\*" "." || goto :fail
git add -- "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/patch/faders.go" "internal/patch/programmer.go" "internal/patch/programmer_model.go" "internal/patch/programmer_tools.go" "internal/patch/programmer_view.go" "internal/session/dmxout.go" "internal/web/faders.go" "internal/web/faders_g2_test.go" "internal/web/faders_g3_test.go" "internal/web/programmer.go" "internal/web/programmer_c4b_test.go" "internal/web/programmer_g1_test.go" "internal/web/server.go" "internal/web/showguard.go" "internal/web/static/css/faders.css" "internal/web/static/index.html" "internal/web/static/js/api.js" "internal/web/static/js/app.js" "internal/web/static/js/faders.js" "internal/web/static/js/testdata/console_c7_test.js" "internal/web/static/js/testdata/console_test.js" "internal/web/static/js/testdata/faders_test.js" "internal/web/static/js/workspace.js" || goto :fail
git commit -F "dist\_incoming\g\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo G committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
