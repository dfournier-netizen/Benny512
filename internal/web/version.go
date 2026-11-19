package web

import (
	"fmt"
	"net/http"
)

// BuildInfo is the binary's build identity: what `benny512.exe --version`
// prints (cmd/benny512 BuildInfo, stamped by the release workflow's
// ldflags), served at GET /api/version for Settings › About (G3b).
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
	// Line is the one-line form, word for word what --version prints.
	Line string `json:"line"`
}

// BuildLine is the one-line build identity. cmd/benny512's --version uses
// it, so About and --version can never disagree.
func BuildLine(version, commit, built string) string {
	return fmt.Sprintf("benny512 %s (commit %s, built %s)", version, commit, built)
}

// unstampedBuild is what a local `go build` is: it says so rather than
// claiming a version it does not have (cmd/benny512's zero values).
func unstampedBuild() BuildInfo {
	return BuildInfo{Version: "dev", Commit: "unknown", Built: "unknown", Line: BuildLine("dev", "unknown", "unknown")}
}

// SetBuildInfo records the build identity. Line is recomputed from the
// three parts. Call before serving, like the other Set* setters.
func (s *Server) SetBuildInfo(b BuildInfo) {
	b.Line = BuildLine(b.Version, b.Commit, b.Built)
	s.build = b
}

// handleVersion: GET /api/version -> BuildInfo.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.build)
}
