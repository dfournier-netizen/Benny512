// This file implements the per-show Console-lite layout (Console-lite C2):
// a plain 2D grid with Z layers on which patch entries and saved groups are
// placed, either by hand or derived from MVR world locations
// (patch.Location).
//
//	GET  /api/patch/layout            -> layoutResponse
//	POST /api/patch/layout/{action}   -> layoutResponse with "result"
//	     derive | refresh | place | move | remove | pin | reset-auto |
//	     item-order |
//	     layer-create | layer-rename | layer-reorder | layer-delete |
//	     object-create | object-update | object-duplicate | object-delete
//
// MODES (C2b): every placement has mode "auto" or "manual". Derive and
// refresh create, move and remove ONLY auto fixture placements; a manual one
// is never moved by them. Any move/resize/rotate/layer change through the API
// makes an item manual; pin makes it manual where it stands; reset-auto hands
// it (or every fixture placement) back to automatic placement. Group
// placements and objects are always manual. A C2 layout (no "schema" key) is
// migrated on read: every existing placement becomes manual, because C2 did
// not record which were derived; layout.migratedManual says how many.
//
// UNPLACED LAYER (C2b): a system layer with the fixed ID "unplaced" holds
// every patch entry that has no other placement, in a tidy grid of
// layoutUnplacedCols columns in patch order, skipping cells taken by stored
// fixture/group items on that layer. These automatic Unplaced placements are
// never stored: they are computed from the stored layout and the patch on
// every read and inside every layout edit, so they are current after any
// entry add, delete, import or location change without hooking those paths
// and without GET ever writing the show file. Each has the stable ID
// "entry-<entry id>"; moving or pinning one stores it as manual under that
// same ID. The layer cannot be deleted; it can be renamed and reordered (its
// identity is its ID and "system": true, not its name).
//
// OBJECTS (C2b): kind "object" items (truss, label, area, mark — one table,
// layoutObjectTypes) are display/editing items, never fixture-selectable
// (C4 selection must skip kind "object"). Their geometry is in cells at
// half-cell precision, with integer N being the top-left edge of cell N
// (so N+0.5 is a cell centre); col/row/w/h on the item are then the cells
// the geometry covers, computed, never edited directly. Objects never block
// a fixture's cell.
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

	// C2b.
	layoutSchema       = 2
	layoutMaxObjects   = 500
	layoutObjectStep   = 0.5 // object geometry precision, in cells
	layoutUnplacedCols = 16  // width of the Unplaced layer's tidy grid
	unplacedLayerID    = "unplaced"
	unplacedLayerName  = "Unplaced"
	modeAuto           = "auto"
	modeManual         = "manual"
)

// layoutObjectType is one kind of layout object. Shape decides the geometry:
// "line" = col,row -> col2,row2; "rect" = col,row,w,h; "point" = col,row.
// Add a type here and every action, GET's objectTypes and validation pick it
// up.
type layoutObjectType struct {
	Type         string `json:"type"`
	Label        string `json:"label"`
	Shape        string `json:"shape"`
	TextRequired bool   `json:"textRequired"`
	Rotatable    bool   `json:"rotatable"`
}

var layoutObjectTypes = []layoutObjectType{
	{Type: "truss", Label: "Truss or pipe", Shape: "line"},
	{Type: "label", Label: "Text label", Shape: "point", TextRequired: true, Rotatable: true},
	{Type: "area", Label: "Stage or area outline", Shape: "rect"},
	{Type: "mark", Label: "Reference mark (centre line, stage edge)", Shape: "line"},
}

func objectTypeOf(name string) (layoutObjectType, bool) {
	for _, t := range layoutObjectTypes {
		if t.Type == name {
			return t, true
		}
	}
	return layoutObjectType{}, false
}

// entryItemID is the stable ID of the item placing entry id when this build
// creates it (automatic Unplaced, derive, place, or a stored Unplaced item).
func entryItemID(id string) string { return "entry-" + id }

// showLayout is the stored layout. Derived is false until the first derive;
// until then CellMM/ZGapMM/OriginXMM/OriginYMM are 0 and mean nothing.
// OriginXMM/OriginYMM are the world X/Y (mm) that cell 0,0 stands for, fixed
// by the first derive so a later derive lands new fixtures on the same grid
// (an extension of the brief's shape, for that reason).
//
// Schema is 2 from C2b on; a C2 layout has none (0) and is migrated by
// normalizeLayout. MigratedManual is how many placements that migration
// turned into manual ones (0 for a layout created by C2b or later).
type showLayout struct {
	Schema         int           `json:"schema"`
	MigratedManual int           `json:"migratedManual"`
	Derived        bool          `json:"derived"`
	CellMM         float64       `json:"cellMm"`
	ZGapMM         float64       `json:"zGapMm"`
	OriginXMM      float64       `json:"originXMm"`
	OriginYMM      float64       `json:"originYMm"`
	Layers         []layoutLayer `json:"layers"`
	Items          []layoutItem  `json:"items"`
}

// layoutLayer is one Z layer. ZKnown false = a hand-made layer with no
// height range (ZMin/ZMax 0, meaningless); derived layers carry the Z range
// (mm) of the fixtures that formed them. Order is the display order, 0 first,
// kept contiguous. System is true only for the Unplaced layer.
type layoutLayer struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	System bool    `json:"system"`
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
//
// C2b: Kind may also be "object" (Ref ""; ObjectType, Text and Geometry
// set; Col/Row/W/H are the covered cells). Mode is "auto" or "manual" (see
// the file notes). Order is the stacking order within the layer, 0 at the
// bottom, kept contiguous per layer.
type layoutItem struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Ref        string          `json:"ref"`
	Layer      string          `json:"layer"`
	Col        int             `json:"col"`
	Row        int             `json:"row"`
	W          int             `json:"w"`
	H          int             `json:"h"`
	Rot        int             `json:"rot"`
	Mode       string          `json:"mode"`
	Order      int             `json:"order"`
	ObjectType string          `json:"objectType,omitempty"`
	Text       string          `json:"text,omitempty"`
	Geometry   *layoutGeometry `json:"geometry,omitempty"`
}

