// Export support for the RDM-only capture ring (report task item 3,
// priority ask: "exportable to a file the owner can hand to the architect").
//
//	GET /api/capture/export?format=json|txt&uid=<uid>&pid=<hex>&cc=<mnemonic>&dir=in|out
//
// format is required (json|txt). Every other query param narrows the
// export via the same capture.Filter dimensions the RDM snapshot endpoint
// uses (see captureFilterFromQuery in server.go) — in particular, ?uid=
// scopes the export to one device's exchanges, which is what the device
// detail panel's Export button uses.
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"benny512/internal/capture"
)

func (s *Server) handleCaptureExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "txt" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("format must be json or txt, got %q", format))
		return
	}
	f, err := captureFilterFromQuery(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	entries := s.RDMCapture.Snapshot(f, 0) // 0 = every retained entry
	now := time.Now()
	scope := "all RDM/ToD traffic"
	if f.UID != "" {
		scope = "device " + f.UID + " exchanges only"
	}
	header := exportHeader{
		AppVersion: AppVersion, GeneratedAt: now, NIC: nonEmptyStr(s.NIC, "(unset)"),
		Scope: scope, EntryCount: len(entries), BufferCapacity: s.RDMCapture.Capacity(),
	}

	filename := exportFilename(format, now, f.UID)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		doc := exportDoc{Header: header, Entries: entries}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(doc)
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		writeExportText(w, header, entries)
	}
}

type exportHeader struct {
	AppVersion     string    `json:"appVersion"`
	GeneratedAt    time.Time `json:"generatedAt"`
	NIC            string    `json:"nic"`
	Scope          string    `json:"scope"`
	EntryCount     int       `json:"entryCount"`
	BufferCapacity int       `json:"bufferCapacity"`
}

type exportDoc struct {
	Header  exportHeader    `json:"header"`
	Entries []capture.Entry `json:"entries"`
}

func nonEmptyStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// exportFilename builds a sensible, timestamped download name (report task:
// "a sensible filename incl. timestamp").
func exportFilename(format string, at time.Time, uid string) string {
	stamp := at.Format("20060102_150405")
	scope := "rdm-capture"
	if uid != "" {
		scope = "rdm-capture_" + sanitizeForFilename(uid)
	}
	return fmt.Sprintf("benny512_%s_%s.%s", scope, stamp, format)
}

func sanitizeForFilename(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
}

// --- request/response pairing for the TXT transcript -----------------------

// rdmExchange groups a request with its matching response, when one exists
// in the exported window (report task: "request/response paired view where
// possible (match by TN+UID)"). Entries with no RDM detail (ToD packets,
// undecodable bytes) always stand alone.
type rdmExchange struct {
	Request  *capture.Entry
	Response *capture.Entry
}

// pairExchanges groups entries in time order. Pairing key is
// (TransactionNumber, the "other" UID) — a request's key uses its
// destination UID, a response's key uses its source UID, so a request and
// its reply share a key. Retransmits reuse the request's TN (matches the
// original), while ACK_TIMER re-issues and ACK_OVERFLOW continuations get a
// fresh TN (session.RDMController's documented behaviour) — so those show
// up as their own separate exchange rather than being folded into the
// first attempt, which keeps each printed block's round-trip time accurate
// for the specific attempt it describes.
func pairExchanges(entries []capture.Entry) []rdmExchange {
	type key struct {
		tn  byte
		uid string
	}
	pending := make(map[key]*rdmExchange)
	var order []*rdmExchange

	for i := range entries {
		e := &entries[i]
		if e.RDM == nil {
			order = append(order, &rdmExchange{Request: e})
			continue
		}
		if !e.RDM.IsResponse {
			ex := &rdmExchange{Request: e}
			pending[key{tn: e.RDM.TransactionNumber, uid: e.RDM.DestUID}] = ex
			order = append(order, ex)
			continue
		}
		k := key{tn: e.RDM.TransactionNumber, uid: e.RDM.SourceUID}
		if ex, ok := pending[k]; ok && ex.Response == nil {
			ex.Response = e
			delete(pending, k)
			continue
		}
		order = append(order, &rdmExchange{Response: e})
	}

	out := make([]rdmExchange, len(order))
	for i, p := range order {
		out[i] = *p
	}
	return out
}

func writeExportText(w io.Writer, h exportHeader, entries []capture.Entry) {
	fmt.Fprintln(w, "Benny512 RDM Capture Export")
	fmt.Fprintf(w, "App version: %s\n", h.AppVersion)
	fmt.Fprintf(w, "Generated:   %s\n", h.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "NIC:         %s\n", h.NIC)
	fmt.Fprintf(w, "Scope:       %s\n", h.Scope)
	fmt.Fprintf(w, "Entries:     %d (RDM-only buffer capacity %d)\n", h.EntryCount, h.BufferCapacity)
	fmt.Fprintln(w, strings.Repeat("=", 78))
	fmt.Fprintln(w)

	if len(entries) == 0 {
		fmt.Fprintln(w, "(no RDM/ToD traffic captured yet)")
		return
	}

	exchanges := pairExchanges(entries)
	for i, ex := range exchanges {
		fmt.Fprintf(w, "--- Exchange %d of %d ---\n", i+1, len(exchanges))
		if ex.Request != nil {
			fmt.Fprint(w, capture.FormatEntryText(*ex.Request))
		}
		if ex.Response != nil {
			fmt.Fprint(w, capture.FormatEntryText(*ex.Response))
			if ex.Request != nil {
				rtt := ex.Response.Time.Sub(ex.Request.Time)
				fmt.Fprintf(w, "  round-trip: %s\n", rtt)
			}
		} else if ex.Request != nil && ex.Request.RDM != nil && !ex.Request.RDM.IsResponse {
			fmt.Fprintln(w, "  (no response captured for this request)")
		}
		fmt.Fprintln(w, strings.Repeat("-", 78))
		fmt.Fprintln(w)
	}
}
