@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run the earlier scripts first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\c2\files\*" "." || goto :fail
git add -- "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/patch/channeldetail_migrate_test.go" "internal/patch/entry.go" "internal/patch/location_migrate_test.go" "internal/patch/phasecount.go" "internal/web/layout.go" "internal/web/layout_test.go" "internal/web/mvrlocation_test.go" "internal/web/patch.go" "internal/web/patchimport.go" "internal/web/server.go" "internal/web/static/js/mvrimport.js" "internal/web/static/js/mvrparse.js" "internal/web/static/js/testdata/capture_demo_show_real_extract.xml" "internal/web/static/js/testdata/mvr_location_dump.js" "internal/web/workspace.go" || goto :fail
git commit -F "dist\_incoming\c2\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo C2 committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
