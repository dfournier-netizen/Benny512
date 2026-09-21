package web

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"benny512/internal/params"
	"benny512/internal/patch"
	"benny512/internal/rdm"
)

type savedGroup struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	EntryIDs []string `json:"entryIds"`
}
type savedTestPreset struct {
	savedGroup
	FadeMS  *int64              `json:"fadeMs,omitempty"`
	Specs   []patch.PatternSpec `json:"specs"`
	Isolate bool                `json:"isolate"`
}
type observedFixture struct {
	UID          string                     `json:"uid"`
	Universe     uint16                     `json:"universe"`
	Address      uint16                     `json:"address"`
	AddressKnown bool                       `json:"addressKnown"`
	Footprint    uint16                     `json:"footprint"`
	InfoKnown    bool                       `json:"infoKnown"`
	LastSeen     time.Time                  `json:"lastSeen"`
	Unreachable  bool                       `json:"unreachable"`
	Values       map[rdm.ParameterID][]byte `json:"values"`
}
type rigBaseline struct {
	ProfileDigests map[string]string `json:"profileDigests"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	At             time.Time         `json:"at"`
	Entries        []patch.Entry     `json:"entries"`
	Devices        []observedFixture `json:"devices"`
	Issues         []workspaceIssue  `json:"issues"`
}
type showWorkspace struct {
	Groups    []savedGroup      `json:"groups"`
	Presets   []savedTestPreset `json:"presets"`
	Baselines []rigBaseline     `json:"baselines"`
}

func workspaceFor(p patch.Patch) showWorkspace {
	w := showWorkspace{Groups: []savedGroup{}, Presets: []savedTestPreset{}, Baselines: []rigBaseline{}}
	_ = json.Unmarshal(p.Workspace, &w)
	if w.Groups == nil {
		w.Groups = []savedGroup{}
	}
	if w.Presets == nil {
		w.Presets = []savedTestPreset{}
	}
	if w.Baselines == nil {
		w.Baselines = []rigBaseline{}
	}
	return w
}

type workspaceIssue struct {
	EntryID string `json:"entryId"`
	UID     string `json:"uid"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Action  string `json:"action"`
}

// Observation is cache-only: looking at context, issues or a report never
// initiates discovery, RDM polling, or a write to the rig.
func (s *Server) observeFixtures() []observedFixture {
	result := make([]observedFixture, 0)
	for _, f := range s.Registry.Devices() {
		d := observedFixture{UID: f.UID.String(), Universe: f.Port.RawValue(), Footprint: f.DMXFootprint, InfoKnown: f.HasDeviceInfo, LastSeen: f.LastSeen, Unreachable: f.ProxyUnreachable, Values: map[rdm.ParameterID][]byte{}}
		if data := f.Params[rdm.PIDDMXStartAddress]; len(data) == 2 {
			d.Address = binary.BigEndian.Uint16(data)
			d.AddressKnown = true
		}
		if !d.AddressKnown {
			if info, err := params.DecodeDeviceInfo(f.Params[rdm.PIDDeviceInfo]); err == nil {
				d.Address = info.DMXStartAddress
				d.AddressKnown = true
			}
		}
		// Configuration only; transient sensors/counters are not configuration drift.
		for _, pid := range []rdm.ParameterID{rdm.PIDDMXPersonality, rdm.PIDDeviceLabel, rdm.PIDSoftwareVersionLabel, rdm.PIDPanInvert, rdm.PIDTiltInvert, rdm.PIDPanTiltSwap, rdm.PIDCurve} {
			if v, ok := f.Params[pid]; ok {
				d.Values[pid] = append([]byte{}, v...)
			}
		}
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UID < result[j].UID })
	return result
}

