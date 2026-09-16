package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"benny512/internal/transport"
	"benny512/internal/web"
)

// These cover the two startup questions persistence introduced:
//
//   - which NIC gets bound when --iface is not given (resolveStartupNIC), and
//   - that --demo never goes near the installation's real settings file.
//
// The NIC question matters more than it looks. Settings.NIC used to be purely
// cosmetic — the socket was bound from --iface (or auto-selected) and nothing
// ever read the setting — so choosing a network card on the Settings screen
// did precisely nothing. Making it the startup default is what turns that
// control from a lie into a setting.

// fakeIfaces is a stand-in interface list. resolveStartupNIC takes the list as
// a parameter rather than calling transport.ListInterfaces itself exactly so
// these cases are about the logic and not about whatever cards the machine
// running the tests happens to have.
func fakeIfaces(names ...string) []transport.Interface {
	out := make([]transport.Interface, 0, len(names))
	for _, n := range names {
		out = append(out, transport.Interface{Name: n, Up: true, IPv4: []string{"10.0.0.1"}})
	}
	return out
}

func captureLogf(t *testing.T) (func(level, format string, args ...any), *[]string) {
	t.Helper()
	var lines []string
	return func(level, format string, args ...any) {
		lines = append(lines, level+": "+strings.TrimSpace(fmt.Sprintf(format, args...)))
	}, &lines
}

func TestResolveStartupNIC_UsesThePersistedNICWhenIfaceIsAbsent(t *testing.T) {
	logf, lines := captureLogf(t)
	got := resolveStartupNIC(fakeIfaces("lo0", "en0", "en7"), "", "en7", logf)
	if got != "en7" {
		t.Errorf("resolveStartupNIC = %q, want the saved %q — a persisted NIC that is not bound is a setting that does nothing", got, "en7")
	}
	if !containsAny(*lines, "en7") {
		t.Errorf("the saved NIC should be named in the startup log, got %v", *lines)
	}
}

func TestResolveStartupNIC_ExplicitIfaceAlwaysWins(t *testing.T) {
	logf, lines := captureLogf(t)
	got := resolveStartupNIC(fakeIfaces("lo0", "en0", "en7"), "en0", "en7", logf)
	if got != "en0" {
		t.Errorf("resolveStartupNIC = %q, want the explicit --iface %q", got, "en0")
	}
	if !containsLevel(*lines, "warn") {
		t.Errorf("a --iface that disagrees with the saved NIC should be reported, got %v", *lines)
	}
}

// An explicit --iface must win even when it names an interface that is not
// present: main then fails on selectInterface's "interface %q not found",
// which is the right answer for someone who typed a name. resolveStartupNIC
// must not quietly substitute the saved one.
func TestResolveStartupNIC_ExplicitIfaceWinsEvenWhenAbsent(t *testing.T) {
	logf, _ := captureLogf(t)
	if got := resolveStartupNIC(fakeIfaces("lo0", "en7"), "enX", "en7", logf); got != "enX" {
		t.Errorf("resolveStartupNIC = %q, want the explicit %q passed through so startup can fail loudly on it", got, "enX")
	}
}

// A venue laptop changes cards. A saved NIC that is gone must degrade to the
// auto-selection this application has always done — never a fatal error.
func TestResolveStartupNIC_VanishedPersistedNICDegradesWithAWarning(t *testing.T) {
	logf, lines := captureLogf(t)
	got := resolveStartupNIC(fakeIfaces("lo0", "en0"), "", "usb-ethernet-from-last-week", logf)
	if got != "" {
		t.Errorf("resolveStartupNIC = %q, want \"\" (auto-select) when the saved NIC is gone", got)
	}
	if !containsLevel(*lines, "warn") || !containsAny(*lines, "usb-ethernet-from-last-week") {
		t.Errorf("a vanished saved NIC must be reported by name, got %v", *lines)
	}
}

func TestResolveStartupNIC_NothingSavedAndNoFlagAutoSelects(t *testing.T) {
	logf, lines := captureLogf(t)
	if got := resolveStartupNIC(fakeIfaces("lo0", "en0"), "", "", logf); got != "" {
		t.Errorf("resolveStartupNIC = %q, want \"\"", got)
	}
	if len(*lines) != 0 {
		t.Errorf("nothing to say when there is no flag and no saved NIC, got %v", *lines)
	}
}

// TestDemoModeDoesNotTouchTheRealSettingsFile is the QA-safety claim: a --demo
// session must never mutate the installation's configuration. It checks both
// halves — the demo server has no settings file at all, and a POST through its
// real HTTP handler leaves benny512-settings.json exactly as it found it
// (whether that is "absent" or "someone's real settings").
func TestDemoModeDoesNotTouchTheRealSettingsFile(t *testing.T) {
	real := settingsStorePath()
	before, beforeErr := os.ReadFile(real)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, start := buildDemo(ctx, false, false)
	defer srv.Close()
	start()
	go srv.Run(ctx)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	if got := srv.SettingsStorePath(); got != "" {
		t.Errorf("the --demo server has settings store path %q, want \"\" — --demo must be in-memory only", got)
	}

	body, _ := json.Marshal(web.Settings{NIC: "demo-should-never-persist-this", PollIntervalMS: 1234, CaptureLimit: 7, TimeoutProfiles: map[string]string{}})
	resp, err := http.Post(ts.URL+"/api/settings", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /api/settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/settings: status = %d", resp.StatusCode)
	}

	after, afterErr := os.ReadFile(real)
	switch {
	case os.IsNotExist(beforeErr):
		if afterErr == nil {
			t.Errorf("--demo created the real settings file %s:\n%s", real, after)
		}
	case beforeErr != nil:
		// Unreadable both before and after is fine; it was never ours to read.
	default:
		if afterErr != nil || string(after) != string(before) {
			t.Errorf("--demo modified the real settings file %s\nbefore: %s\n after: %s (err %v)", real, before, after, afterErr)
		}
	}
	// And the same for the file's recovery copy and the temp files a save
	// would have left in that directory.
	if _, err := os.Stat(real + ".bak"); err == nil && os.IsNotExist(beforeErr) {
		t.Errorf("--demo created %s.bak", real)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(real), ".benny-settings-*")); len(matches) != 0 {
		t.Errorf("--demo left settings temp files behind: %v", matches)
	}
}

func containsAny(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

func containsLevel(lines []string, level string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, level+":") {
			return true
		}
	}
	return false
}
