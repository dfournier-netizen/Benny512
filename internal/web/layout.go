// This file implements the per-show Console-lite layout (Console-lite C2):
// a plain 2D grid with Z layers on which patch entries and saved groups are
// placed, either by hand or derived from MVR world locations
// (patch.Location).
//
//	GET  /api/patch/layout            -> layoutResponse
//	POST /api/patch/layout/{action}   -> layoutResponse with "result"
//	     derive | place | move | remove |
//	     layer-create | layer-rename | layer-reorder | layer-delete
//
// STORAGE: the layout lives inside the show's workspace JSON
// (patch.Patch.Workspace, beside saved groups), not a new top-level Patch
// field. Groups are what a group placement refers to, and keeping both in one
// blob means a group delete and its placement removal are one atomic write,
// and the layout inherits exactly the semantics groups already have: per-show
// isolation on switch, the preceding-save .bak, Recover, Reset this show, the
// damaged-workspace refusal, and a fresh import/new show starting empty. A
// Patch field would share the file too, but would need its own copy of each
// of those paths.
//
// Layout edits change no DMX, so there is no Apply-to-confirm; they go through
// the show guard (X-Benny-Show token) like every other /api/patch mutation.
package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"benny512/internal/patch"
)

// Limits. Items are additionally bounded by "each entry and each group at
// most once", i.e. at most entries + groups.
const (
	layoutMaxLayers = 32
	layoutMaxSpan   = 64  // W and H, in cells
	layoutMaxCell   = 999 // Col/Row lie in [-999, 999]
	layoutNameMax   = 80

	// Derive defaults when neither the request nor an earlier derive gives
	// one. 500 mm is a common pipe spacing between fixtures; 1 m separates a
	// floor row, boom heights and a trim. Both are configurable per request.
	layoutDefaultCellMM = 500
	layoutDefaultZGapMM = 1000
	layoutMinCellMM     = 50
	layoutMaxCellMM     = 10000
	layoutMinZGapMM     = 10
	layoutMaxZGapMM     = 100000
	// How far (Chebyshev rings) derive searches for a free cell.
	layoutSearchRings = 64
)

// showLayout is the stored layout. Derived is false until the first derive;
// until then CellMM/ZGapMM/OriginXMM/OriginYMM are 0 and mean nothing.
// OriginXMM/OriginYMM are the world X/Y (mm) that cell 0,0 stands for, fixed
// by the first derive so a later derive lands new fixtures on the same grid
// (an extension of the brief's shape, for that reason).
type showLayout struct {
	Derived   bool          `json:"derived"`
	CellMM    float64       `json:"cellMm"`
	ZGapMM    float64       `json:"zGapMm"`
	OriginXMM float64       `json:"originXMm"`
	OriginYMM float64       `json:"originYMm"`
	Layers    []layoutLayer `json:"layers"`
	Items     []layoutItem  `json:"items"`
}

// layoutLayer is one Z layer. ZKnown false = a hand-made layer with no
// height range (ZMin/ZMax 0, meaningless); derived layers carry the Z range
// (mm) of the fixtures that formed them. Order is the display order, 0 first,
// kept contiguous.
type layoutLayer struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	ZKnown bool    `json:"zKnown"`
	ZMin   float64 `json:"zMin"`
	ZMax   float64 `json:"zMax"`
	Order  int     `json:"order"`
}

