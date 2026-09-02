package library

import (
	"fmt"
	"time"
)

// This file implements importing a library document handed over by someone
// else. The contract the owner asked for, in three parts:
//
//	merge-or-replace, USER-CHOSEN   — Mode is required and must be spelled
//	                                  out; there is no default, because a
//	                                  defaulted destructive mode is how
//	                                  people lose libraries.
//	never silently destructive      — ModeReplace additionally requires an
//	                                  explicit confirmation at the HTTP
//	                                  layer (Apply-to-confirm, see
//	                                  internal/web/library.go) and reports
//	                                  exactly how many records it removed.
//	reports what it did             — per record: added / updated /
//	                                  skipped, with a reason.
//
// And the fourth, non-negotiable one: a malformed or wrong-schema document
// is rejected WHOLE. validate() (store.go) runs against the decoded
// document before the store is touched at all, so there is no code path
// that can leave a half-imported library behind.

// ImportMode selects merge vs replace. Deliberately a string with no zero-
// value meaning: an unset mode is an error, never a silent default.
type ImportMode string

// Import modes.
const (
	// ModeMerge folds the incoming records into the existing library.
	// Nothing is ever removed. This is the safe mode and the one a
	// coworker's file is normally taken in with.
	ModeMerge ImportMode = "merge"
	// ModeReplace discards the entire existing library and installs the
	// incoming one. Requires explicit confirmation at the HTTP layer.
	ModeReplace ImportMode = "replace"
)

// ImportAction is what happened to one record.
type ImportAction string

// Import actions.
const (
	ActionAdded   ImportAction = "added"
	ActionUpdated ImportAction = "updated"
	ActionSkipped ImportAction = "skipped"
)

// ImportRecordResult is the per-record line of the import report — enough
// for a UI to show "17 added, 3 updated, 41 unchanged" and to let the user
// expand and see exactly which.
type ImportRecordResult struct {
	Key          string       `json:"key"`
	Manufacturer string       `json:"manufacturer"`
	Model        string       `json:"model"`
	Action       ImportAction `json:"action"`
	// Detail explains a non-obvious action in one human sentence (why a
	// record was skipped, what an update actually changed). Optional.
	Detail string `json:"detail,omitempty"`
}

// ImportResult is the whole import report.
type ImportResult struct {
	Mode ImportMode `json:"mode"`
	// Counts. Deliberately NO `omitempty` on any of them: "0 added" is the
	// single most important thing this response ever has to say (it is how
	// a user learns their import did nothing), and omitting it would leave
	// the UI rendering `undefined`.
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	// Removed is how many pre-existing records a replace threw away.
	// Always 0 for a merge. No `omitempty` for the same reason.
	Removed int `json:"removed"`
	// Total is how many records the incoming document contained.
	Total int `json:"total"`
	// Records is one line per incoming record, in the document's order.
	// Never nil — always `make([]ImportRecordResult, 0)`.
	Records []ImportRecordResult `json:"records"`
}

// Import validates doc and applies it to the store under a single lock, so
// two concurrent imports can never interleave. It returns an error (and
// changes nothing at all) if doc is not a well-formed library document of a
// schema this build understands; see validate().
//
// Note the ordering: validate first, THEN take the lock and mutate. A
// document that fails validation never reaches the store, which is what
// makes "reject rather than half-import" a structural property.
func (st *Store) Import(doc Library, mode ImportMode) (ImportResult, error) {
	switch mode {
	case ModeMerge, ModeReplace:
	default:
		return ImportResult{}, fmt.Errorf("import mode must be %q or %q, got %q", ModeMerge, ModeReplace, mode)
	}
	if err := validate(&doc); err != nil {
		return ImportResult{}, err
	}
	// Deliberately NOT migrate(&doc): migrate sorts records by key (it is
	// the load-from-disk path, where a canonical order makes the file
	// diffable), and the per-record report below must come back in the
	// order the user's file actually listed them so they can follow it
	// down the page. Every record is normalized individually by
	// upsertLocked instead, and the schema/format normalization migrate
	// would do is irrelevant here — validate has already established both.
	now := time.Now()
	res := ImportResult{Mode: mode, Total: len(doc.Records), Records: make([]ImportRecordResult, 0, len(doc.Records))}

	st.mu.Lock()
	defer st.mu.Unlock()

	if mode == ModeReplace {
		res.Removed = len(st.lib.Records)
		created := st.lib.CreatedAt
		st.lib = newLibrary()
		if !created.IsZero() {
			// Preserve when the owner's library first came into existence
			// across a replace — the records are new, the library is not.
			st.lib.CreatedAt = created
		}
	}

	for _, r := range doc.Records {
		stored, outcome := st.upsertLocked(r, now)
		line := ImportRecordResult{Key: stored.Key, Manufacturer: stored.Manufacturer, Model: stored.Model}
		switch outcome {
		case OutcomeAdded:
			line.Action = ActionAdded
			res.Added++
		case OutcomeUpdated:
			line.Action = ActionUpdated
			res.Updated++
		default:
			line.Action = ActionSkipped
			line.Detail = "already present and identical"
			res.Skipped++
		}
		if outcome != OutcomeAdded && stored.Key != KeyFor(r.Manufacturer, r.Model) {
			line.Detail = fmt.Sprintf("merged into existing record %q / %q", stored.Manufacturer, stored.Model)
		}
		res.Records = append(res.Records, line)
	}

	// One persist for the whole import rather than one per record: an
	// import is a single user action, and writing the file N times would
	// leave N chances for a crash to catch it mid-import. upsertLocked's
	// own touchLocked calls have already written intermediate states, so
	// this final call is what guarantees the on-disk file matches the
	// finished result even if the last record was a no-op skip.
	st.touchLocked(now)
	return res, nil
}
