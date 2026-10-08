@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run the earlier scripts first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\c4\files\*" "." || goto :fail
git add -- "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/patch/entry.go" "internal/patch/programmer.go" "internal/patch/programmer_model.go" "internal/patch/programmer_tools.go" "internal/patch/programmer_view.go" "internal/patch/rigcheck_base_test.go" "internal/session/dmxout.go" "internal/session/dmxout_c4a_test.go" "internal/session/dmxout_c4b_test.go" "internal/web/gdtfparse_channeldetail_test.go" "internal/web/output.go" "internal/web/programmer.go" "internal/web/programmer_c4a_test.go" "internal/web/programmer_c4b_test.go" "internal/web/programmer_sync_test.go" "internal/web/programmer_tools.go" "internal/web/server.go" "internal/web/showguard.go" "internal/web/static/index.html" "internal/web/static/js/api.js" "internal/web/static/js/gdtfparse.js" "internal/web/static/js/programmer.js" "internal/web/static/js/testdata/gdtf_channeldetail_test.js" "internal/web/static/js/testdata/programmer_sync_test.js" "internal/web/universeidentify_test.go" "internal/web/workspace.go" || goto :fail
git commit -F "dist\_incoming\c4\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo C4 committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
