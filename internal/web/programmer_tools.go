package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"benny512/internal/patch"
)

// Console-lite chunk C4b: the programmer's tools over HTTP (behind the show
// guard like every /api/programmer route; X-Benny-Programmer optional, 409
// when stale; every change bumps the revision and broadcasts).
//
//	POST /api/programmer/highlight        {highlight?, lowlight?, lowlightPercent?, step?: next|previous|all}
//	POST /api/programmer/locate           {targets?}
//	POST /api/programmer/fan              {targets?, attribute, function|functionName|functionIndex?,
//	                                       shape: linear|reverse|mirror|edges-in, from: {value}, to: {value}}
//	POST /api/programmer/groups/{action}  store {name} | update {id} | merge {id} | rename {id, name} | delete {id}
//	POST /api/programmer/presets/{action} store {name, family} | overwrite {id} | rename {id, name}
//	                                      | delete {id} | recall {id}
//
// {value} is one of {dmx}, {fraction}, {physical}, {set}, {slot} (C4a).

// Limits for stored programmer data, per show.
const (
	maxGroups            = 100 // unchanged from the Rig Check groups limit
	maxPresetsPerFamily  = 100
	maxPresetValues      = 20000
	programmerNameMaxLen = 80
)

type programmerHighlightRequest struct {
	Highlight       *bool `json:"highlight"`
	Lowlight        *bool `json:"lowlight"`
	LowlightPercent *int  `json:"lowlightPercent"`
	// Step (I2d): "next" | "previous" | "all" — Highlight Previous/Next
	// through the selection in its order (component-specs §15).
	Step string `json:"step"`
}

func (s *Server) handleProgrammerHighlight(w http.ResponseWriter, r *http.Request) {
	var req programmerHighlightRequest
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
	if _, err := s.Programmer.SetHighlight(req.Highlight, req.Lowlight, req.LowlightPercent, req.Step, expected); err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	s.broadcastProgrammer()
	s.writeProgrammerJSON(w, http.StatusOK, s.programmerView())
}

type programmerLocateRequest struct {
	Targets []patch.ProgTarget `json:"targets"`
}

func (s *Server) handleProgrammerLocate(w http.ResponseWriter, r *http.Request) {
	var req programmerLocateRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	expected, err := programmerExpected(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.syncProgrammer(false)
	res, err := s.Programmer.Locate(req.Targets, expected)
	if err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	s.broadcastProgrammer()
	s.writeProgrammerJSON(w, http.StatusOK, res)
}

type programmerValueSpec struct {
	DMX      *float64 `json:"dmx"`
	Fraction *float64 `json:"fraction"`
	Physical *float64 `json:"physical"`
	Set      *string  `json:"set"`
	Slot     *int     `json:"slot"`
}

func (v programmerValueSpec) spec() patch.ProgValueSpec {
	return patch.ProgValueSpec{DMX: v.DMX, Fraction: v.Fraction, Physical: v.Physical, Set: v.Set, Slot: v.Slot}
}

type programmerFanRequest struct {
	Targets       []patch.ProgTarget  `json:"targets"`
	Attribute     string              `json:"attribute"`
	Function      string              `json:"function"`
	FunctionName  string              `json:"functionName"`
	FunctionIndex *int                `json:"functionIndex"`
	Shape         string              `json:"shape"`
	From          programmerValueSpec `json:"from"`
	To            programmerValueSpec `json:"to"`
}

func (s *Server) handleProgrammerFan(w http.ResponseWriter, r *http.Request) {
	var req programmerFanRequest
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
	res, err := s.Programmer.Fan(patch.ProgFanRequest{Targets: req.Targets, Attribute: req.Attribute, Function: req.Function,
		FunctionName: req.FunctionName, FunctionIndex: req.FunctionIndex, Shape: req.Shape, From: req.From.spec(), To: req.To.spec()}, expected)
	if err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	s.broadcastProgrammer()
	s.writeProgrammerJSON(w, http.StatusOK, res)
}

// --- stored groups and presets --------------------------------------------------

type programmerStoreRequest struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Family string `json:"family"`
}