func workspaceIssues(p patch.Patch, devices []observedFixture) []workspaceIssue {
	out := make([]workspaceIssue, 0)
	byUID := map[string]observedFixture{}
	for _, d := range devices {
		byUID[d.UID] = d
	}
	for _, f := range patch.DetectCollisions(p) {
		for _, id := range f.EntryIDs {
			name := ""
			if i := p.IndexOf(id); i >= 0 {
				name = p.Entries[i].Name
			}
			out = append(out, workspaceIssue{EntryID: id, Name: name, Message: f.Message, Action: "entries"})
		}
	}
	for _, e := range p.Entries {
		add := func(message, action string) {
			out = append(out, workspaceIssue{EntryID: e.ID, UID: e.ConfirmedUID, Name: e.Name, Message: message, Action: action})
		}
		if e.ConfirmedUID == "" {
			add("Not committed", "reconcile")
			continue
		}
		d, ok := byUID[e.ConfirmedUID]
		if !ok {
			add("Committed fixture not in discovery cache", "reconcile")
			continue
		}
		if d.Unreachable {
			add("RDM proxy reports unreachable", "devices")
		}
		if !d.AddressKnown {
			add("Live address not read", "devices")
		} else if d.Universe != e.Universe || d.Address != e.StartAddress {
			add("Live address differs from patch", "reconcile")
		}
		if d.InfoKnown && e.Footprint != d.Footprint {
			add("Live footprint differs from profile", "reconcile")
		}
		for _, line := range patch.DiffEntry(e) {
			if line.State == patch.DiffDiffers {
				add(line.Label+" differs (last read)", "reconcile")
			}
		}
	}
	return out
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	// Do not call PatternStatus here: an unrelated browser's context polling
	// must not keep another operator's abandoned test alive.
	name, ok := s.PatchStore.Context()
	s.identifyMu.Lock()
	identifyRunning := s.identify.Running
	s.identifyMu.Unlock()
	writeJSON(w, 200, map[string]any{"active": ok, "name": name, "nic": s.NIC, "output": s.DMX.OutputRunning() || identifyRunning, "simulation": s.Simulation})
}
func (s *Server) handleStopAllOutput(w http.ResponseWriter, r *http.Request) {
	s.RigCheck.Stop()
	s.DMX.Blackout()
	s.DMX.Stop()
	writeJSON(w, 200, map[string]bool{"stopped": true})
}
func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := s.PatchStore.Get()
	data := workspaceFor(p)
	entries := make([]map[string]any, 0, len(p.Entries))
	counts := map[string]uint16{}
	for _, d := range s.Registry.Devices() {
		if d.HasDeviceInfo {
			counts[d.UID.String()] = d.SubDeviceCount
		}
	}
	for _, e := range p.Entries {
		n, source := patch.PhaseCountFor(e, counts[e.ConfirmedUID])
		entries = append(entries, map[string]any{"id": e.ID, "name": e.Name, "uid": e.ConfirmedUID, "universe": e.Universe, "startAddress": e.StartAddress, "phaseCount": n, "phaseSource": source})
	}
	baselines := make([]map[string]any, 0, len(data.Baselines))
	for _, b := range data.Baselines {
		baselines = append(baselines, map[string]any{"id": b.ID, "name": b.Name, "at": b.At, "issues": len(b.Issues)})
	}
	writeJSON(w, 200, map[string]any{"active": ok, "name": p.Name, "entries": entries, "groups": data.Groups, "presets": data.Presets, "baselines": baselines, "issues": workspaceIssues(p, s.observeFixtures()), "canRehearse": s.OnRehearse != nil})
}

func exactEntries(p patch.Patch, ids []string) ([]patch.Entry, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("select at least one patch entry")
	}
	out := make([]patch.Entry, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		i := p.IndexOf(id)
		if i < 0 || seen[id] {
			return nil, fmt.Errorf("selection contains a missing or duplicate entry; select the group again")
		}
		seen[id] = true
		out = append(out, p.Entries[i])
	}
	return out, nil
}

