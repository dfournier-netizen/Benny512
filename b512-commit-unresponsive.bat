@echo off
setlocal
cd /d "%~dp0"
echo Creating branch fix/rdm-unresponsive-device from the current main...
for /f %%b in ('git rev-parse --abbrev-ref HEAD') do set CUR=%%b
if /i not "%CUR%"=="main" (echo Expected to be on main, found %CUR%. Stopping. & goto :fail)
git switch -c fix/rdm-unresponsive-device || goto :fail
git add -- "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/decisions/0001-silence-pauses-a-device-only-with-link-evidence.md" "docs/evidence/captures/RDM-LOG36.txt" "internal/registry/registry.go" "internal/registry/unreachable_cause_test.go" "internal/session/rdmcontroller.go" "internal/session/rdmcontroller_test.go" "internal/session/rdmdiscovery.go" "internal/session/rdmproxy.go" "internal/session/rdmsilence_test.go" "internal/web/diagnostics.go" "internal/web/server.go" "internal/web/nodes_fault_test.go" "internal/web/unresponsive_fixture_test.go" "internal/web/unresponsive_fixture_cause_test.go" "internal/web/static/js/devices.js" "internal/web/static/js/nodes.js" "internal/web/static/js/testdata/nodes_fault_test.js" || goto :fail
git commit -F b512-commit-unresponsive-msg.txt || goto :fail
echo.
git log --oneline -3
echo.
echo Committed. Nothing was pushed. Publish with:  git push -u origin fix/rdm-unresponsive-device
echo You can delete b512-commit-unresponsive.bat and b512-commit-unresponsive-msg.txt now.
pause
exit /b 0
:fail
echo.
echo Something went wrong - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
