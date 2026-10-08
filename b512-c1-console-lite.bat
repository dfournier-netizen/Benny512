@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run b512-commit-unresponsive.bat first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\c1\files\*" "." || goto :fail
git add -- "docs/HANDOFF.md" "docs/design/console-lite-ui-brief.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/library/channeldetail_verify_test.go" "internal/library/library.go" "internal/library/store.go" "internal/library/testdata/legacy_v1_verified_library.json" "internal/library/verification.go" "internal/patch/asfound_test.go" "internal/patch/asfoundnotfitted_test.go" "internal/patch/channeldetail_migrate_test.go" "internal/patch/channelfunctions_test.go" "internal/patch/entry.go" "internal/web/gdtfparse_channeldetail_test.go" "internal/web/library.go" "internal/web/library_reread_test.go" "internal/web/patch.go" "internal/web/patchattrs.go" "internal/web/patchimport.go" "internal/web/static/js/api.js" "internal/web/static/js/gdtfparse.js" "internal/web/static/js/library.js" "internal/web/static/js/mvrimport.js" "internal/web/static/js/patch.js" "internal/web/static/js/testdata/gdtf_channeldetail_test.js" "internal/web/static/js/testdata/robe_bmfl_spot_real_extract.xml" "internal/web/static/js/testdata/robe_extracts_legacy_channelfunctions.golden.json" "internal/web/static/js/testdata/robe_ledbeam100_real_extract.xml" "internal/web/workspace.go" "internal/web/workspace_channeldetail_test.go" || goto :fail
git commit -F "dist\_incoming\c1\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo C1 committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