// layoutGeometry is an object's shape in cells, in half-cell steps. Col/Row
// are always present; Col2/Row2 only for a line, W/H only for a rectangle
// (absent, not zero, otherwise).
type layoutGeometry struct {
	Col  float64  `json:"col"`
	Row  float64  `json:"row"`
	Col2 *float64 `json:"col2,omitempty"`
	Row2 *float64 `json:"row2,omitempty"`
	W    *float64 `json:"w,omitempty"`
	H    *float64 `json:"h,omitempty"`
}

func normalizeLayout(l *showLayout) {
	if l.Layers == nil {
		l.Layers = make([]layoutLayer, 0)
	}
	if l.Items == nil {
		l.Items = make([]layoutItem, 0)
	}
	if l.Schema < layoutSchema {
		n := 0
		for i := range l.Items {
			if l.Items[i].Mode == "" {
				l.Items[i].Mode = modeManual
				n++
			}
		}
		l.Schema, l.MigratedManual = layoutSchema, n
	}
	sort.SliceStable(l.Layers, func(i, j int) bool { return l.Layers[i].Order < l.Layers[j].Order })
	if findLayer(l, unplacedLayerID) < 0 {
		l.Layers = append(l.Layers, layoutLayer{ID: unplacedLayerID, Name: unplacedLayerName})
	}
	for i := range l.Layers {
		l.Layers[i].System = l.Layers[i].ID == unplacedLayerID
		l.Layers[i].Order = i
	}
	renumberItemOrders(l)
}

// renumberItemOrders makes Order contiguous (0..n-1) within each layer,
// keeping the relative order (ties: slice position).
func renumberItemOrders(l *showLayout) {
	byLayer := map[string][]int{}
	for i, it := range l.Items {
		byLayer[it.Layer] = append(byLayer[it.Layer], i)
	}
	for _, idx := range byLayer {
		sort.SliceStable(idx, func(a, b int) bool { return l.Items[idx[a]].Order < l.Items[idx[b]].Order })
		for n, i := range idx {
			l.Items[i].Order = n
		}
	}
}

// nextOrder is the Order that puts a new item on top of layer.
func nextOrder(l *showLayout, layer string) int {
	n := 0
	for _, it := range l.Items {
		if it.Layer == layer && it.Order >= n {
			n = it.Order + 1
		}
	}
	return n
}

// unplacedItems computes the automatic Unplaced placements (never stored):
// every entry with no stored item, in patch order, in the first free cell
// of a layoutUnplacedCols-wide grid from 0,0 on the Unplaced layer. A cell
// is taken by a stored fixture or group item there; objects never take one.
// Reason: "no-location" (nothing to derive from) or "not-derived" (has a
// position but no derived placement: not derived yet, or derive found no
// free cell). ItemID is "" only if all 16,000 cells are taken.
func unplacedItems(p patch.Patch, l *showLayout) ([]layoutItem, []layoutUnplaced) {
	hasItem := map[string]bool{}
	type cell struct{ c, r int }
	taken := map[cell]bool{}
	onLayer := 0
	for _, it := range l.Items {
		if it.Kind == "entry" {
			hasItem[it.Ref] = true
		}
		if it.Layer != unplacedLayerID {
			continue
		}
		onLayer++
		if it.Kind == "object" {
			continue
		}
		for c := it.Col; c < it.Col+it.W; c++ {
			for r := it.Row; r < it.Row+it.H; r++ {
				taken[cell{c, r}] = true
			}
		}
	}
	items, list := make([]layoutItem, 0), make([]layoutUnplaced, 0)
	limit := layoutUnplacedCols * (layoutMaxCell + 1)
	k := 0
	for _, e := range p.Entries {
		if hasItem[e.ID] {
			continue
		}
		reason := "not-derived"
		if !e.Location.Known {
			reason = "no-location"
		}
		for k < limit && taken[cell{k % layoutUnplacedCols, k / layoutUnplacedCols}] {
			k++
		}
		if k >= limit {
			list = append(list, layoutUnplaced{EntryID: e.ID, Reason: reason})
			continue
		}
		it := layoutItem{ID: entryItemID(e.ID), Kind: "entry", Ref: e.ID, Layer: unplacedLayerID,
			Col: k % layoutUnplacedCols, Row: k / layoutUnplacedCols, W: 1, H: 1, Mode: modeAuto, Order: onLayer + len(items)}
		items = append(items, it)
		list = append(list, layoutUnplaced{EntryID: e.ID, Reason: reason, ItemID: it.ID})
		k++
	}
	return items, list
}

// materialize returns the stored index of item id. An automatic Unplaced
// item is first stored, as manual, where it stands (same ID). -1 = no such
// item.
func materialize(p *patch.Patch, l *showLayout, id string) int {
	if i := findItem(l, id); i >= 0 {
		return i
	}
	virt, _ := unplacedItems(*p, l)
	for _, v := range virt {
		if v.ID == id {
			v.Mode = modeManual
			l.Items = append(l.Items, v)
			return len(l.Items) - 1
		}
	}
	return -1
}

