package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"benny512/internal/patch"
)

// This file is the HTTP surface of the Console-lite programmer (chunk C4a,
// internal/patch/programmer.go). One programmer per station; every browser
// controls the same one (owner decision 2026-10-07).
//
//	GET  /api/programmer            -> patch.ProgView (selection, attributes by group)
//	GET  /api/programmer/fixtures   -> {revision, fixtures: [patch.FixtureModel]}
//	POST /api/programmer/select     {action, targets | group | layer}
//	POST /api/programmer/set        {targets?, attribute, function|functionName|functionIndex?,
//	                                 dmx | fraction | physical | set | slot}
//	POST /api/programmer/clear      {scope: "all"|"selection", group?}
//	POST /api/programmer/raw        {entryId, writes: [{offset, value | release}]}
//
// Every route goes through the show guard (X-Benny-Show), like /api/patch.
// Every response carries the programmer revision in X-Benny-Programmer; a
// POST that sends X-Benny-Programmer is refused with 409 when it is not the
// current revision (optional, like the show token). Every successful
// mutation broadcasts {"type":"programmer","revision":N} on the WebSocket so
// every connected browser refreshes.
//
// LIFECYCLE. The programmer follows the active show: after every non-GET
// show-guarded request it is re-synced — on a show boundary (switch, new,
// reset this show, recover, full reset) it is cleared; after a structural
// edit, fixtures that left the patch leave the selection and the values, and
// a fixture whose profile changed loses its values. See syncProgrammer.

const programmerRevisionHeader = "X-Benny-Programmer"

// syncProgrammer brings the programmer (and the engine's base source) up to
// date with the active show. clear empties it (a show boundary).
func (s *Server) syncProgrammer(clear bool) {
	if !clear && s.Programmer.SyncedTo(s.PatchStore.Token()) {
		return
	}
	p, _, tok := s.PatchStore.GetWithToken()
	changed := s.Programmer.Sync(tok, p.Entries, clear)
	// Stored groups and presets are shown with the programmer (C4b): a
	// change to them, from any route, makes every browser re-read it.
	ws := workspaceFor(p)
	b, _ := json.Marshal(struct {
		G []savedGroup
		P []programmerPreset
	}{ws.Groups, ws.ProgrammerPresets})
	sum := sha256.Sum256(b)
	if s.Programmer.NoteWorkspace(hex.EncodeToString(sum[:])) {
		changed = true
	}
	if changed {
		s.broadcastProgrammer()
	}
}

func (s *Server) broadcastProgrammer() {
	rev := s.Programmer.Revision()
	s.hub.broadcast(wsMessage{Type: "programmer", At: time.Now(), Revision: &rev})
	// Tests on the programmer selection follow it (C5, tests.go).
	s.refreshTests()
}

// programmerExpected reads the optional X-Benny-Programmer request header.
func programmerExpected(r *http.Request) (*uint64, error) {
	h := r.Header.Get(programmerRevisionHeader)
	if h == "" {
		return nil, nil
	}
	v, err := strconv.ParseUint(h, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be the programmer revision number this browser last saw.", programmerRevisionHeader)
	}
	return &v, nil
}

func (s *Server) writeProgrammerJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(programmerRevisionHeader, strconv.FormatUint(s.Programmer.Revision(), 10))
	writeJSON(w, status, v)
}

func (s *Server) writeProgrammerError(w http.ResponseWriter, err error) {
	var nothing patch.ProgNothingApplied
	var bad patch.ProgrammerRequestError
	var recall patch.ProgRecallNothing
	switch {
	case errors.As(err, &recall):
		s.writeProgrammerJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "targets": recall.Result.Targets})
	case errors.Is(err, patch.ErrProgrammerStale):
		s.writeProgrammerJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.As(err, &nothing):
		s.writeProgrammerJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "skipped": nothing.Result.Skipped})
	case errors.As(err, &bad):
		s.writeProgrammerJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		s.writeProgrammerJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// programmerViewJSON is GET /api/programmer: the programmer's read model
// plus the show's stored groups (with members) and preset summaries.
type programmerViewJSON struct {
	patch.ProgView
	StoredGroups []savedGroup          `json:"storedGroups"`
	Presets      []programmerPresetSum `json:"presets"`
}

