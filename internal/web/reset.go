// This file implements POST /api/reset: the destructive "wipe everything,
// then exit" full reset (task ask — Dom explicitly chose "Reset and exit"
// over a reset-and-keep-running alternative). Unlike POST /api/devices/
// clear (devicesclear.go), which deliberately spares logging/capture/params
// state, this clears every layer the server owns, including the patch and
// Rig Walk session, deletes their on-disk files, and — if cmd/benny512 has
// wired Server.OnShutdownRequest — shuts the process down shortly after
// responding.
package web

import (
	"errors"
	"net/http"
	"os"
	"time"

	"benny512/internal/params"
)

// ErrResetConfirmationRequired is returned (as a 400) when POST /api/reset
// is called without the exact confirm string — a deliberately un-guessable
// tripwire against an accidental/scripted POST, not real security (task
// ask's contract: {"confirm":"RESET"}).
var ErrResetConfirmationRequired = errors.New("confirmation required")

// resetShutdownDelay is how long handleReset waits, on its own goroutine,
// before invoking OnShutdownRequest — long enough that the 200 response
// this handler just wrote has certainly reached the browser before the
// process starts tearing down (task ask: "the HTTP response actually
// reaches the browser before the process dies").
const resetShutdownDelay = 500 * time.Millisecond

type resetRequest struct {
	Confirm string `json:"confirm"`
}

// resetResponse's Deleted/Errors are always non-nil (never JSON `null`) so
// a browser client can iterate them without a nil check either way.
type resetResponse struct {
	OK      bool     `json:"ok"`
	Deleted []string `json:"deleted"`
	Errors  []string `json:"errors"`
	Exiting bool     `json:"exiting"`
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != "RESET" {
		writeError(w, http.StatusBadRequest, ErrResetConfirmationRequired)
		return
	}

	// 1. Stop the rig check with its existing blackout-and-stop discipline
	// (internal/patch/rigcheck.go), then blackout and stop DMX output
	// outright — RigCheck.Stop only zeroes the universes IT started, so a
	// universe lit via a direct /api/dmx send outside any rig check would
	// otherwise survive untouched. Safety rule, not a nicety: never leave
	// the rig lit through a reset.
	if s.RigCheck != nil {
		s.RigCheck.Stop()
	}
	s.DMX.Blackout()
	s.DMX.Stop()

	// 2. End the Rig Walk session: identify-off for whichever device is
	// currently under the walk spotlight, mirroring handleWalkEnd's own
	// "leaving the screen" discipline (internal/web/walk.go) — best-effort,
	// the session itself is discarded (in-memory and on disk) as part of
	// step 5 below.
	if sess, ok := s.walkStore.Get(); ok {
		if dev, ok2 := sess.CurrentDevice(); ok2 {
			_ = s.walkSetIdentify(dev.UID, false)
		}
	}

	// 3. Clear the registry (devices AND the Art-Net node table), every
	// cached Table of Devices, the process-wide PARAMETER_DESCRIPTION cache
	// plus per-UID introspection state, and both capture rings.
	s.Registry.ClearDevices()
	s.Nodes.ClearNodes()
	s.RDM.ClearToD()
	// The automatic identity reader's ledger goes with the device table it
	// describes. Left behind, every UID would still read as "already read"
	// after the reset that deleted what was read — and the rig would never
	// fill in again.
	s.AutoRead.Reset()
	params.ClearDescriptorCache()
	params.ClearAllDeviceState()
	s.Capture.Clear()
	s.RDMCapture.Clear()

	// 4. Close the RDM disk logger, if one is open.
	s.settingsMu.Lock()
	if s.rdmLogger != nil {
		_ = s.rdmLogger.Close()
		s.rdmLogger = nil
	}
	s.settingsMu.Unlock()

	// 5. Delete the on-disk stores (patch JSON, rig-walk JSON), tracking
	// what actually happened: a path that was never configured (empty —
	// e.g. --demo's PatchStore, see cmd/benny512/main.go, or any test that
	// built a Server via New directly) is silently skipped; a file that was
	// already missing is NOT an error (os.IsNotExist means "already
	// clean"); any other removal failure is collected as a string rather
	// than aborting the rest of the reset. Then clear each store's
	// in-memory state too (Clear on an already-deleted path is a no-op
	// second removal attempt, harmless).
	var deleted []string
	var resetErrs []string
	deleteTracked := func(path string) {
		if path == "" {
			return
		}
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				resetErrs = append(resetErrs, err.Error())
			}
			return
		}
		deleted = append(deleted, path)
	}
	deleteTracked(s.patchStorePath)
	deleteTracked(s.walkStorePath)
	if err := s.PatchStore.Clear(); err != nil {
		resetErrs = append(resetErrs, err.Error())
	}
	s.walkStore.Clear()
	// DELIBERATELY ABSENT: the Fixture Library (s.LibraryStore, see
	// internal/library and library.go). It is the owner's explicit, standing
	// exemption from this reset — the library is the knowledge accumulated
	// across every job (a fixture type's modes, footprints and channel maps),
	// not this rig's state, and a reset that threw it away would destroy the
	// one thing here that cannot be rebuilt by re-importing an MVR. Do not
	// add it, and do not add its file to deleteTracked. Two things make that
	// hard to do by accident rather than merely remembered:
	// internal/library.Store offers no Clear() method at all, and
	// SetLibraryStorePath deliberately does not retain the path on the
	// Server, so there is nothing here to call and no path here to delete.
	// See Server.LibraryStore's doc comment and
	// TestLibrary_SurvivesFullReset.

	// 6. Reset Settings to the same defaults New installs — and WRITE those
	// defaults through to benny512-settings.json, so the reset survives the
	// restart this handler is about to trigger. Settings became durable in
	// settingsdurable.go; a reset that only cleared them in memory would be
	// undone by the very next launch reloading the file, which is the exact
	// shape of bug persistence introduces if the destructive paths are not
	// updated alongside it.
	//
	// The file is REWRITTEN rather than deleted (unlike the patch and
	// rig-walk files above) for two reasons: the previous configuration then
	// survives one step back in benny512-settings.json.bak, and "the file
	// says defaults" and "there is no file" are the same thing to the loader
	// anyway, so writing is strictly the more recoverable of the two.
	//
	// A failed write is collected like any other reset error rather than
	// aborting: the in-memory settings are defaults either way, and the user
	// is told the file could not be written.
	//
	// DELIBERATELY ABSENT, as before: benny512-sacn.json. The CID inside it
	// is this installation's E1.31 source identity and is supposed to outlive
	// a reset (see internal/sacn/settings.go) — do not add it here.
	s.settingsMu.Lock()
	s.settings = defaultSettings()
	settingsErr := s.settingsStore.Save(s.settings)
	s.settingsMu.Unlock()
	if settingsErr != nil {
		resetErrs = append(resetErrs, settingsErr.Error())
	}

	if deleted == nil {
		deleted = []string{}
	}
	if resetErrs == nil {
		resetErrs = []string{}
	}

	// 7. Write the response and flush it before doing anything that might
	// end the process.
	exiting := s.OnShutdownRequest != nil
	writeJSON(w, http.StatusOK, resetResponse{OK: true, Deleted: deleted, Errors: resetErrs, Exiting: exiting})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// 8. Trigger process shutdown, after a short delay so the response
	// above has certainly reached the browser first.
	if s.OnShutdownRequest != nil {
		go func() {
			time.Sleep(resetShutdownDelay)
			s.OnShutdownRequest("reset")
		}()
	}
}