func isUnplacedAuto(p *patch.Patch, l *showLayout, id string) bool {
	virt, _ := unplacedItems(*p, l)
	for _, v := range virt {
		if v.ID == id {
			return true
		}
	}
	return false
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
	// Placed false = the entry is only in the automatic Unplaced grid.
	// ItemID is the item that shows (and selects) it on the layout.
	Placed bool   `json:"placed"`
	ItemID string `json:"itemId"`
}

type layoutUnplaced struct {
	EntryID string `json:"entryId"`
	// Reason: "no-location" (nothing to derive from; place by hand),
	// "not-derived" (has a location, no derived placement yet) or, in a
	// derive result only, "no-free-cell".
	Reason string `json:"reason"`
	// ItemID is its automatic item on the Unplaced layer.
	ItemID string `json:"itemId"`
}

type layoutStale struct {
	ItemID  string `json:"itemId"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Problem string `json:"problem"`
}

type layoutLimits struct {
	MaxLayers int `json:"maxLayers"`
	// MaxItems bounds fixture and group placements (objects excluded).
	MaxItems      int     `json:"maxItems"`
	MaxSpan       int     `json:"maxSpan"`
	MinCell       int     `json:"minCell"`
	MaxCell       int     `json:"maxCell"`
	MaxObjects    int     `json:"maxObjects"`
	ObjectTextMax int     `json:"objectTextMax"`
	ObjectStep    float64 `json:"objectStep"`
	UnplacedCols  int     `json:"unplacedCols"`
}

type layoutResponse struct {
	// Revision is the layout revision this body reflects (C6c): every
	// successful layout action bumps it and broadcasts it on the WebSocket
	// as {"type":"layout","revision":N}, so a browser that already holds N
	// (its own edit's answer) skips the re-read.
	Revision uint64            `json:"revision"`
	Active   bool              `json:"active"`
	Name     string            `json:"name"`
	Layout   showLayout        `json:"layout"`
	Entries  []layoutEntryView `json:"entries"`
	Groups   []savedGroup      `json:"groups"`
	Unplaced []layoutUnplaced  `json:"unplaced"`
	Stale    []layoutStale     `json:"stale"`
	Limits   layoutLimits      `json:"limits"`
	// ObjectTypes is layoutObjectTypes, so the editor needs no copy of it.
	ObjectTypes []layoutObjectType `json:"objectTypes"`
	Result      any                `json:"result,omitempty"`
}

func buildLayoutResponse(p patch.Patch, active bool, ws showWorkspace) layoutResponse {
	l := ws.Layout
	normalizeLayout(&l)
	stored := l.Items
	virt, unplaced := unplacedItems(p, &l)
	l.Items = append(append(make([]layoutItem, 0, len(stored)+len(virt)), stored...), virt...)
	resp := layoutResponse{
		Active: active, Name: p.Name, Layout: l,
		Entries:  make([]layoutEntryView, 0, len(p.Entries)),
		Groups:   ws.Groups,
		Unplaced: unplaced,
		Stale:    make([]layoutStale, 0),
		Limits: layoutLimits{layoutMaxLayers, len(p.Entries) + len(ws.Groups), layoutMaxSpan, -layoutMaxCell, layoutMaxCell,
			layoutMaxObjects, layoutNameMax, layoutObjectStep, layoutUnplacedCols},
		ObjectTypes: layoutObjectTypes,
	}
	placed, itemOf := map[string]bool{}, map[string]string{}
	for _, it := range stored {
		if it.Kind == "entry" {
			placed[it.Ref] = true
			itemOf[it.Ref] = it.ID
		}
	}
	for _, v := range virt {
		itemOf[v.Ref] = v.ID
	}
	for _, e := range p.Entries {
		n := patch.GeometryCellEstimate(e)
		resp.Entries = append(resp.Entries, layoutEntryView{
			ID: e.ID, Name: e.Name, FixtureType: e.FixtureType, Mode: e.Mode, FixtureNumber: e.FixtureNumber,
			Universe: e.Universe, StartAddress: e.StartAddress, Footprint: e.Footprint, Location: e.Location,
			CellCount: n, CellCountKnown: n > 0, Placed: placed[e.ID], ItemID: itemOf[e.ID],
		})
	}
	groups := map[string]bool{}
	for _, g := range ws.Groups {
		groups[g.ID] = true
	}
	layers := map[string]bool{}
	for _, ly := range l.Layers {
		layers[ly.ID] = true
	}
	for _, it := range stored {
		problem := ""
		_, typeKnown := objectTypeOf(it.ObjectType)
		switch {
		case it.Kind == "entry" && p.IndexOf(it.Ref) < 0:
			problem = "The patch entry this item places is no longer in the patch."
		case it.Kind == "group" && !groups[it.Ref]:
			problem = "The saved group this item places no longer exists."
		case it.Kind == "object" && !typeKnown:
			problem = fmt.Sprintf("The object type %q is not known to this build.", it.ObjectType)
		case it.Kind == "object" && it.Geometry == nil:
			problem = "The object has no geometry."
		case it.Kind != "entry" && it.Kind != "group" && it.Kind != "object":
			problem = "The item is not a fixture, group or object placement."
		case !layers[it.Layer]:
			problem = "The item's layer no longer exists."
		case it.Mode != modeAuto && it.Mode != modeManual:
			problem = "The item's mode is neither auto nor manual."
		case it.Mode == modeAuto && it.Kind != "entry":
			problem = "Only a fixture placement can be automatic."
		}
		if problem != "" {
			resp.Stale = append(resp.Stale, layoutStale{ItemID: it.ID, Kind: it.Kind, Ref: it.Ref, Problem: problem})
		}
	}
	return resp
}

// --- live sync (C6c) ---------------------------------------------------------

const layoutRevisionHeader = "X-Benny-Layout"

// layoutRevision counts successful layout actions. Seeded from the clock in
// milliseconds (exact in a JS number) so a browser holding a revision from
// before a restart cannot match.
type layoutRevision struct{ n atomic.Uint64 }

func newLayoutRevision() *layoutRevision {
	r := &layoutRevision{}
	r.n.Store(uint64(time.Now().UnixMilli()))
	return r
}

func (s *Server) writeLayoutJSON(w http.ResponseWriter, resp layoutResponse) {
	w.Header().Set(layoutRevisionHeader, fmt.Sprint(resp.Revision))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetLayout(w http.ResponseWriter, r *http.Request) {
	// The revision is read BEFORE the show: a mutation landing in between
	// makes the body newer than its revision, never older, so the
	// broadcast that follows still triggers a re-read.
	rev := s.layoutRev.n.Load()
	p, ok := s.PatchStore.Get()
	ws, err := strictWorkspace(p)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	resp := buildLayoutResponse(p, ok, ws)
	resp.Revision = rev
	s.writeLayoutJSON(w, resp)
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

type layoutResetRequest struct {
	ID      string `json:"id"`
	All     bool   `json:"all"`
	Confirm string `json:"confirm"`
}

// layoutOrderRequest: To is "front", "back", "forward" (one step up) or
// "backward" (one step down) within the item's layer.
type layoutOrderRequest struct {
	ID string `json:"id"`
	To string `json:"to"`
}

type layoutObjectCreateRequest struct {
	ObjectType string          `json:"objectType"`
	Layer      string          `json:"layer"`
	Text       string          `json:"text"`
	Rot        int             `json:"rot"`
	Geometry   *layoutGeometry `json:"geometry"`
}

// layoutObjectUpdateRequest: absent fields are left as they are; a
// geometry replaces the whole geometry. The object type cannot change.
type layoutObjectUpdateRequest struct {
	ID       string          `json:"id"`
	Layer    string          `json:"layer"`
	Text     *string         `json:"text"`
	Rot      *int            `json:"rot"`
	Geometry *layoutGeometry `json:"geometry"`
}

// layoutObjectDuplicateRequest: the copy is offset by dCol/dRow cells
// (half-cell steps; default 1 and 1) and stacked on top of its layer.
type layoutObjectDuplicateRequest struct {
	ID   string   `json:"id"`
	DCol *float64 `json:"dCol"`
	DRow *float64 `json:"dRow"`
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

// Placed = auto placements created; Moved = auto placements whose layer or
// cell changed; Unchanged = auto placements left where they were;
// ReturnedToUnplaced = entries whose auto placement was dropped because they
// no longer have a position (they are back in the Unplaced grid); Kept =
// stored items derive does not own (manual, groups, objects).
type layoutDeriveResult struct {
	Placed             []layoutDerivePlaced `json:"placed"`
	Moved              []layoutDerivePlaced `json:"moved"`
	Unchanged          int                  `json:"unchanged"`
	ReturnedToUnplaced []string             `json:"returnedToUnplaced"`
	NotPlaced          []layoutUnplaced     `json:"notPlaced"`
	LayersCreated      int                  `json:"layersCreated"`
	Kept               int                  `json:"kept"`
	Discarded          int                  `json:"discarded"`
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
		"layer-create": true, "layer-rename": true, "layer-reorder": true, "layer-delete": true,
		"refresh": true, "pin": true, "reset-auto": true, "item-order": true,
		"object-create": true, "object-update": true, "object-duplicate": true, "object-delete": true}
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
	var rev uint64
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
		renumberItemOrders(&ws.Layout)
		encoded, err := json.Marshal(ws)
		if err != nil {
			return err
		}
		p.Workspace = encoded
		// Bumped inside the store's write, so revisions follow the order
		// the mutations were applied in (a store write that then fails
		// leaves a gap, which only costs a browser one spare re-read).
		rev = s.layoutRev.n.Add(1)
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
	resp.Revision = rev
	s.hub.broadcast(wsMessage{Type: "layout", At: time.Now(), Revision: &rev})
	s.writeLayoutJSON(w, resp)
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

// overlapping returns the fixture or group item on layer that shares a cell
// with the rectangle, ignoring the item with ID skip, or nil. Objects never
// take a cell.
func overlapping(l *showLayout, layer string, col, row, w, h int, skip string) *layoutItem {
	for i := range l.Items {
		it := &l.Items[i]
		if it.ID == skip || it.Layer != layer || it.Kind == "object" {
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

	case "refresh":
		var req struct{}
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		if !l.Derived {
			return nil, layoutErr(http.StatusConflict, "Derive the layout from positions first; refresh re-runs that derive with its saved cell size and layer gap.")
		}
		return autoPlace(p, l, l.CellMM, l.ZGapMM)

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
		id := newLayoutID("pl")
		if req.Kind == "entry" {
			id = entryItemID(req.Ref)
		}
		it := layoutItem{ID: id, Kind: req.Kind, Ref: req.Ref, Layer: req.Layer, Col: req.Col, Row: req.Row,
			W: orDefault(req.W, 1), H: orDefault(req.H, 1), Rot: orDefault(req.Rot, 0), Mode: modeManual, Order: nextOrder(l, req.Layer)}
		if err := checkGeometry(it.Col, it.Row, it.W, it.H, it.Rot); err != nil {
			return nil, err
		}
		for i := range l.Items {
			if l.Items[i].Kind == it.Kind && l.Items[i].Ref == it.Ref {
				return nil, layoutErr(http.StatusConflict, "This %s is already on the layout; move it instead.", it.Kind)
			}
		}
		if countKind(l, "entry")+countKind(l, "group") >= len(p.Entries)+len(ws.Groups) {
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
		if i := findItem(l, req.ID); i >= 0 && l.Items[i].Kind == "object" {
			return nil, layoutErr(http.StatusBadRequest, "Edit an object's position with object-update; its geometry is in half cells.")
		}
		i := materialize(p, l, req.ID)
		if i < 0 {
			return nil, layoutErr(http.StatusNotFound, "That item is not on the layout.")
		}
		it := l.Items[i]
		if req.Layer != "" && req.Layer != it.Layer {
			if findLayer(l, req.Layer) < 0 {
				return nil, layoutErr(http.StatusNotFound, "That layer is not in this layout.")
			}
			it.Layer = req.Layer
			it.Order = nextOrder(l, req.Layer)
		}
		it.Mode = modeManual
		it.Col, it.Row = orDefault(req.Col, it.Col), orDefault(req.Row, it.Row)
		it.W, it.H, it.Rot = orDefault(req.W, it.W), orDefault(req.H, it.H), orDefault(req.Rot, it.Rot)
		if err := checkGeometry(it.Col, it.Row, it.W, it.H, it.Rot); err != nil {
			return nil, err
		}
		if o := overlapping(l, it.Layer, it.Col, it.Row, it.W, it.H, it.ID); o != nil {
			return nil, layoutErr(http.StatusConflict, "Those cells are taken by %s on this layer.", itemLabel(p, ws, o))
		}
		l.Items[i] = it
		return map[string]string{"id": it.ID, "mode": it.Mode}, nil

	case "remove":
		var req layoutIDRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i := findItem(l, req.ID)
		if i < 0 {
			if isUnplacedAuto(p, l, req.ID) {
				return nil, layoutErr(http.StatusConflict, "This fixture is in the Unplaced layer because it has no other place; it can be moved but not removed, so it stays selectable.")
			}
			return nil, layoutErr(http.StatusNotFound, "That item is not on the layout.")
		}
		l.Items = append(l.Items[:i], l.Items[i+1:]...)
		return map[string]string{"id": req.ID}, nil

	case "pin":
		var req layoutIDRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i := materialize(p, l, req.ID)
		if i < 0 {
			return nil, layoutErr(http.StatusNotFound, "That item is not on the layout.")
		}
		l.Items[i].Mode = modeManual
		return map[string]string{"id": req.ID, "mode": modeManual}, nil

	case "reset-auto":
		var req layoutResetRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		n := 0
		switch {
		case req.All && req.ID != "":
			return nil, layoutErr(http.StatusBadRequest, "Name one item or all, not both.")
		case req.All:
			if req.Confirm != "RESET" {
				return nil, layoutErr(http.StatusBadRequest, "Confirm RESET to hand every fixture placement back to automatic placement; fixtures placed by hand will move.")
			}
			for i := range l.Items {
				if l.Items[i].Kind == "entry" && l.Items[i].Mode != modeAuto {
					l.Items[i].Mode = modeAuto
					n++
				}
			}
		case req.ID == "":
			return nil, layoutErr(http.StatusBadRequest, "Name the item to reset, or all with confirm RESET.")
		case req.Confirm != "":
			return nil, layoutErr(http.StatusBadRequest, "Confirm is only used with all.")
		default:
			i := findItem(l, req.ID)
			if i < 0 {
				if isUnplacedAuto(p, l, req.ID) {
					break // already automatic
				}
				return nil, layoutErr(http.StatusNotFound, "That item is not on the layout.")
			}
			switch l.Items[i].Kind {
			case "group":
				return nil, layoutErr(http.StatusConflict, "A group placement is always placed by hand; it has no automatic position.")
			case "object":
				return nil, layoutErr(http.StatusConflict, "An object is always placed by hand; it has no automatic position.")
			}
			if l.Items[i].Mode != modeAuto {
				l.Items[i].Mode = modeAuto
				n++
			}
		}
		if n > 0 {
			if err := settleAuto(p, l); err != nil {
				return nil, err
			}
		}
		return map[string]int{"reset": n}, nil

	case "item-order":
		var req layoutOrderRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		if req.To != "front" && req.To != "back" && req.To != "forward" && req.To != "backward" {
			return nil, layoutErr(http.StatusBadRequest, "Stacking order must be front, back, forward or backward.")
		}
		i := findItem(l, req.ID)
		if i < 0 {
			if isUnplacedAuto(p, l, req.ID) {
				return nil, layoutErr(http.StatusConflict, "This fixture is placed automatically in the Unplaced layer; pin it before changing its stacking order.")
			}
			return nil, layoutErr(http.StatusNotFound, "That item is not on the layout.")
		}
		renumberItemOrders(l)
		sib := make([]int, 0)
		for j, it := range l.Items {
			if it.Layer == l.Items[i].Layer {
				sib = append(sib, j)
			}
		}
		sort.SliceStable(sib, func(a, b int) bool { return l.Items[sib[a]].Order < l.Items[sib[b]].Order })
		pos := 0
		for k, j := range sib {
			if j == i {
				pos = k
			}
		}
		switch req.To {
		case "front":
			l.Items[i].Order = len(sib)
		case "back":
			l.Items[i].Order = -1
		case "forward":
			if pos+1 < len(sib) {
				o := sib[pos+1]
				l.Items[i].Order, l.Items[o].Order = l.Items[o].Order, l.Items[i].Order
			}
		case "backward":
			if pos > 0 {
				o := sib[pos-1]
				l.Items[i].Order, l.Items[o].Order = l.Items[o].Order, l.Items[i].Order
			}
		}
		return map[string]string{"id": req.ID}, nil

	case "object-create":
		var req layoutObjectCreateRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		t, ok := objectTypeOf(req.ObjectType)
		if !ok {
			return nil, layoutErr(http.StatusBadRequest, "The object type must be one of %s.", objectTypeList())
		}
		if findLayer(l, req.Layer) < 0 {
			return nil, layoutErr(http.StatusNotFound, "That layer is not in this layout.")
		}
		it := layoutItem{ID: newLayoutID("ob"), Kind: "object", Layer: req.Layer, Mode: modeManual, Order: nextOrder(l, req.Layer),
			ObjectType: t.Type, Text: strings.TrimSpace(req.Text), Rot: req.Rot, Geometry: cloneGeometry(req.Geometry)}
		if err := checkObject(t, &it); err != nil {
			return nil, err
		}
		if countKind(l, "object") >= layoutMaxObjects {
			return nil, layoutErr(http.StatusBadRequest, "A layout holds at most %d objects.", layoutMaxObjects)
		}
		l.Items = append(l.Items, it)
		return map[string]string{"id": it.ID}, nil

	case "object-update":
		var req layoutObjectUpdateRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i, t, err := findObject(l, req.ID, "update")
		if err != nil {
			return nil, err
		}
		it := l.Items[i]
		if req.Layer != "" && req.Layer != it.Layer {
			if findLayer(l, req.Layer) < 0 {
				return nil, layoutErr(http.StatusNotFound, "That layer is not in this layout.")
			}
			it.Layer, it.Order = req.Layer, nextOrder(l, req.Layer)
		}
		if req.Text != nil {
			it.Text = strings.TrimSpace(*req.Text)
		}
		if req.Rot != nil {
			it.Rot = *req.Rot
		}
		if req.Geometry != nil {
			it.Geometry = cloneGeometry(req.Geometry)
		}
		if err := checkObject(t, &it); err != nil {
			return nil, err
		}
		l.Items[i] = it
		return map[string]string{"id": it.ID}, nil

	case "object-duplicate":
		var req layoutObjectDuplicateRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i, t, err := findObject(l, req.ID, "duplicate")
		if err != nil {
			return nil, err
		}
		dc, dr := 1.0, 1.0
		if req.DCol != nil {
			dc = *req.DCol
		}
		if req.DRow != nil {
			dr = *req.DRow
		}
		it := l.Items[i]
		it.ID, it.Order, it.Geometry = newLayoutID("ob"), nextOrder(l, it.Layer), cloneGeometry(it.Geometry)
		if it.Geometry == nil {
			return nil, layoutErr(http.StatusConflict, "This object has no geometry to copy; remove it instead.")
		}
		it.Geometry.Col += dc
		it.Geometry.Row += dr
		if it.Geometry.Col2 != nil {
			*it.Geometry.Col2 += dc
			*it.Geometry.Row2 += dr
		}
		if err := checkObject(t, &it); err != nil {
			return nil, err
		}
		if countKind(l, "object") >= layoutMaxObjects {
			return nil, layoutErr(http.StatusBadRequest, "A layout holds at most %d objects.", layoutMaxObjects)
		}
		l.Items = append(l.Items, it)
		return map[string]string{"id": it.ID}, nil

	case "object-delete":
		var req layoutIDRequest
		if err := decodeStrict(body, &req); err != nil {
			return nil, err
		}
		i := findItem(l, req.ID)
		switch {
		case i < 0 && isUnplacedAuto(p, l, req.ID):
			return nil, layoutErr(http.StatusBadRequest, "That item is not an object.")
		case i < 0:
			return nil, layoutErr(http.StatusNotFound, "That object is not on the layout.")
		case l.Items[i].Kind != "object":
			return nil, layoutErr(http.StatusBadRequest, "That item is not an object; take fixtures and groups off the layout with remove.")
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
		ly := layoutLayer{ID: newLayoutID("ly"), Name: name, ZKnown: req.ZKnown, ZMin: req.ZMin, ZMax: req.ZMax}
		insertLayers(l, []layoutLayer{ly})
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
		if req.ID == unplacedLayerID {
			return nil, layoutErr(http.StatusConflict, "The Unplaced layer cannot be deleted; it holds every fixture that has no other place, so each one stays selectable.")
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

// deriveLayout (re)places every automatic fixture placement from positions.
//
// Layers: the Z values of the entries being placed are sorted; a new layer
// starts wherever the gap to the previous value exceeds ZGapMM. A cluster
// whose Z range overlaps an existing height-known layer joins that layer
// (unchanged); otherwise a layer named after its range is created. New
// layers are created in ascending Z and inserted just before the Unplaced
// layer.
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
// C2b: without replace, manual items, group placements and objects are kept
// as they are and every AUTO fixture placement is recomputed (autoPlace) —
// so derive and refresh both follow changed positions. With replace (and
// Confirm "REPLACE"), all layers except Unplaced and all items — manual
// ones, group placements and objects included — are discarded and rebuilt.
// Entries with no location are never given a derived spot; they stay in the
// Unplaced grid.
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
		return nil, layoutErr(http.StatusBadRequest, "Confirm REPLACE to discard the current layout, including hand placements and objects, and rebuild it from positions.")
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
	discarded := 0
	if req.Replace {
		discarded = len(l.Items)
		l.Items = make([]layoutItem, 0)
		kept := make([]layoutLayer, 0, 1)
		if i := findLayer(l, unplacedLayerID); i >= 0 {
			kept = append(kept, l.Layers[i])
		}
		l.Layers = kept
		l.Derived = false
	}
	if !l.Derived {
		l.OriginXMM, l.OriginYMM = located[0].Location.X, located[0].Location.Y
		for _, e := range located {
			l.OriginXMM = math.Min(l.OriginXMM, e.Location.X)
			l.OriginYMM = math.Max(l.OriginYMM, e.Location.Y)
		}
	}
	res, err := autoPlace(p, l, cell, gap)
	if err != nil {
		return nil, err
	}
	res.Discarded = discarded
	l.Derived, l.CellMM, l.ZGapMM = true, cell, gap
	return res, nil
}

// autoPlace recomputes every automatic fixture placement (the derive
// algorithm above) on the current origin, cell size and layer gap: auto
// entry items are taken off, then every located entry without a stored
// (manual) item is placed again in patch order, with manual items and group
// placements as obstacles. An entry keeps its item ID (and its stacking
// order when it stays on the same layer), so selection survives a refresh.
func autoPlace(p *patch.Patch, l *showLayout, cell, gap float64) (layoutDeriveResult, error) {
	res := layoutDeriveResult{Placed: make([]layoutDerivePlaced, 0), Moved: make([]layoutDerivePlaced, 0),
		ReturnedToUnplaced: make([]string, 0), NotPlaced: make([]layoutUnplaced, 0)}
	prev := map[string]layoutItem{}
	kept := make([]layoutItem, 0, len(l.Items))
	for _, it := range l.Items {
		if it.Kind == "entry" && it.Mode == modeAuto {
			prev[it.Ref] = it
			continue
		}
		kept = append(kept, it)
	}
	l.Items = kept
	res.Kept = len(kept)
	hasItem := map[string]bool{}
	for _, it := range l.Items {
		if it.Kind == "entry" {
			hasItem[it.Ref] = true
		}
	}
	todo := make([]patch.Entry, 0, len(p.Entries))
	for _, e := range p.Entries {
		switch {
		case hasItem[e.ID]:
		case e.Location.Known:
			todo = append(todo, e)
		default:
			if _, was := prev[e.ID]; was {
				res.ReturnedToUnplaced = append(res.ReturnedToUnplaced, e.ID)
			}
			res.NotPlaced = append(res.NotPlaced, layoutUnplaced{EntryID: e.ID, Reason: "no-location", ItemID: entryItemID(e.ID)})
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
			ly := layoutLayer{ID: newLayoutID("ly"), Name: name, ZKnown: true, ZMin: c.zMin, ZMax: c.zMax}
			newLayers = append(newLayers, ly)
			id = ly.ID
		}
		for _, eid := range c.ids {
			layerOf[eid] = id
		}
	}
	if len(l.Layers)+len(newLayers) > layoutMaxLayers {
		return res, layoutErr(http.StatusBadRequest, "Deriving would need %d layers and a layout holds at most %d; derive again with a larger layer height gap.", len(l.Layers)+len(newLayers), layoutMaxLayers)
	}
	insertLayers(l, newLayers)
	res.LayersCreated = len(newLayers)

	type cellKey struct {
		layer    string
		col, row int
	}
	taken := map[cellKey]bool{}
	for _, it := range l.Items {
		if it.Kind == "object" {
			continue
		}
		for c := it.Col; c < it.Col+it.W; c++ {
			for r := it.Row; r < it.Row+it.H; r++ {
				taken[cellKey{it.Layer, c, r}] = true
			}
		}
	}
	inGrid := func(c, r int) bool {
		return c >= -layoutMaxCell && c <= layoutMaxCell && r >= -layoutMaxCell && r <= layoutMaxCell
	}
	for k, e := range todo {
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
				if _, was := prev[e.ID]; was {
					res.ReturnedToUnplaced = append(res.ReturnedToUnplaced, e.ID)
				}
				res.NotPlaced = append(res.NotPlaced, layoutUnplaced{EntryID: e.ID, Reason: "no-free-cell", ItemID: entryItemID(e.ID)})
				continue
			}
		}
		taken[cellKey{layer, col, row}] = true
		it := layoutItem{ID: entryItemID(e.ID), Kind: "entry", Ref: e.ID, Layer: layer, Col: col, Row: row, W: 1, H: 1, Rot: 0,
			Mode: modeAuto, Order: 1<<30 + k}
		old, was := prev[e.ID]
		if was {
			it.ID = old.ID
			if old.Layer == layer {
				it.Order = old.Order
			}
		}
		l.Items = append(l.Items, it)
		pl := layoutDerivePlaced{EntryID: e.ID, ItemID: it.ID, Layer: layer, Col: col, Row: row, Displaced: displaced}
		switch {
		case !was:
			res.Placed = append(res.Placed, pl)
		case old.Layer != layer || old.Col != col || old.Row != row:
			res.Moved = append(res.Moved, pl)
		default:
			res.Unchanged++
		}
	}
	renumberItemOrders(l)
	return res, nil
}

// settleAuto applies automatic placement after items were handed back to
// it: a derived layout re-runs autoPlace on its saved settings; otherwise
// there is nothing to derive from, so auto fixture items are dropped and
// those fixtures fall back to the Unplaced grid.
func settleAuto(p *patch.Patch, l *showLayout) error {
	if l.Derived {
		_, err := autoPlace(p, l, l.CellMM, l.ZGapMM)
		return err
	}
	kept := make([]layoutItem, 0, len(l.Items))
	for _, it := range l.Items {
		if it.Kind != "entry" || it.Mode != modeAuto {
			kept = append(kept, it)
		}
	}
	l.Items = kept
	return nil
}

// insertLayers puts layers just before the Unplaced layer (at the end if
// there is none) and renumbers Order.
func insertLayers(l *showLayout, layers []layoutLayer) {
	renumberLayers(l)
	at := findLayer(l, unplacedLayerID)
	if at < 0 {
		at = len(l.Layers)
	}
	out := make([]layoutLayer, 0, len(l.Layers)+len(layers))
	out = append(append(append(out, l.Layers[:at]...), layers...), l.Layers[at:]...)
	for i := range out {
		out[i].Order = i
	}
	l.Layers = out
}

func countKind(l *showLayout, kind string) int {
	n := 0
	for _, it := range l.Items {
		if it.Kind == kind {
			n++
		}
	}
	return n
}

func objectTypeList() string {
	names := make([]string, 0, len(layoutObjectTypes))
	for _, t := range layoutObjectTypes {
		names = append(names, t.Type)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

func cloneGeometry(g *layoutGeometry) *layoutGeometry {
	if g == nil {
		return nil
	}
	c := *g
	for _, p := range []**float64{&c.Col2, &c.Row2, &c.W, &c.H} {
		if *p != nil {
			v := **p
			*p = &v
		}
	}
	return &c
}

// findObject finds an object item for an object-* action.
func findObject(l *showLayout, id, verb string) (int, layoutObjectType, error) {
	i := findItem(l, id)
	if i < 0 {
		return -1, layoutObjectType{}, layoutErr(http.StatusNotFound, "That object is not on the layout.")
	}
	if l.Items[i].Kind != "object" {
		return -1, layoutObjectType{}, layoutErr(http.StatusBadRequest, "That item is not an object; move fixtures and groups with move.")
	}
	t, ok := objectTypeOf(l.Items[i].ObjectType)
	if !ok {
		return -1, layoutObjectType{}, layoutErr(http.StatusConflict, "This object's type is not known to this build, so it cannot %s it; remove it instead.", verb)
	}
	return i, t, nil
}

// checkObject validates an object item's text, rotation and geometry for
// its type and sets the covered cells (Col/Row/W/H). Covered cells run from
// floor(min) to ceil(max) - 1 on each axis, at least one cell.
func checkObject(t layoutObjectType, it *layoutItem) error {
	if len([]rune(it.Text)) > layoutNameMax {
		return layoutErr(http.StatusBadRequest, "Object text must be at most %d characters.", layoutNameMax)
	}
	if t.TextRequired && it.Text == "" {
		return layoutErr(http.StatusBadRequest, "A %s needs text of 1 to %d characters.", t.Type, layoutNameMax)
	}
	if it.Rot != 0 && it.Rot != 90 && it.Rot != 180 && it.Rot != 270 {
		return layoutErr(http.StatusBadRequest, "Rotation must be 0, 90, 180 or 270 degrees.")
	}
	if !t.Rotatable && it.Rot != 0 {
		return layoutErr(http.StatusBadRequest, "A %s cannot be rotated; its geometry sets its direction.", t.Type)
	}
	g := it.Geometry
	if g == nil {
		return layoutErr(http.StatusBadRequest, "A %s needs its geometry.", t.Type)
	}
	hasLine, hasRect := g.Col2 != nil || g.Row2 != nil, g.W != nil || g.H != nil
	xs, ys := []float64{g.Col}, []float64{g.Row}
	switch t.Shape {
	case "line":
		if g.Col2 == nil || g.Row2 == nil || hasRect {
			return layoutErr(http.StatusBadRequest, "A %s is a line: give col, row, col2 and row2, and no w or h.", t.Type)
		}
		if *g.Col2 == g.Col && *g.Row2 == g.Row {
			return layoutErr(http.StatusBadRequest, "A %s needs two different end points.", t.Type)
		}
		xs, ys = append(xs, *g.Col2), append(ys, *g.Row2)
	case "rect":
		if g.W == nil || g.H == nil || hasLine {
			return layoutErr(http.StatusBadRequest, "A %s is a rectangle: give col, row, w and h, and no col2 or row2.", t.Type)
		}
		if !(*g.W > 0) || !(*g.H > 0) {
			return layoutErr(http.StatusBadRequest, "A %s's width and height must each be at least half a cell.", t.Type)
		}
		xs, ys = append(xs, g.Col+*g.W), append(ys, g.Row+*g.H)
		if !onStep(*g.W) || !onStep(*g.H) {
			return layoutErr(http.StatusBadRequest, "Object coordinates must be in half cells, for example 2 or 2.5.")
		}
	default: // point
		if hasLine || hasRect {
			return layoutErr(http.StatusBadRequest, "A %s is a point: give only col and row.", t.Type)
		}
	}
	for _, v := range append(append([]float64{}, xs...), ys...) {
		if !onStep(v) {
			return layoutErr(http.StatusBadRequest, "Object coordinates must be in half cells, for example 2 or 2.5.")
		}
		if v < -layoutMaxCell || v > layoutMaxCell {
			return layoutErr(http.StatusBadRequest, "The object must lie within columns and rows %d to %d.", -layoutMaxCell, layoutMaxCell)
		}
	}
	cover := func(vs []float64) (int, int) {
		lo, hi := vs[0], vs[0]
		for _, v := range vs {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		start := int(math.Floor(lo))
		return start, max(1, int(math.Ceil(hi))-start)
	}
	it.Col, it.W = cover(xs)
	it.Row, it.H = cover(ys)
	return nil
}

// onStep reports whether v is a finite multiple of layoutObjectStep.
func onStep(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	q := v / layoutObjectStep
	return q == math.Round(q)
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