// storeStatus carries an HTTP status out of a workspace mutation.
type storeStatus struct {
	status int
	msg    string
}

func (e storeStatus) Error() string { return e.msg }

func storeErr(status int, format string, a ...any) error {
	return storeStatus{status: status, msg: fmt.Sprintf(format, a...)}
}

// mutateWorkspace edits the active show's workspace in one store write,
// refusing a workspace that does not parse (strictWorkspace).
func (s *Server) mutateWorkspace(fn func(ws *showWorkspace) error) error {
	if _, ok := s.PatchStore.Get(); !ok {
		return storeErr(http.StatusBadRequest, "Create a show first.")
	}
	_, err := s.PatchStore.Mutate(func(p *patch.Patch) error {
		ws, err := strictWorkspace(*p)
		if err != nil {
			return storeErr(http.StatusConflict, "%s", err.Error())
		}
		if err := fn(&ws); err != nil {
			return err
		}
		b, err := json.Marshal(ws)
		if err != nil {
			return err
		}
		p.Workspace = b
		return nil
	})
	return err
}

func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	var st storeStatus
	var bad patch.ProgrammerRequestError
	switch {
	case errors.As(err, &st):
		writeError(w, st.status, err)
	case errors.As(err, &bad), errors.Is(err, patch.ErrProgrammerStale):
		s.writeProgrammerError(w, err)
	default:
		writePatchStoreError(w, err)
	}
}

func storeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > programmerNameMaxLen {
		return "", storeErr(http.StatusBadRequest, "A name is 1 to %d characters.", programmerNameMaxLen)
	}
	return name, nil
}