// layoutItem is one placement: Kind "entry" (Ref = patch entry ID) or "group"
// (Ref = saved group ID) on Layer at Col,Row spanning W x H cells. Rot (0, 90,
// 180, 270) turns only the glyph. A multi-cell fixture is ONE item; its cells
// are the entry's own business (GET reports cellCount), so selecting the
// parent or a sub-fixture is never blocked by the layout.
type layoutItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Layer string `json:"layer"`
	Col   int    `json:"col"`
	Row   int    `json:"row"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Rot   int    `json:"rot"`
}

func normalizeLayout(l *showLayout) {
	if l.Layers == nil {
		l.Layers = make([]layoutLayer, 0)
	}
	if l.Items == nil {
		l.Items = make([]layoutItem, 0)
	}
	sort.SliceStable(l.Layers, func(i, j int) bool { return l.Layers[i].Order < l.Layers[j].Order })
}

var layoutIDCounter uint64

func newLayoutID(prefix string) string {
	return fmt.Sprintf("%s%d-%d", prefix, time.Now().UnixNano(), atomic.AddUint64(&layoutIDCounter, 1))
}

// layoutError carries an HTTP status out of a PatchStore.Mutate callback.
type layoutError struct {
	status int
	msg    string
}

func (e layoutError) Error() string { return e.msg }

func layoutErr(status int, format string, a ...any) error {
	return layoutError{status, fmt.Sprintf(format, a...)}
}

var errWorkspaceDamaged = layoutError{http.StatusConflict, "The saved show tools are damaged; export the show and restore its preceding save before editing the layout."}

// strictWorkspace parses p's workspace, refusing (rather than silently
// rewriting) one that does not parse — the same rule handleWorkspaceAction
// applies to groups.
func strictWorkspace(p patch.Patch) (showWorkspace, error) {
	if len(p.Workspace) > 0 {
		var checked showWorkspace
		if err := json.Unmarshal(p.Workspace, &checked); err != nil {
			return showWorkspace{}, errWorkspaceDamaged
		}
	}
	return workspaceFor(p), nil
}

// dropLayoutRefs removes every placement of (kind, ref) from p's workspace,
// in place. A workspace that does not parse is left alone (its layout cannot
// be edited safely); GET reports the resulting stale reference instead.
func dropLayoutRefs(p *patch.Patch, kind, ref string) {
	if len(p.Workspace) == 0 {
		return
	}
	var checked showWorkspace
	if json.Unmarshal(p.Workspace, &checked) != nil {
		return
	}
	ws := workspaceFor(*p)
	kept := make([]layoutItem, 0, len(ws.Layout.Items))
	for _, it := range ws.Layout.Items {
		if it.Kind != kind || it.Ref != ref {
			kept = append(kept, it)
		}
	}
	if len(kept) == len(ws.Layout.Items) {
		return
	}
	ws.Layout.Items = kept
	if b, err := json.Marshal(ws); err == nil {
		p.Workspace = b
	}
}

// --- GET ----------------------------------------------------------------

type layoutEntryView struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	FixtureType   string         `json:"fixtureType"`
	Mode          string         `json:"mode"`
	FixtureNumber string         `json:"fixtureNumber"`
	Universe      uint16         `json:"universe"`
	StartAddress  uint16         `json:"startAddress"`
	Footprint     uint16         `json:"footprint"`
	Location      patch.Location `json:"location"`
	// CellCount is patch.GeometryCellEstimate (GDTF geometry instances);
	// CellCountKnown false = no GDTF geometry to count, CellCount then 0.
	CellCount      int  `json:"cellCount"`
	CellCountKnown bool `json:"cellCountKnown"`
	Placed         bool `json:"placed"`
}

type layoutUnplaced struct {
	EntryID string `json:"entryId"`
	// Reason: "no-location" (nothing to derive from; place by hand) or
	// "not-placed" (has a location, not on the grid yet).
	Reason string `json:"reason"`
}

type layoutStale struct {
	ItemID  string `json:"itemId"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Problem string `json:"problem"`
}

type layoutLimits struct {
	MaxLayers int `json:"maxLayers"`
	MaxItems  int `json:"maxItems"`
	MaxSpan   int `json:"maxSpan"`
	MinCell   int `json:"minCell"`
	MaxCell   int `json:"maxCell"`
}

type layoutResponse struct {
	Active   bool              `json:"active"`
	Name     string            `json:"name"`
	Layout   showLayout        `json:"layout"`
	Entries  []layoutEntryView `json:"entries"`
	Groups   []savedGroup      `json:"groups"`
	Unplaced []layoutUnplaced  `json:"unplaced"`
	Stale    []layoutStale     `json:"stale"`
	Limits   layoutLimits      `json:"limits"`
	Result   any               `json:"result,omitempty"`
}

