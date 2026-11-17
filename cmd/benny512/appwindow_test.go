package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeMachine describes a computer for the L3 window launcher: which Edge
// installs exist, and a record of what was started.
type fakeMachine struct {
	goos    string
	env     map[string]string
	files   map[string]bool
	started [][]string
	fail    error
}

func (m *fakeMachine) launcher() launcher {
	return launcher{
		goos:   m.goos,
		getenv: func(k string) string { return m.env[k] },
		exists: func(p string) bool { return m.files[p] },
		start: func(name string, args ...string) error {
			m.started = append(m.started, append([]string{name}, args...))
			return m.fail
		},
	}
}

func edgeAt(base string) string {
	return filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
}

var winEnv = map[string]string{
	"ProgramFiles(x86)": `C:\Program Files (x86)`,
	"ProgramFiles":      `C:\Program Files`,
	"LOCALAPPDATA":      `C:\Users\dom\AppData\Local`,
}

type logLine struct{ level, text string }

func captureLog() (*[]logLine, func(string, string, ...any)) {
	var lines []logLine
	return &lines, func(level, format string, args ...any) {
		lines = append(lines, logLine{level, fmt.Sprintf(format, args...)})
	}
}

// TestL3AppWindowCommand: Edge app mode with its own profile when Edge is
// installed (machine-wide first, then per-user); the default browser when
// it is not; nothing at all off Windows.
func TestL3AppWindowCommand(t *testing.T) {
	url := "http://127.0.0.1:8080/"
	prof := `C:\Users\dom\AppData\Roaming\Benny512\app-window`
	edgeArgs := []string{"--app=" + url, "--user-data-dir=" + prof, "--no-first-run", "--no-default-browser-check"}
	cases := []struct {
		name         string
		goos         string
		files        []string
		profile      string
		wantName     string
		wantArgs     []string
		wantEdge, ok bool
	}{
		{"machine-wide Edge", "windows", []string{edgeAt(winEnv["ProgramFiles(x86)"]), edgeAt(winEnv["LOCALAPPDATA"])}, prof,
			edgeAt(winEnv["ProgramFiles(x86)"]), edgeArgs, true, true},
		{"64-bit Program Files Edge", "windows", []string{edgeAt(winEnv["ProgramFiles"])}, prof,
			edgeAt(winEnv["ProgramFiles"]), edgeArgs, true, true},
		{"per-user Edge only", "windows", []string{edgeAt(winEnv["LOCALAPPDATA"])}, prof,
			edgeAt(winEnv["LOCALAPPDATA"]), edgeArgs, true, true},
		{"no profile folder: Edge's default profile", "windows", []string{edgeAt(winEnv["ProgramFiles(x86)"])}, "",
			edgeAt(winEnv["ProgramFiles(x86)"]), []string{"--app=" + url, "--no-first-run", "--no-default-browser-check"}, true, true},
		{"no Edge: default browser", "windows", nil, prof,
			"rundll32", []string{"url.dll,FileProtocolHandler", url}, false, true},
		{"not Windows: nothing", "linux", []string{edgeAt(winEnv["ProgramFiles(x86)"])}, prof,
			"", nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMachine{goos: c.goos, env: winEnv, files: map[string]bool{}}
			for _, f := range c.files {
				m.files[f] = true
			}
			name, args, viaEdge, ok := appWindowCommand(m.launcher(), url, c.profile)
			if name != c.wantName || !reflect.DeepEqual(args, c.wantArgs) || viaEdge != c.wantEdge || ok != c.ok {
				t.Fatalf("got %q %q edge=%v ok=%v\nwant %q %q edge=%v ok=%v", name, args, viaEdge, ok, c.wantName, c.wantArgs, c.wantEdge, c.ok)
			}
		})
	}
}

// TestL3OpenAppWindowLogs: what the console log says in each case, and that
// a failure to open never stops anything (it only logs the address).
func TestL3OpenAppWindowLogs(t *testing.T) {
	url := appWindowURL(8080)
	if url != "http://127.0.0.1:8080/" {
		t.Fatalf("window URL = %q, want this computer's loopback", url)
	}

	m := &fakeMachine{goos: "windows", env: winEnv, files: map[string]bool{edgeAt(winEnv["ProgramFiles(x86)"]): true}}
	lines, logf := captureLog()
	openAppWindow(m.launcher(), url, "P", logf)
	if len(m.started) != 1 || m.started[0][0] != edgeAt(winEnv["ProgramFiles(x86)"]) || m.started[0][1] != "--app="+url {
		t.Fatalf("started %q, want Edge in app mode", m.started)
	}
	if len(*lines) != 1 || (*lines)[0].level != "info" || !strings.Contains((*lines)[0].text, "closing it leaves Benny512 running") {
		t.Fatalf("log %q: want one info line saying closing the window leaves Benny512 running", *lines)
	}

	m = &fakeMachine{goos: "windows", env: winEnv, files: map[string]bool{}}
	lines, logf = captureLog()
	openAppWindow(m.launcher(), url, "P", logf)
	if len(m.started) != 1 || m.started[0][0] != "rundll32" {
		t.Fatalf("started %q, want the default browser", m.started)
	}
	if len(*lines) != 1 || (*lines)[0].level != "warn" || !strings.Contains((*lines)[0].text, "Microsoft Edge was not found") || !strings.Contains((*lines)[0].text, url) {
		t.Fatalf("log %q: want a warning naming the missing Edge and the address", *lines)
	}

	m = &fakeMachine{goos: "windows", env: winEnv, files: map[string]bool{edgeAt(winEnv["ProgramFiles(x86)"]): true}, fail: errors.New("access denied")}
	lines, logf = captureLog()
	openAppWindow(m.launcher(), url, "P", logf)
	if len(*lines) != 1 || (*lines)[0].level != "warn" || !strings.Contains((*lines)[0].text, "access denied") || !strings.Contains((*lines)[0].text, "open "+url) {
		t.Fatalf("log %q: want a warning with the reason and the address to open by hand", *lines)
	}

	m = &fakeMachine{goos: "linux", env: winEnv, files: map[string]bool{}}
	lines, logf = captureLog()
	openAppWindow(m.launcher(), url, "P", logf)
	if len(m.started) != 0 || len(*lines) != 0 {
		t.Fatalf("off Windows: started %q, logged %q; want nothing", m.started, *lines)
	}
}
