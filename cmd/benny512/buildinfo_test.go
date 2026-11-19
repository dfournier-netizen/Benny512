package main

import "testing"

// TestAboutShowsWhatVersionPrints (G3b1): Settings › About reads GET
// /api/version, which serves webBuildInfo(); its line must be exactly what
// `benny512.exe --version` prints, stamped or not.
func TestAboutShowsWhatVersionPrints(t *testing.T) {
	if got := webBuildInfo(); got.Line != BuildInfo() || got.Version != version || got.Commit != commit || got.Built != built {
		t.Fatalf("unstamped: webBuildInfo() = %+v, --version prints %q", got, BuildInfo())
	}
	v, c, b := version, commit, built
	t.Cleanup(func() { version, commit, built = v, c, b })
	version, commit, built = "v0.3.5", "abc1234", "2026-11-18T09:00:00Z"
	if got := webBuildInfo(); got.Line != BuildInfo() || got.Line != "benny512 v0.3.5 (commit abc1234, built 2026-11-18T09:00:00Z)" {
		t.Fatalf("stamped: webBuildInfo() = %+v, --version prints %q", got, BuildInfo())
	}
}