func (s *Server) handleProgrammerGroups(w http.ResponseWriter, r *http.Request) {
	var req programmerStoreRequest
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
	if err := s.Programmer.CheckRevision(expected); err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	action := r.PathValue("action")
	members := s.Programmer.Selection()
	err = s.mutateWorkspace(func(ws *showWorkspace) error {
		find := func() (int, error) {
			for i, g := range ws.Groups {
				if g.ID == req.ID {
					return i, nil
				}
			}
			return -1, storeErr(http.StatusNotFound, "There is no stored group with that id; refresh the groups.")
		}
		needSelection := func() error {
			if len(members) == 0 {
				return storeErr(http.StatusBadRequest, "Nothing is selected. Select the fixtures and cells the group should hold.")
			}
			return nil
		}
		switch action {
		case "store":
			name, err := storeName(req.Name)
			if err != nil {
				return err
			}
			if err := needSelection(); err != nil {
				return err
			}
			if len(ws.Groups) >= maxGroups {
				return storeErr(http.StatusBadRequest, "This show already has %d groups, the limit; delete one first.", maxGroups)
			}
			g := savedGroup{ID: fmt.Sprintf("w-%d", time.Now().UnixNano()), Name: name, Members: members}
			normalizeGroup(&g)
			ws.Groups = append(ws.Groups, g)
		case "update":
			i, err := find()
			if err != nil {
				return err
			}
			if err := needSelection(); err != nil {
				return err
			}
			ws.Groups[i].Members = members
			normalizeGroup(&ws.Groups[i])
		case "merge":
			// I2d §14: add the selection to a stored group, after its own
			// members, in selection order, without duplicates.
			i, err := find()
			if err != nil {
				return err
			}
			if err := needSelection(); err != nil {
				return err
			}
			have := map[patch.ProgTarget]bool{}
			for _, m := range ws.Groups[i].Members {
				have[m] = true
			}
			for _, m := range members {
				if !have[m] {
					have[m] = true
					ws.Groups[i].Members = append(ws.Groups[i].Members, m)
				}
			}
			normalizeGroup(&ws.Groups[i])
		case "rename":
			i, err := find()
			if err != nil {
				return err
			}
			name, err := storeName(req.Name)
			if err != nil {
				return err
			}
			ws.Groups[i].Name = name
		case "delete":
			i, err := find()
			if err != nil {
				return err
			}
			ws.Groups = append(ws.Groups[:i], ws.Groups[i+1:]...)
			// As workspace delete-group: no placement of it stays on the layout.
			kept := make([]layoutItem, 0, len(ws.Layout.Items))
			for _, it := range ws.Layout.Items {
				if it.Kind != "group" || it.Ref != req.ID {
					kept = append(kept, it)
				}
			}
			ws.Layout.Items = kept
		default:
			return storeErr(http.StatusNotFound, "Unknown group action %q; use store, update, merge, rename or delete.", action)
		}
		return nil
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.syncProgrammer(false)
	s.writeProgrammerJSON(w, http.StatusOK, s.programmerView())
}

func (s *Server) handleProgrammerPresets(w http.ResponseWriter, r *http.Request) {
	var req programmerStoreRequest
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
	action := r.PathValue("action")
	if action == "recall" {
		p, _ := s.PatchStore.Get()
		ws, err := strictWorkspace(p)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		for _, pr := range ws.ProgrammerPresets {
			if pr.ID != req.ID {
				continue
			}
			res, err := s.Programmer.RecallPreset(patch.AttributeGroup(pr.Family), pr.Values, expected)
			if err != nil {
				s.writeProgrammerError(w, err)
				return
			}
			s.broadcastProgrammer()
			s.writeProgrammerJSON(w, http.StatusOK, res)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("There is no stored preset with that id; refresh the presets."))
		return
	}
	if err := s.Programmer.CheckRevision(expected); err != nil {
		s.writeProgrammerError(w, err)
		return
	}
	capture := func(family string) ([]patch.PresetValue, error) {
		vals, err := s.Programmer.CapturePreset(patch.AttributeGroup(family))
		if err != nil {
			return nil, err
		}
		if len(vals) > maxPresetValues {
			return nil, storeErr(http.StatusBadRequest, "A preset holds at most %d channel values; select fewer fixtures.", maxPresetValues)
		}
		return vals, nil
	}
	err = s.mutateWorkspace(func(ws *showWorkspace) error {
		find := func() (int, error) {
			for i, pr := range ws.ProgrammerPresets {
				if pr.ID == req.ID {
					return i, nil
				}
			}
			return -1, storeErr(http.StatusNotFound, "There is no stored preset with that id; refresh the presets.")
		}
		now := time.Now().UTC()
		switch action {
		case "store":
			name, err := storeName(req.Name)
			if err != nil {
				return err
			}
			vals, err := capture(req.Family)
			if err != nil {
				return err
			}
			n := 0
			for _, pr := range ws.ProgrammerPresets {
				if pr.Family == req.Family {
					n++
				}
			}
			if n >= maxPresetsPerFamily {
				return storeErr(http.StatusBadRequest, "This show already has %d %s presets, the limit; delete one first.", maxPresetsPerFamily, req.Family)
			}
			ws.ProgrammerPresets = append(ws.ProgrammerPresets, programmerPreset{ID: fmt.Sprintf("pp-%d", time.Now().UnixNano()),
				Name: name, Family: req.Family, CreatedAt: now, UpdatedAt: now, Values: vals})
		case "overwrite":
			i, err := find()
			if err != nil {
				return err
			}
			vals, err := capture(ws.ProgrammerPresets[i].Family)
			if err != nil {
				return err
			}
			ws.ProgrammerPresets[i].Values, ws.ProgrammerPresets[i].UpdatedAt = vals, now
		case "rename":
			i, err := find()
			if err != nil {
				return err
			}
			name, err := storeName(req.Name)
			if err != nil {
				return err
			}
			ws.ProgrammerPresets[i].Name, ws.ProgrammerPresets[i].UpdatedAt = name, now
		case "delete":
			i, err := find()
			if err != nil {
				return err
			}
			ws.ProgrammerPresets = append(ws.ProgrammerPresets[:i], ws.ProgrammerPresets[i+1:]...)
		default:
			return storeErr(http.StatusNotFound, "Unknown preset action %q; use store, overwrite, rename, delete or recall.", action)
		}
		return nil
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.syncProgrammer(false)
	s.writeProgrammerJSON(w, http.StatusOK, s.programmerView())
}
