@echo off
setlocal
cd /d "%~dp0"
git rev-parse --verify --quiet feat/console-lite >nul || (echo Branch feat/console-lite not found. Run the earlier scripts first. & goto :fail)
git diff --quiet || (echo You have uncommitted changes. Commit or stash them first. & goto :fail)
git diff --cached --quiet || (echo You have staged changes. Commit them first. & goto :fail)
git switch feat/console-lite || goto :fail
xcopy /E /Y /I /Q "dist\_incoming\c3\files\*" "." || goto :fail
git rm -q -- "internal/patch/rigcheck_sacn_test.go" "internal/web/pattern_protocol_test.go" "internal/web/static/js/testdata/functioncheck_protocol_test.js" || goto :fail
git add -- "cmd/benny512/demo.go" "cmd/benny512/main.go" "cmd/benny512/rehearsal.go" "cmd/benny512/rehearsal_test.go" "docs/HANDOFF.md" "docs/notes/2026-10.md" "docs/plans/console-lite.md" "internal/library/channeldetail_verify_test.go" "internal/library/library.go" "internal/library/schema2_test.go" "internal/library/store.go" "internal/library/testdata/README.md" "internal/patch/catalog.go" "internal/patch/channeldetail_migrate_test.go" "internal/patch/durable.go" "internal/patch/entry.go" "internal/patch/gdtf10defaults_test.go" "internal/patch/location_migrate_test.go" "internal/patch/patternfade.go" "internal/patch/patternfade_test.go" "internal/patch/profilecache_test.go" "internal/patch/rigcheck.go" "internal/patch/rigcheck_golden_test.go" "internal/patch/rigcheck_test.go" "internal/patch/rigcheckout.go" "internal/patch/showfile.go" "internal/patch/testdata/rigcheck_artnet_golden.txt" "internal/patch/testpattern.go" "internal/patch/testpattern_test.go" "internal/sacn/sender.go" "internal/session/dmxout.go" "internal/session/dmxout_test.go" "internal/web/fixturetype_scope_test.go" "internal/web/gdtfparse_channeldetail_test.go" "internal/web/layout.go" "internal/web/layout_c2b_test.go" "internal/web/layout_test.go" "internal/web/library_test.go" "internal/web/output.go" "internal/web/output_c3_test.go" "internal/web/output_c3_ui_test.go" "internal/web/patch.go" "internal/web/patch_test.go" "internal/web/profilecache_test.go" "internal/web/reset.go" "internal/web/reset_test.go" "internal/web/sacn.go" "internal/web/sacn_api_test.go" "internal/web/sacn_isolation_test.go" "internal/web/sacn_ui_test.go" "internal/web/server.go" "internal/web/server_test.go" "internal/web/settingsdurable.go" "internal/web/static/css/workspace.css" "internal/web/static/js/api.js" "internal/web/static/js/gdtfparse.js" "internal/web/static/js/mvrimport.js" "internal/web/static/js/patch.js" "internal/web/static/js/rigcheck.js" "internal/web/static/js/send.js" "internal/web/static/js/settings.js" "internal/web/static/js/testdata/README.md" "internal/web/static/js/testdata/gdtf_channeldetail_test.js" "internal/web/static/js/testdata/output_strip_test.js" "internal/web/static/js/testdata/paladin_cube_legacy_channelfunctions.golden.json" "internal/web/static/js/testdata/patch_lifecycle_test.js" "internal/web/static/js/testdata/settings_output_test.js" "internal/web/static/js/testdata/settings_sacn_test.js" "internal/web/static/js/testdata/universeidentify_test.js" "internal/web/static/js/ui.js" "internal/web/static/js/universeidentify.js" "internal/web/static/js/workspace.js" "internal/web/testdata/c2_layout_show.json" "internal/web/universeidentify.go" "internal/web/universeidentify_test.go" "internal/web/workspace.go" "internal/web/workspace_test.go" || goto :fail
git commit -F "dist\_incoming\c3\msg.txt" || goto :fail
echo.
git log --oneline -3
echo.
echo C3 committed on feat/console-lite. Nothing was pushed. You can delete this .bat now.
pause
exit /b 0
:fail
echo.
echo Stopped - nothing was pushed. Copy this window and send it to Claude.
pause
exit /b 1