func (s *Server) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string   `json:"name"`
		ID       string   `json:"id"`
		EntryIDs []string `json:"entryIds"`
		Fault    string   `json:"fault"`
		Confirm  string   `json:"confirm"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, 400, fmt.Errorf("create a show first"))
		return
	}
	data := workspaceFor(p)
	action := r.PathValue("action")
	if len(p.Workspace) > 0 {
		var checked showWorkspace
		if err := json.Unmarshal(p.Workspace, &checked); err != nil {
			writeError(w, 409, fmt.Errorf("saved show tools are damaged; export the show and restore its preceding save before editing"))
			return
		}
	}
	if strings.HasPrefix(action, "delete-") {
		found := false
		switch action {
		case "delete-group":
			for i, item := range data.Groups {
				if item.ID == req.ID {
					data.Groups = append(data.Groups[:i], data.Groups[i+1:]...)
					found = true
					break
				}
			}
		case "delete-preset":
			for i, item := range data.Presets {
				if item.ID == req.ID {
					data.Presets = append(data.Presets[:i], data.Presets[i+1:]...)
					found = true
					break
				}
			}
		case "delete-baseline":
			for i, item := range data.Baselines {
				if item.ID == req.ID {
					data.Baselines = append(data.Baselines[:i], data.Baselines[i+1:]...)
					found = true
					break
				}
			}
		}
		if !found {
			writeError(w, 404, fmt.Errorf("saved item not found"))
			return
		}
		_, err := s.PatchStore.Mutate(func(p *patch.Patch) error { encoded, err := json.Marshal(data); p.Workspace = encoded; return err })
		if err != nil {
			writePatchStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
		return
	}
	if action == "rehearse" {
		if s.OnRehearse == nil {
			writeError(w, 400, fmt.Errorf("rehearsal is unavailable in this session"))
			return
		}
		if req.Confirm != "REHEARSE" {
			writeError(w, 400, fmt.Errorf("confirm REHEARSE to stop live output and open a simulated rig"))
			return
		}
		if req.Fault != "none" && req.Fault != "missing" && req.Fault != "address" && req.Fault != "slow" {
			writeError(w, 400, fmt.Errorf("choose none, missing, address, or slow"))
			return
		}
		s.RigCheck.Stop()
		s.DMX.Blackout()
		s.DMX.Stop()
		port, err := s.OnRehearse(p, req.Fault)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]int{"port": port})
		return
	}
	if action == "load-preset" {
		for _, preset := range data.Presets {
			if preset.ID == req.ID {
				if preset.FadeMS != nil && (*preset.FadeMS < 0 || *preset.FadeMS > 30000) {
					writeError(w, 422, fmt.Errorf("saved fade time must be between 0 and 30000 ms"))
					return
				}
				entries, err := exactEntries(p, preset.EntryIDs)
				if err != nil {
					writeError(w, 409, err)
					return
				}
				s.RigCheck.Stop()
				st, err := s.RigCheck.SetPatternTests(s.withRDMPhaseWeights(entries), preset.Specs, preset.Isolate)
				if err != nil {
					writeRigCheckError(w, err)
					return
				}
				if preset.FadeMS != nil {
					st, _ = s.RigCheck.SetPatternFade(time.Duration(*preset.FadeMS) * time.Millisecond)
				}
				s.setPatternScope(patternScopeFields{ScopeKind: "selection", EntryIDs: preset.EntryIDs}, entries)
				writeJSON(w, 200, s.patternStatusJSON(st))
				return
			}
		}
		writeError(w, 404, fmt.Errorf("preset not found"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 80 {
		writeError(w, 400, fmt.Errorf("name must be 1–80 characters"))
		return
	}
	id := fmt.Sprintf("w-%d", time.Now().UnixNano())
	switch action {
	case "save-group":
		if _, err := exactEntries(p, req.EntryIDs); err != nil {
			writeError(w, 400, err)
			return
		}
		if len(data.Groups) >= 100 {
			writeError(w, 400, fmt.Errorf("100 group limit reached"))
			return
		}
		data.Groups = append(data.Groups, savedGroup{ID: id, Name: name, EntryIDs: req.EntryIDs})
	case "save-preset":
		ids, specs, isolate, fadeMS := s.RigCheck.SavedPattern()
		if len(specs) == 0 {
			writeError(w, 400, fmt.Errorf("select function tests before saving a preset"))
			return
		}
		if _, err := exactEntries(p, ids); err != nil {
			writeError(w, 409, err)
			return
		}
		if len(data.Presets) >= 100 {
			writeError(w, 400, fmt.Errorf("100 preset limit reached"))
			return
		}
		data.Presets = append(data.Presets, savedTestPreset{savedGroup: savedGroup{ID: id, Name: name, EntryIDs: ids}, Specs: specs, Isolate: isolate, FadeMS: &fadeMS})
	case "snapshot":
		if len(data.Baselines) >= 10 {
			writeError(w, 400, fmt.Errorf("10 baseline limit reached; export and delete an old baseline first"))
			return
		}
		devices := s.observeFixtures()
		digests := map[string]string{}
		entries := append([]patch.Entry{}, p.Entries...)
		for i, e := range entries {
			digests[e.ID] = profileDigest(e)
			entries[i].ChannelFunctions = nil
		}
		data.Baselines = append(data.Baselines, rigBaseline{ID: id, Name: name, At: time.Now(), Entries: entries, ProfileDigests: digests, Devices: devices, Issues: workspaceIssues(p, devices)})
	default:
		writeError(w, 404, fmt.Errorf("unknown workspace action"))
		return
	}
	_, err := s.PatchStore.Mutate(func(p *patch.Patch) error {
		encoded, err := json.Marshal(data)
		if err != nil {
			return err
		}
		p.Workspace = encoded
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"id": id})
}

func profileDigest(e patch.Entry) string {
	data, _ := json.Marshal(e.ChannelFunctions)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func baselineChanges(b rigBaseline, p patch.Patch, devices []observedFixture) []string {
	changes := make([]string, 0)
	oldEntries := map[string]patch.Entry{}
	for _, e := range b.Entries {
		oldEntries[e.ID] = e
	}
	for _, e := range p.Entries {
		old, ok := oldEntries[e.ID]
		name := e.Name
		if name == "" {
			name = e.ID
		}
		if !ok {
			changes = append(changes, name+": added to patch")
		} else {
			// Ignore read timestamps, compare intended configuration and identity.
			profileChanged := !reflect.DeepEqual(old.ChannelFunctions, e.ChannelFunctions)
			if digest, ok := b.ProfileDigests[e.ID]; ok {
				profileChanged = digest != profileDigest(e)
			}
			if old.Universe != e.Universe || old.StartAddress != e.StartAddress || old.Mode != e.Mode || old.Footprint != e.Footprint || old.ConfirmedUID != e.ConfirmedUID || profileChanged || !reflect.DeepEqual(old.Intended, e.Intended) || old.PhaseCount != e.PhaseCount {
				changes = append(changes, name+": intended configuration, profile, phase count or commitment changed")
			}
		}
		delete(oldEntries, e.ID)
	}
	for _, e := range oldEntries {
		changes = append(changes, e.Name+": removed from patch")
	}
	oldDevices := map[string]observedFixture{}
	for _, d := range b.Devices {
		oldDevices[d.UID] = d
	}
	for _, d := range devices {
		old, ok := oldDevices[d.UID]
		if !ok {
			changes = append(changes, d.UID+": newly observed")
		} else {
			old.LastSeen = time.Time{}
			current := d
			current.LastSeen = time.Time{}
			if !reflect.DeepEqual(old, current) {
				changes = append(changes, d.UID+": observed configuration, reachability or read coverage changed")
			}
		}
		delete(oldDevices, d.UID)
	}
	for uid := range oldDevices {
		changes = append(changes, uid+": no longer in discovery cache")
	}
	sort.Strings(changes)
	return changes
}

func (s *Server) handleBaselineReport(w http.ResponseWriter, r *http.Request) {
	p, _ := s.PatchStore.Get()
	data := workspaceFor(p)
	for _, b := range data.Baselines {
		if b.ID == r.PathValue("id") {
			devices := s.observeFixtures()
			changes := baselineChanges(b, p, devices)
			issues := workspaceIssues(p, devices)
			if r.URL.Query().Get("format") == "txt" {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Content-Disposition", `attachment; filename="benny512-rig-report.txt"`)
				fmt.Fprintf(w, "Benny512 rig report\nShow: %s\nBaseline: %s (%s)\nReport: %s\n\nCACHE-ONLY evidence; not a fresh discovery or a physical acceptance test.\nBaseline recorded %d issues.\n\nChanges (%d):\n", p.Name, b.Name, b.At.Format(time.RFC3339), time.Now().Format(time.RFC3339), len(b.Issues), len(changes))
				for _, c := range changes {
					fmt.Fprintln(w, "- "+c)
				}
				fmt.Fprintf(w, "\nOpen issues (%d):\n", len(issues))
				for _, i := range issues {
					fmt.Fprintf(w, "- %s %s: %s\n", i.Name, i.EntryID, i.Message)
				}
				fmt.Fprintln(w, "\nObservation timestamps:")
				for _, d := range devices {
					fmt.Fprintf(w, "- %s: %s\n", d.UID, d.LastSeen.Format(time.RFC3339))
				}
				return
			}
			writeJSON(w, 200, map[string]any{"baseline": b.Name, "at": b.At, "changes": changes, "issues": issues, "evidence": "cached observations; perform discovery/readback for fresh evidence"})
			return
		}
	}
	writeError(w, 404, fmt.Errorf("baseline not found"))
}
