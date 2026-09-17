package web

import (
	"net/http"
	"sort"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/autoread"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
)

type devicePortSummary struct {
	NodeIP      string    `json:"nodeIp"`
	PortAddress uint16    `json:"portAddress"`
	Advertised  int       `json:"advertised"`
	Answered    int       `json:"answered"`
	Pending     int       `json:"pending"`
	NoResponse  int       `json:"noResponse"`
	NotRead     int       `json:"notRead"`
	Complete    bool      `json:"complete"`
	Updated     time.Time `json:"updated"`
}

// Scope is the latest cached ToD, not the registry's accumulated history.
// BindIndex is deliberately absent: one IP/Port-Address is one output path.
func summarizeDevicePort(td session.ToDSnapshot, fixtures []registry.Fixture, state func(registry.Fixture) autoread.State) devicePortSummary {
	out := devicePortSummary{NodeIP: td.IP.String(), PortAddress: td.PortAddress, Complete: td.Complete, Updated: td.Updated}
	type evidence struct{ answered, pending, gaveUp bool }
	byUID := make(map[rdm.UID]evidence)
	for _, f := range fixtures {
		if f.Node.IP != td.IP || f.Port.RawValue() != td.PortAddress {
			continue
		}
		e := byUID[f.UID]
		e.answered = e.answered || f.HasResponded
		s := state(f)
		e.pending = e.pending || s == autoread.StatePending || s == autoread.StateReading
		e.gaveUp = e.gaveUp || s == autoread.StateGaveUp
		byUID[f.UID] = e
	}
	seen := make(map[rdm.UID]bool)
	for _, uid := range td.UIDs {
		if seen[uid] {
			continue
		}
		seen[uid] = true
		out.Advertised++
		e := byUID[uid]
		switch {
		case e.answered:
			out.Answered++
		case e.pending:
			out.Pending++
		case e.gaveUp:
			out.NoResponse++
		default:
			out.NotRead++
		}
	}
	return out
}

func (s *Server) handleDevicePorts(w http.ResponseWriter, r *http.Request) {
	fixtures := s.Registry.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)
	out := make([]devicePortSummary, 0)
	for _, td := range s.RDM.ToDSnapshots() {
		out = append(out, summarizeDevicePort(td, fixtures, func(f registry.Fixture) autoread.State {
			state, _ := s.AutoRead.State(autoread.KeyFor(f.Node.IP, f.Node.BindIndex, f.Port, f.UID))
			return state
		}))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeIP != out[j].NodeIP {
			return out[i].NodeIP < out[j].NodeIP
		}
		return out[i].PortAddress < out[j].PortAddress
	})
	writeJSON(w, http.StatusOK, out)
}
