@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run the earlier scripts first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\c7\files\*" "." || goto :fail
git rm -q -- "internal/web/fixturetype_scope_test.go" "internal/web/patternfade.go" "internal/web/patternfade_test.go" "internal/web/rigcheck_scope_test.go" "internal/web/static/js/rigcheck.js" "internal/web/static/js/send.js" "internal/web/static/js/testdata/rigcheck_scope_test.js" || goto :fail
git add -- "WORKFLOWS.md" "cmd/benny512/main.go" "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/session/dmxout.go" "internal/web/ballyhoo_rate_test.go" "internal/web/console_c6c_test.go" "internal/web/console_c7_test.go" "internal/web/console_c8_test.go" "internal/web/midi.go" "internal/web/output_c3_test.go" "internal/web/output_c3_ui_test.go" "internal/web/patch.go" "internal/web/patch_lifecycle_test.go" "internal/web/patch_test.go" "internal/web/programmer_c4a_test.go" "internal/web/reset_test.go" "internal/web/sacn_api_test.go" "internal/web/sacn_isolation_test.go" "internal/web/sacn_ui_test.go" "internal/web/server.go" "internal/web/server_test.go" "internal/web/showguard.go" "internal/web/static/css/screens-console-midi.css" "internal/web/static/css/screens-console-tools.css" "internal/web/static/index.html" "internal/web/static/js/api.js" "internal/web/static/js/app.js" "internal/web/static/js/console-controls.js" "internal/web/static/js/console-midi.js" "internal/web/static/js/console-tests.js" "internal/web/static/js/console-tools.js" "internal/web/static/js/console.js" "internal/web/static/js/patch.js" "internal/web/static/js/testdata/console_c6c_test.js" "internal/web/static/js/testdata/console_c7_test.js" "internal/web/static/js/testdata/console_midi_test.js" "internal/web/static/js/testdata/console_test.js" "internal/web/static/js/testdata/patch_lifecycle_test.js" "internal/web/static/js/testdata/universeidentify_test.js" "internal/web/static/js/universeidentify.js" "internal/web/static/js/walk.js" "internal/web/static/js/workspace.js" "internal/web/tests.go" "internal/web/tests_c5_test.go" "internal/web/universe_scheme_test.go" "internal/web/universeidentify_test.go" || goto :fail
git commit -F "dist\_incoming\c7\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo C7 committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
