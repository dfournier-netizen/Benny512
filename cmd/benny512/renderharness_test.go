package main

// TestRenderHarness is not a check: with BENNY512_RENDER_GDTF set to a GDTF
// library folder (one written by internal/gdtfshare's TestWriteRenderLibrary)
// and BENNY512_RENDER_DIR to a disposable temp folder, it serves the --demo
// build (buildDemo, exactly as `benny512 --demo` builds it) on
// BENNY512_RENDER_ADDR until the test binary is killed, with that folder as
// its GDTF library and the Fixture Library in the temp folder. It exists for
// the G3b browser render-proofs: --demo itself has no GDTF library (main.go
// gives it none, on purpose), and this adds one without any production
// switch. It uses only what main.go uses: buildDemo, SetLibraryStorePath,
// SetGDTFLibraryPath, SetBuildInfo. Skipped when the variables are unset,
// which is every ordinary test run.
//
// Any conversation with GDTF Share from here (Sync now, Test login, a
// single pull) is meant to fail as offline: run it with HTTPS_PROXY pointed
// at a closed port so nothing can reach the real gdtf-share.com.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderHarness(t *testing.T) {
	gdtfDir, dir, addr := os.Getenv("BENNY512_RENDER_GDTF"), os.Getenv("BENNY512_RENDER_DIR"), os.Getenv("BENNY512_RENDER_ADDR")
	if gdtfDir == "" || dir == "" || addr == "" {
		t.Skip("set BENNY512_RENDER_GDTF, BENNY512_RENDER_DIR and BENNY512_RENDER_ADDR to serve the render-proof build")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, start := buildDemo(ctx, false, false)
	srv.SetLibraryStorePath(filepath.Join(dir, "benny512-library.json"))
	srv.SetGDTFLibraryPath(gdtfDir)
	srv.SetBuildInfo(webBuildInfo())
	start()
	go srv.Run(ctx)
	t.Logf("serving the --demo build with GDTF library %s on %s", gdtfDir, addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		t.Fatal(err)
	}
}