func buildLayoutResponse(p patch.Patch, active bool, ws showWorkspace) layoutResponse {
	l := ws.Layout
	normalizeLayout(&l)
	resp := layoutResponse{
		Active: active, Name: p.Name, Layout: l,
		Entries:  make([]layoutEntryView, 0, len(p.Entries)),
		Groups:   ws.Groups,
		Unplaced: make([]layoutUnplaced, 0),
		Stale:    make([]layoutStale, 0),
		Limits:   layoutLimits{layoutMaxLayers, len(p.Entries) + len(ws.Groups), layoutMaxSpan, -layoutMaxCell, layoutMaxCell},
	}
	placed := map[string]bool{}
	for _, it := range l.Items {
		if it.Kind == "entry" {
			placed[it.Ref] = true
		}
	}
	for _, e := range p.Entries {
		n := patch.GeometryCellEstimate(e)
		resp.Entries = append(resp.Entries, layoutEntryView{
			ID: e.ID, Name: e.Name, FixtureType: e.FixtureType, Mode: e.Mode, FixtureNumber: e.FixtureNumber,
			Universe: e.Universe, StartAddress: e.StartAddress, Footprint: e.Footprint, Location: e.Location,
			CellCount: n, CellCountKnown: n > 0, Placed: placed[e.ID],
		})
		if !placed[e.ID] {
			reason := "not-placed"
			if !e.Location.Known {
				reason = "no-location"
			}
			resp.Unplaced = append(resp.Unplaced, layoutUnplaced{EntryID: e.ID, Reason: reason})
		}
	}
	groups := map[string]bool{}
	for _, g := range ws.Groups {
		groups[g.ID] = true
	}
	layers := map[string]bool{}
	for _, ly := range l.Layers {
		layers[ly.ID] = true
	}
	for _, it := range l.Items {
		problem := ""
		switch {
		case it.Kind == "entry" && p.IndexOf(it.Ref) < 0:
			problem = "The patch entry this item places is no longer in the patch."
		case it.Kind == "group" && !groups[it.Ref]:
			problem = "The saved group this item places no longer exists."
		case it.Kind != "entry" && it.Kind != "group":
			problem = "The item is neither an entry nor a group placement."
		case !layers[it.Layer]:
			problem = "The item's layer no longer exists."
		}
		if problem != "" {
			resp.Stale = append(resp.Stale, layoutStale{ItemID: it.ID, Kind: it.Kind, Ref: it.Ref, Problem: problem})
		}
	}
	return resp
}

func (s *Server) handleGetLayout(w http.ResponseWriter, r *http.Request) {
	p, ok := s.PatchStore.Get()
	ws, err := strictWorkspace(p)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, buildLayoutResponse(p, ok, ws))
}

// --- POST -------------------------------------------------------------------

type layoutDeriveRequest struct {
	CellMM  *float64 `json:"cellMm"`
	ZGapMM  *float64 `json:"zGapMm"`
	Replace bool     `json:"replace"`
	Confirm string   `json:"confirm"`
}

type layoutPlaceRequest struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Layer string `json:"layer"`
	Col   int    `json:"col"`
	Row   int    `json:"row"`
	W     *int   `json:"w"`
	H     *int   `json:"h"`
	Rot   *int   `json:"rot"`
}

type layoutMoveRequest struct {
	ID    string `json:"id"`
	Layer string `json:"layer"` // "" = stay on the current layer
	Col   *int   `json:"col"`
	Row   *int   `json:"row"`
	W     *int   `json:"w"`
	H     *int   `json:"h"`
	Rot   *int   `json:"rot"`
}

type layoutIDRequest struct {
	ID string `json:"id"`
}

type layoutLayerCreateRequest struct {
	Name   string  `json:"name"`
	ZKnown bool    `json:"zKnown"`
	ZMin   float64 `json:"zMin"`
	ZMax   float64 `json:"zMax"`
}

type layoutLayerRenameRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type layoutLayerReorderRequest struct {
	Order []string `json:"order"`
}

type layoutLayerDeleteRequest struct {
	ID          string `json:"id"`
	RemoveItems bool   `json:"removeItems"`
}

// layoutDerivePlaced / layoutDeriveResult: what a derive did. Displaced =
// the target cell was taken and the nearest free cell was used.
type layoutDerivePlaced struct {
	EntryID   string `json:"entryId"`
	ItemID    string `json:"itemId"`
	Layer     string `json:"layer"`
	Col       int    `json:"col"`
	Row       int    `json:"row"`
	Displaced bool   `json:"displaced"`
}

