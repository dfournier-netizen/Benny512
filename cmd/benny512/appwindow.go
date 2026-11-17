package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// L3 (owner 2026-11-17): on Windows the exe opens Benny512 in its own
// window — Microsoft Edge in app mode (--app=), which has no tabs, address
// bar or browser menus. Edge ships with Windows 10/11, so this adds no
// dependency. Owner decisions:
//   - it opens on every start; --no-window skips it (the UI is still served,
//     for phones and tablets);
//   - closing the window leaves Benny512 running; quitting is still the
//     console window, which stays. Output is NOT exempt from the C3 lease:
//     if no browser is left connected, after 5 s output follows the
//     Settings lease-loss choice (Blackout or Hold last look);
//   - a second launch is unchanged: the port is in use and it stops.
// The window has its own Edge profile (--user-data-dir under the user's
// config folder) so it remembers its own size and position and never mixes
// with the user's everyday Edge windows, tabs or sign-ins. Without Edge the
// default browser opens the address instead, and the log says so.

// launcher is everything appWindowCommand and openAppWindow need from the
// machine, so tests can describe a machine instead of using this one.
type launcher struct {
	goos   string
	getenv func(string) string
	exists func(string) bool
	start  func(name string, args ...string) error
}

func systemLauncher() launcher {
	return launcher{
		goos:   runtime.GOOS,
		getenv: os.Getenv,
		exists: func(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() },
		start: func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			if err := cmd.Start(); err != nil {
				return err
			}
			// Reap the process whenever it exits; Benny512 never waits on
			// the window (closing it leaves the app running).
			go func() { _ = cmd.Wait() }()
			return nil
		},
	}
}

// edgeCandidates are the places Edge installs itself, in the order tried:
// the machine-wide install (normally Program Files (x86)), then a 64-bit
// Program Files install, then a per-user install.
func edgeCandidates(getenv func(string) string) []string {
	var out []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if base := getenv(env); base != "" {
			out = append(out, filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	}
	return out
}

// appWindowCommand returns the program and arguments that open url as the
// Benny512 window. viaEdge is false when Edge was not found and the default
// browser is used instead. ok is false where there is nothing to open
// (any system but Windows: development and test machines).
func appWindowCommand(l launcher, url, profileDir string) (name string, args []string, viaEdge, ok bool) {
	if l.goos != "windows" {
		return "", nil, false, false
	}
	for _, p := range edgeCandidates(l.getenv) {
		if !l.exists(p) {
			continue
		}
		args = []string{"--app=" + url}
		if profileDir != "" {
			args = append(args, "--user-data-dir="+profileDir)
		}
		args = append(args, "--no-first-run", "--no-default-browser-check")
		return p, args, true, true
	}
	// No Edge: the user's default browser, through the shell's URL handler.
	return "rundll32", []string{"url.dll,FileProtocolHandler", url}, false, true
}

// openAppWindow opens the window and says in the log what happened. It never
// stops Benny512: if nothing can be opened the address is still served and
// the log says where.
func openAppWindow(l launcher, url, profileDir string, logf func(level, format string, args ...any)) {
	name, args, viaEdge, ok := appWindowCommand(l, url, profileDir)
	if !ok {
		return
	}
	if err := l.start(name, args...); err != nil {
		logf("warn", "could not open the Benny512 window (%v); open %s in a browser", err, url)
		return
	}
	if viaEdge {
		logf("info", "opened the Benny512 window (Microsoft Edge app mode); closing it leaves Benny512 running, and output follows the lease-loss setting once no window is connected")
		return
	}
	logf("warn", "Microsoft Edge was not found, so %s opened in your default browser instead of its own window", url)
}

// appWindowProfileDir is the window's own Edge profile folder, beside the
// fixture library in the user's config folder. "" (Edge then uses its
// default profile) if that folder cannot be made.
func appWindowProfileDir() string {
	config, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(config, "Benny512", "app-window")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ""
	}
	return dir
}

// appWindowURL is the address the window opens: always this computer's
// loopback, whatever address other devices use.
func appWindowURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/", port) }
