@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run the earlier scripts first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\c6\files\*" "." || goto :fail
git add -- "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/web/console_c6b_test.go" "internal/web/console_c6c_test.go" "internal/web/layout.go" "internal/web/layout_c6c_test.go" "internal/web/server.go" "internal/web/static/css/screens-console-controls.css" "internal/web/static/css/screens-console-tests.css" "internal/web/static/css/screens-console.css" "internal/web/static/css/workspace.css" "internal/web/static/index.html" "internal/web/static/js/api.js" "internal/web/static/js/app.js" "internal/web/static/js/console-controls.js" "internal/web/static/js/console-tests.js" "internal/web/static/js/console.js" "internal/web/static/js/programmer.js" "internal/web/static/js/rigcheck.js" "internal/web/static/js/testdata/console_c6c_test.js" "internal/web/static/js/testdata/console_controls_test.js" "internal/web/static/js/testdata/console_test.js" || goto :fail
git commit -F "dist\_incoming\c6\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo C6 committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