type layoutDeriveResult struct {
	Placed        []layoutDerivePlaced `json:"placed"`
	NotPlaced     []layoutUnplaced     `json:"notPlaced"`
	LayersCreated int                  `json:"layersCreated"`
	Kept          int                  `json:"kept"`
	Discarded     int                  `json:"discarded"`
}

func decodeStrict(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return layoutErr(http.StatusBadRequest, "The request body could not be read (%v).", err)
	}
	return nil
}

func (s *Server) handleLayoutAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	known := map[string]bool{"derive": true, "place": true, "move": true, "remove": true,
		"layer-create": true, "layer-rename": true, "layer-reorder": true, "layer-delete": true}
	if !known[action] {
		writeError(w, http.StatusNotFound, fmt.Errorf("Unknown layout action %q.", action))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, ok := s.PatchStore.Get(); !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Create a show before editing its layout."))
		return
	}
	var result any
	updated, err := s.PatchStore.Mutate(func(p *patch.Patch) error {
		ws, err := strictWorkspace(*p)
		if err != nil {
			return err
		}
		normalizeLayout(&ws.Layout)
		result, err = applyLayoutAction(action, body, p, &ws)
		if err != nil {
			return err
		}
		renumberLayers(&ws.Layout)
		encoded, err := json.Marshal(ws)
		if err != nil {
			return err
		}
		p.Workspace = encoded
		return nil
	})
	var le layoutError
	if errors.As(err, &le) {
		writeError(w, le.status, le)
		return
	}
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	ws, _ := strictWorkspace(updated)
	resp := buildLayoutResponse(updated, true, ws)
	resp.Result = result
	writeJSON(w, http.StatusOK, resp)
}

func renumberLayers(l *showLayout) {
	sort.SliceStable(l.Layers, func(i, j int) bool { return l.Layers[i].Order < l.Layers[j].Order })
	for i := range l.Layers {
		l.Layers[i].Order = i
	}
}

func layoutName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || len([]rune(n)) > layoutNameMax {
		return "", layoutErr(http.StatusBadRequest, "A layer name must be 1 to %d characters.", layoutNameMax)
	}
	return n, nil
}

func findLayer(l *showLayout, id string) int {
	for i, ly := range l.Layers {
		if ly.ID == id {
			return i
		}
	}
	return -1
}

func findItem(l *showLayout, id string) int {
	for i, it := range l.Items {
		if it.ID == id {
			return i
		}
	}
	return -1
}

// checkGeometry validates an item's cell rectangle and glyph rotation.
func checkGeometry(col, row, w, h, rot int) error {
	if w < 1 || w > layoutMaxSpan || h < 1 || h > layoutMaxSpan {
		return layoutErr(http.StatusBadRequest, "Width and height must each be 1 to %d cells.", layoutMaxSpan)
	}
	if rot != 0 && rot != 90 && rot != 180 && rot != 270 {
		return layoutErr(http.StatusBadRequest, "Rotation must be 0, 90, 180 or 270 degrees.")
	}
	if col < -layoutMaxCell || row < -layoutMaxCell || col+w-1 > layoutMaxCell || row+h-1 > layoutMaxCell {
		return layoutErr(http.StatusBadRequest, "The item must lie within columns and rows %d to %d.", -layoutMaxCell, layoutMaxCell)
	}
	return nil
}

// overlapping returns the item on layer that shares a cell with the
// rectangle, ignoring the item with ID skip, or nil.
func overlapping(l *showLayout, layer string, col, row, w, h int, skip string) *layoutItem {
	for i := range l.Items {
		it := &l.Items[i]
		if it.ID == skip || it.Layer != layer {
			continue
		}
		if col < it.Col+it.W && it.Col < col+w && row < it.Row+it.H && it.Row < row+h {
			return it
		}
	}
	return nil
}

func itemLabel(p *patch.Patch, ws *showWorkspace, it *layoutItem) string {
	if it.Kind == "entry" {
		if i := p.IndexOf(it.Ref); i >= 0 && p.Entries[i].Name != "" {
			return fmt.Sprintf("fixture %q", p.Entries[i].Name)
		}
		return "a fixture"
	}
	for _, g := range ws.Groups {
		if g.ID == it.Ref {
			return fmt.Sprintf("group %q", g.Name)
		}
	}
	return "a group"
}