// programmerPresetSum is a stored preset without its values.
type programmerPresetSum struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Family    string    `json:"family"`
	Fixtures  int       `json:"fixtures"`
	Channels  int       `json:"channels"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *Server) programmerView() programmerViewJSON {
	p, _ := s.PatchStore.Get()
	ws := workspaceFor(p)
	out := programmerViewJSON{ProgView: s.Programmer.View(), StoredGroups: ws.Groups, Presets: make([]programmerPresetSum, 0, len(ws.ProgrammerPresets))}
	for _, pr := range ws.ProgrammerPresets {
		ids := map[string]bool{}
		for _, v := range pr.Values {
			ids[v.EntryID] = true
		}
		out.Presets = append(out.Presets, programmerPresetSum{ID: pr.ID, Name: pr.Name, Family: pr.Family, Fixtures: len(ids), Channels: len(pr.Values), UpdatedAt: pr.UpdatedAt})
	}
	return out
}

func (s *Server) handleGetProgrammer(w http.ResponseWriter, r *http.Request) {
	s.syncProgrammer(false)
	s.writeProgrammerJSON(w, http.StatusOK, s.programmerView())
}

type programmerFixturesResponse struct {
	Revision uint64               `json:"revision"`
	Fixtures []patch.FixtureModel `json:"fixtures"`
}

func (s *Server) handleGetProgrammerFixtures(w http.ResponseWriter, r *http.Request) {
	s.syncProgrammer(false)
	s.writeProgrammerJSON(w, http.StatusOK, programmerFixturesResponse{Revision: s.Programmer.Revision(), Fixtures: s.Programmer.Fixtures()})
}

// --- select -----------------------------------------------------------------------

type programmerSelectRequest struct {
	Action  string             `json:"action"`
	Targets []patch.ProgTarget `json:"targets"`
	// Group is a saved group's id, Layer a layout layer's id: their fixtures
	// become the targets (whole fixtures, in the group's order / the layer's
	// reading order).
	Group string `json:"group"`
	Layer string `json:"layer"`
}

// programmerIgnored is a fixture a group or layer named that is no longer
// in the patch: reported, never silently dropped.
type programmerIgnored struct {
	EntryID string `json:"entryId"`
	Cell    string `json:"cell"`
	Reason  string `json:"reason"`
}

type programmerSelectResponse struct {
	programmerViewJSON
	Ignored []programmerIgnored `json:"ignored"`
}

func (s *Server) handleProgrammerSelect(w http.ResponseWriter, r *http.Request) {
	var req programmerSelectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	expected, err := programmerExpected(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sources := 0
	for _, given := range []bool{req.Targets != nil, req.Group != "", req.Layer != ""} {
		if given {
			sources++
		}
	}
	if sources > 1 {
		writeError(w, http.StatusBadRequest, errors.New("Select by targets, a group or a layer — one at a time."))
		return
	}
	s.syncProgrammer(false)
	ignored := make([]programmerIgnored, 0)
	targets := req.Targets
	if req.Group != "" || req.Layer != "" {
		p, _ := s.PatchStore.Get()
		ws, err := strictWorkspace(p)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		if req.Group != "" {
			targets, ignored, err = groupTargets(p, ws, req.Group, s.Programmer.ValidTarget)
		} else {
			targets, ignored, err = layerTargets(p, ws, req.Layer, s.Programmer.ValidTarget)
		}
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
	}
	if _, err := s.Programmer.Select(req.Action, targets, expected); err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	s.broadcastProgrammer()
	s.writeProgrammerJSON(w, http.StatusOK, programmerSelectResponse{programmerViewJSON: s.programmerView(), Ignored: ignored})
}

// groupTargets: the group's members — fixtures and cells — in stored order.
// A member whose fixture left the patch, or whose cell the fixture's
// current profile no longer has, is reported, not selected.
func groupTargets(p patch.Patch, ws showWorkspace, id string, valid func(patch.ProgTarget) bool) ([]patch.ProgTarget, []programmerIgnored, error) {
	for _, g := range ws.Groups {
		if g.ID != id {
			continue
		}
		out, ignored := make([]patch.ProgTarget, 0, len(g.Members)), make([]programmerIgnored, 0)
		for _, m := range g.Members {
			switch {
			case p.IndexOf(m.EntryID) < 0:
				ignored = append(ignored, programmerIgnored{EntryID: m.EntryID, Cell: m.Cell, Reason: "This fixture in the group is no longer in the patch."})
			case !valid(m):
				ignored = append(ignored, programmerIgnored{EntryID: m.EntryID, Cell: m.Cell, Reason: "This cell is not in the fixture's current profile."})
			default:
				out = append(out, m)
			}
		}
		return out, ignored, nil
	}
	return nil, nil, errors.New("There is no saved group with that id; refresh the groups.")
}

// layerTargets: every fixture placed on the layer, in reading order (row,
// then column, then stacking order) — a group placed there contributes its
// members in the group's order. Objects are never selectable (layout.go).
// The Unplaced layer includes its automatic placements.
func layerTargets(p patch.Patch, ws showWorkspace, id string, valid func(patch.ProgTarget) bool) ([]patch.ProgTarget, []programmerIgnored, error) {
	l := ws.Layout
	normalizeLayout(&l)
	if findLayer(&l, id) < 0 {
		return nil, nil, errors.New("There is no layout layer with that id; refresh the layout.")
	}
	items := make([]layoutItem, 0)
	for _, it := range l.Items {
		if it.Layer == id {
			items = append(items, it)
		}
	}
	if id == unplacedLayerID {
		virt, _ := unplacedItems(p, &l)
		items = append(items, virt...)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Row != b.Row {
			return a.Row < b.Row
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Order < b.Order
	})
	groups := map[string]savedGroup{}
	for _, g := range ws.Groups {
		groups[g.ID] = g
	}
	out, ignored := make([]patch.ProgTarget, 0), make([]programmerIgnored, 0)
	seen := map[patch.ProgTarget]bool{}
	add := func(t patch.ProgTarget) {
		if seen[t] {
			return
		}
		seen[t] = true
		switch {
		case p.IndexOf(t.EntryID) < 0:
			ignored = append(ignored, programmerIgnored{EntryID: t.EntryID, Cell: t.Cell, Reason: "This fixture placed on the layer is no longer in the patch."})
		case !valid(t):
			ignored = append(ignored, programmerIgnored{EntryID: t.EntryID, Cell: t.Cell, Reason: "This cell is not in the fixture's current profile."})
		default:
			out = append(out, t)
		}
	}
	for _, it := range items {
		switch it.Kind {
		case "entry":
			add(patch.ProgTarget{EntryID: it.Ref})
		case "group":
			for _, m := range groups[it.Ref].Members {
				add(m)
			}
		}
	}
	return out, ignored, nil
}

// --- set / clear / raw --------------------------------------------------------------

type programmerSetRequest struct {
	Targets       []patch.ProgTarget `json:"targets"`
	Attribute     string             `json:"attribute"`
	Function      string             `json:"function"`
	FunctionName  string             `json:"functionName"`
	FunctionIndex *int               `json:"functionIndex"`
	DMX           *float64           `json:"dmx"`
	Fraction      *float64           `json:"fraction"`
	Physical      *float64           `json:"physical"`
	Set           *string            `json:"set"`
	Slot          *int               `json:"slot"`
}

func (s *Server) handleProgrammerSet(w http.ResponseWriter, r *http.Request) {
	var req programmerSetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	expected, err := programmerExpected(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.syncProgrammer(false)
	res, err := s.Programmer.Set(patch.ProgSetRequest{
		Targets: req.Targets, Attribute: req.Attribute, Function: req.Function, FunctionName: req.FunctionName,
		FunctionIndex: req.FunctionIndex, DMX: req.DMX, Fraction: req.Fraction, Physical: req.Physical, Set: req.Set, Slot: req.Slot,
	}, expected)
	if err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	s.broadcastProgrammer()
	s.writeProgrammerJSON(w, http.StatusOK, res)
}

type programmerClearRequest struct {
	Scope string `json:"scope"`
	Group string `json:"group"`
}

type programmerClearResponse struct {
	Revision uint64           `json:"revision"`
	Output   patch.ProgOutput `json:"output"`
	Released int              `json:"released"`
}

func (s *Server) handleProgrammerClear(w http.ResponseWriter, r *http.Request) {
	var req programmerClearRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	expected, err := programmerExpected(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.syncProgrammer(false)
	n, rev, err := s.Programmer.Clear(req.Scope, patch.AttributeGroup(req.Group), expected)
	if err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	s.broadcastProgrammer()
	s.writeProgrammerJSON(w, http.StatusOK, programmerClearResponse{Revision: rev, Output: s.Programmer.Output(), Released: n})
}

type programmerRawWrite struct {
	Offset  uint16 `json:"offset"`
	Value   *int   `json:"value"`
	Release bool   `json:"release"`
}

type programmerRawRequest struct {
	EntryID string               `json:"entryId"`
	Writes  []programmerRawWrite `json:"writes"`
}

type programmerRawResponse struct {
	Revision uint64           `json:"revision"`
	Output   patch.ProgOutput `json:"output"`
}

func (s *Server) handleProgrammerRaw(w http.ResponseWriter, r *http.Request) {
	var req programmerRawRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	expected, err := programmerExpected(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writes := make([]patch.ProgRawWrite, 0, len(req.Writes))
	for _, wr := range req.Writes {
		if (wr.Value == nil) == !wr.Release {
			writeError(w, http.StatusBadRequest, errors.New("Each raw write gives either a value or release: true."))
			return
		}
		writes = append(writes, patch.ProgRawWrite{Offset: wr.Offset, Value: wr.Value})
	}
	s.syncProgrammer(false)
	rev, err := s.Programmer.Raw(req.EntryID, writes, expected)
	if err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	s.broadcastProgrammer()
	s.writeProgrammerJSON(w, http.StatusOK, programmerRawResponse{Revision: rev, Output: s.Programmer.Output()})
}