func orDefault(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

func applyLayoutAction(action string, body []byte, p *patch.Patch, ws *showWorkspace) (any, error) {
	l := &ws.Layout
	switch action {
	case "derive":
		var req layoutDeriveRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		return deriveLayout(req, p, l)

	case "place":
		var req layoutPlaceRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		switch req.Kind {
		case "entry":
			if p.IndexOf(req.Ref) < 0 {
				return nil, layoutErr(http.StatusNotFound, "That patch entry is not in this show.")
			}
		case "group":
			found := false
			for _, g := range ws.Groups {
				found = found || g.ID == req.Ref
			}
			if !found {
				return nil, layoutErr(http.StatusNotFound, "That saved group is not in this show.")
			}
		default:
			return nil, layoutErr(http.StatusBadRequest, "Kind must be entry or group.")
		}
		if findLayer(l, req.Layer) < 0 {
			return nil, layoutErr(http.StatusNotFound, "That layer is not in this layout.")
		}
		it := layoutItem{ID: newLayoutID("pl"), Kind: req.Kind, Ref: req.Ref, Layer: req.Layer, Col: req.Col, Row: req.Row,
			W: orDefault(req.W, 1), H: orDefault(req.H, 1), Rot: orDefault(req.Rot, 0)}
		if err := checkGeometry(it.Col, it.Row, it.W, it.H, it.Rot); err != nil {
			return nil, err
		}
		for i := range l.Items {
			if l.Items[i].Kind == it.Kind && l.Items[i].Ref == it.Ref {
				return nil, layoutErr(http.StatusConflict, "This %s is already on the layout; move it instead.", it.Kind)
			}
		}
		if len(l.Items) >= len(p.Entries)+len(ws.Groups) {
			return nil, layoutErr(http.StatusBadRequest, "The layout already holds as many items as the show has fixtures and groups.")
		}
		if o := overlapping(l, it.Layer, it.Col, it.Row, it.W, it.H, ""); o != nil {
			return nil, layoutErr(http.StatusConflict, "Those cells are taken by %s on this layer.", itemLabel(p, ws, o))
		}
		l.Items = append(l.Items, it)
		return map[string]string{"id": it.ID}, nil

	case "move":
		var req layoutMoveRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i := findItem(l, req.ID)
		if i < 0 {
			return nil, layoutErr(http.StatusNotFound, "That item is not on the layout.")
		}
		it := l.Items[i]
		if req.Layer != "" {
			if findLayer(l, req.Layer) < 0 {
				return nil, layoutErr(http.StatusNotFound, "That layer is not in this layout.")
			}
			it.Layer = req.Layer
		}
		it.Col, it.Row = orDefault(req.Col, it.Col), orDefault(req.Row, it.Row)
		it.W, it.H, it.Rot = orDefault(req.W, it.W), orDefault(req.H, it.H), orDefault(req.Rot, it.Rot)
		if err := checkGeometry(it.Col, it.Row, it.W, it.H, it.Rot); err != nil {
			return nil, err
		}
		if o := overlapping(l, it.Layer, it.Col, it.Row, it.W, it.H, it.ID); o != nil {
			return nil, layoutErr(http.StatusConflict, "Those cells are taken by %s on this layer.", itemLabel(p, ws, o))
		}
		l.Items[i] = it
		return map[string]string{"id": it.ID}, nil

	case "remove":
		var req layoutIDRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i := findItem(l, req.ID)
		if i < 0 {
			return nil, layoutErr(http.StatusNotFound, "That item is not on the layout.")
		}
		l.Items = append(l.Items[:i], l.Items[i+1:]...)
		return map[string]string{"id": req.ID}, nil

	case "layer-create":
		var req layoutLayerCreateRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		name, err := layoutName(req.Name)
		if err != nil {
			return nil, err
		}
		if err := checkZRange(req.ZKnown, req.ZMin, req.ZMax); err != nil {
			return nil, err
		}
		if len(l.Layers) >= layoutMaxLayers {
			return nil, layoutErr(http.StatusBadRequest, "A layout holds at most %d layers.", layoutMaxLayers)
		}
		ly := layoutLayer{ID: newLayoutID("ly"), Name: name, ZKnown: req.ZKnown, ZMin: req.ZMin, ZMax: req.ZMax, Order: len(l.Layers)}
		l.Layers = append(l.Layers, ly)
		return map[string]string{"id": ly.ID}, nil

	case "layer-rename":
		var req layoutLayerRenameRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i := findLayer(l, req.ID)
		if i < 0 {
			return nil, layoutErr(http.StatusNotFound, "That layer is not in this layout.")
		}
		name, err := layoutName(req.Name)
		if err != nil {
			return nil, err
		}
		l.Layers[i].Name = name
		return map[string]string{"id": req.ID}, nil

	case "layer-reorder":
		var req layoutLayerReorderRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		if len(req.Order) != len(l.Layers) {
			return nil, layoutErr(http.StatusBadRequest, "List every layer exactly once to reorder them (%d expected).", len(l.Layers))
		}
		seen := map[string]bool{}
		for pos, id := range req.Order {
			i := findLayer(l, id)
			if i < 0 || seen[id] {
				return nil, layoutErr(http.StatusBadRequest, "List every layer exactly once to reorder them.")
			}
			seen[id] = true
			l.Layers[i].Order = pos
		}
		return map[string]int{"layers": len(req.Order)}, nil

	case "layer-delete":
		var req layoutLayerDeleteRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i := findLayer(l, req.ID)
		if i < 0 {
			return nil, layoutErr(http.StatusNotFound, "That layer is not in this layout.")
		}
		kept := make([]layoutItem, 0, len(l.Items))
		for _, it := range l.Items {
			if it.Layer != req.ID {
				kept = append(kept, it)
			}
		}
		removed := len(l.Items) - len(kept)
		if removed > 0 && !req.RemoveItems {
			return nil, layoutErr(http.StatusConflict, "This layer still holds %d item(s); move them or delete the layer with removeItems.", removed)
		}
		l.Items = kept
		l.Layers = append(l.Layers[:i], l.Layers[i+1:]...)
		return map[string]int{"itemsRemoved": removed}, nil
	}
	return nil, layoutErr(http.StatusNotFound, "Unknown layout action %q.", action)
}

func checkZRange(known bool, zMin, zMax float64) error {
	if math.IsNaN(zMin) || math.IsNaN(zMax) || math.IsInf(zMin, 0) || math.IsInf(zMax, 0) {
		return layoutErr(http.StatusBadRequest, "Layer heights must be finite numbers.")
	}
	if !known && (zMin != 0 || zMax != 0) {
		return layoutErr(http.StatusBadRequest, "A layer without a known height range cannot carry zMin or zMax.")
	}
	if known && zMin > zMax {
		return layoutErr(http.StatusBadRequest, "A layer's zMin must not exceed its zMax.")
	}
	return nil
}

// deriveLayout places every located, not-yet-placed entry on the grid.
//
// Layers: the Z values of the entries being placed are sorted; a new layer
// starts wherever the gap to the previous value exceeds ZGapMM. A cluster
// whose Z range overlaps an existing height-known layer joins that layer
// (unchanged); otherwise a layer named after its range is created. Layers are
// created in ascending Z, after the existing ones.
//
// Cells (plan view, looking down): col = round((X - OriginX)/CellMM),
// row = round((OriginY - Y)/CellMM) — rows grow toward -Y (MVR is Z-up, and
// with +Y upstage rows read downstage). math.Round rounds halves away from 0.
// The origin is (min X, max Y) over EVERY located entry, fixed on the first
// derive (or on replace) and reused afterwards.
//
// Collisions, deterministically: entries are taken in patch order; the first
// keeps a contested cell. A later one gets the free cell (on its layer)
// nearest its exact fractional position by Euclidean distance, ties to the
// smaller row, then the smaller col, searching out to layoutSearchRings rings.
//
// Without replace, every existing item and layer is kept as it is. With
// replace (and Confirm "REPLACE"), all layers and items — manual ones and
// group placements included — are discarded and rebuilt. Entries with no
// location are never given a spot; they stay unplaced.
func deriveLayout(req layoutDeriveRequest, p *patch.Patch, l *showLayout) (any, error) {
	cell, gap := float64(layoutDefaultCellMM), float64(layoutDefaultZGapMM)
	if l.Derived && !req.Replace {
		cell, gap = l.CellMM, l.ZGapMM
	}
	if req.CellMM != nil {
		cell = *req.CellMM
	}
	if req.ZGapMM != nil {
		gap = *req.ZGapMM
	}
	if !(cell >= layoutMinCellMM && cell <= layoutMaxCellMM) {
		return nil, layoutErr(http.StatusBadRequest, "The cell size must be %d to %d mm.", layoutMinCellMM, layoutMaxCellMM)
	}
	if !(gap >= layoutMinZGapMM && gap <= layoutMaxZGapMM) {
		return nil, layoutErr(http.StatusBadRequest, "The layer height gap must be %d to %d mm.", layoutMinZGapMM, layoutMaxZGapMM)
	}
	if req.Replace && req.Confirm != "REPLACE" {
		return nil, layoutErr(http.StatusBadRequest, "Confirm REPLACE to discard the current layout, including hand placements, and rebuild it from positions.")
	}
	if !req.Replace && req.Confirm != "" {
		return nil, layoutErr(http.StatusBadRequest, "Confirm is only used with replace.")
	}
	if l.Derived && !req.Replace && cell != l.CellMM {
		return nil, layoutErr(http.StatusConflict, "This layout was derived at %g mm per cell; derive with replace to use a different cell size.", l.CellMM)
	}
	located := make([]patch.Entry, 0, len(p.Entries))
	for _, e := range p.Entries {
		if e.Location.Known {
			located = append(located, e)
		}
	}
	if len(located) == 0 {
		return nil, layoutErr(http.StatusBadRequest, "No fixture in this show has a position; import an MVR with positions or place fixtures by hand.")
	}
	res := layoutDeriveResult{Placed: make([]layoutDerivePlaced, 0), NotPlaced: make([]layoutUnplaced, 0)}
	if req.Replace {
		res.Discarded = len(l.Items)
		l.Items = make([]layoutItem, 0)
		l.Layers = make([]layoutLayer, 0)
		l.Derived = false
	}
	res.Kept = len(l.Items)
	if !l.Derived {
		l.OriginXMM, l.OriginYMM = located[0].Location.X, located[0].Location.Y
		for _, e := range located {
			l.OriginXMM = math.Min(l.OriginXMM, e.Location.X)
			l.OriginYMM = math.Max(l.OriginYMM, e.Location.Y)
		}
	}
	placed := map[string]bool{}
	for _, it := range l.Items {
		if it.Kind == "entry" {
			placed[it.Ref] = true
		}
	}
	todo := make([]patch.Entry, 0, len(located))
	for _, e := range located {
		if !placed[e.ID] {
			todo = append(todo, e)
		}
	}

	// Cluster Z (stable on patch order for equal Z).
	byZ := append([]patch.Entry(nil), todo...)
	sort.SliceStable(byZ, func(i, j int) bool { return byZ[i].Location.Z < byZ[j].Location.Z })
	layerOf := map[string]string{}
	type cluster struct {
		zMin, zMax float64
		ids        []string
	}
	clusters := make([]cluster, 0)
	for _, e := range byZ {
		z := e.Location.Z
		if n := len(clusters); n > 0 && z-clusters[n-1].zMax <= gap {
			clusters[n-1].zMax = z
			clusters[n-1].ids = append(clusters[n-1].ids, e.ID)
			continue
		}
		clusters = append(clusters, cluster{z, z, []string{e.ID}})
	}
	newLayers := make([]layoutLayer, 0)
	for _, c := range clusters {
		id := ""
		for _, ly := range l.Layers {
			if ly.ZKnown && ly.ZMin <= c.zMax && c.zMin <= ly.ZMax {
				id = ly.ID
				break
			}
		}
		if id == "" {
			name := fmt.Sprintf("Height %.0f mm", c.zMin)
			if math.Round(c.zMin) != math.Round(c.zMax) {
				name = fmt.Sprintf("Height %.0f–%.0f mm", c.zMin, c.zMax)
			}
			ly := layoutLayer{ID: newLayoutID("ly"), Name: name, ZKnown: true, ZMin: c.zMin, ZMax: c.zMax, Order: len(l.Layers) + len(newLayers)}
			newLayers = append(newLayers, ly)
			id = ly.ID
		}
		for _, eid := range c.ids {
			layerOf[eid] = id
		}
	}
	if len(l.Layers)+len(newLayers) > layoutMaxLayers {
		return nil, layoutErr(http.StatusBadRequest, "Deriving would need %d layers and a layout holds at most %d; derive again with a larger layer height gap.", len(l.Layers)+len(newLayers), layoutMaxLayers)
	}
	l.Layers = append(l.Layers, newLayers...)
	res.LayersCreated = len(newLayers)

	type cellKey struct {
		layer    string
		col, row int
	}
	taken := map[cellKey]bool{}
	for _, it := range l.Items {
		for c := it.Col; c < it.Col+it.W; c++ {
			for r := it.Row; r < it.Row+it.H; r++ {
				taken[cellKey{it.Layer, c, r}] = true
			}
		}
	}
	inGrid := func(c, r int) bool {
		return c >= -layoutMaxCell && c <= layoutMaxCell && r >= -layoutMaxCell && r <= layoutMaxCell
	}
	for _, e := range todo {
		layer := layerOf[e.ID]
		fx := (e.Location.X - l.OriginXMM) / cell
		fy := (l.OriginYMM - e.Location.Y) / cell
		c0, r0 := int(math.Round(fx)), int(math.Round(fy))
		col, row, displaced := c0, r0, false
		if !inGrid(c0, r0) || taken[cellKey{layer, c0, r0}] {
			displaced = true
			found := false
			for ring := 1; ring <= layoutSearchRings && !found; ring++ {
				for dc := -ring; dc <= ring && !found; dc++ {
					for dr := -ring; dr <= ring; dr++ {
						if max(abs(dc), abs(dr)) == ring && inGrid(c0+dc, r0+dr) && !taken[cellKey{layer, c0 + dc, r0 + dr}] {
							found = true
							// Any cell nearer the exact position than this
							// one lies within 2·ring+1 rings (see the
							// distance bound in the file notes); pick the
							// best of those.
							col, row = nearestFree(func(c, r int) bool { return inGrid(c, r) && !taken[cellKey{layer, c, r}] }, c0, r0, fx, fy, 2*ring+1)
							break
						}
					}
				}
			}
			if !found {
				res.NotPlaced = append(res.NotPlaced, layoutUnplaced{EntryID: e.ID, Reason: "no-free-cell"})
				continue
			}
		}
		taken[cellKey{layer, col, row}] = true
		it := layoutItem{ID: newLayoutID("pl"), Kind: "entry", Ref: e.ID, Layer: layer, Col: col, Row: row, W: 1, H: 1, Rot: 0}
		l.Items = append(l.Items, it)
		res.Placed = append(res.Placed, layoutDerivePlaced{EntryID: e.ID, ItemID: it.ID, Layer: layer, Col: col, Row: row, Displaced: displaced})
	}
	for _, e := range p.Entries {
		if !e.Location.Known && !placed[e.ID] {
			res.NotPlaced = append(res.NotPlaced, layoutUnplaced{EntryID: e.ID, Reason: "no-location"})
		}
	}
	l.Derived, l.CellMM, l.ZGapMM = true, cell, gap
	return res, nil
}

// nearestFree returns the free cell within Chebyshev radius `radius` of
// (c0, r0) nearest the fractional point (fx, fy); ties go to the smaller
// row, then the smaller col. Called only once a free cell is known to exist
// in ring k, with radius 2k+1: a cell farther than that is at least
// 2k+0.5 from the point, while the ring-k cell is at most (k+0.5)·√2 —
// smaller for every k >= 1 — so nothing nearer is missed.
func nearestFree(free func(c, r int) bool, c0, r0 int, fx, fy float64, radius int) (int, int) {
	bc, br, best := 0, 0, math.Inf(1)
	for r := r0 - radius; r <= r0+radius; r++ {
		for c := c0 - radius; c <= c0+radius; c++ {
			if !free(c, r) {
				continue
			}
			d := (float64(c)-fx)*(float64(c)-fx) + (float64(r)-fy)*(float64(r)-fy)
			if d < best || (d == best && (r < br || (r == br && c < bc))) {
				bc, br, best = c, r, d
			}
		}
	}
	return bc, br
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
