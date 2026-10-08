package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"benny512/internal/patch"
	"benny512/internal/session"
)

// Console-lite chunk C5: Rig Check folded into the Console as the TESTS
// layer plus TEST SEQUENCES (owner decision 2026-10-06; shape approved by the
// owner: the Rig Check engine acts on whatever is selected, under manual
// values, with chainable sequences stepped by Next/Back or auto-advance).
//
//	GET  /api/tests[?kind=selection|group|layer|all&group=&layer=]
//	     -> testsViewJSON: revision, output, scope, targets, available tests
//	        (for the current scope, or for the scope in the query), the
//	        ad-hoc tests, the running tests' status, stored sequences, run state
//	POST /api/tests/set              {tests: [test], scope?: {kind, group, layer}}
//	POST /api/tests/clear            {}  (also stops a running sequence)
//	POST /api/tests/fade             {fadeMs: 0-30000}
//	POST /api/tests/sequence-save    {id?, name, steps: [step]}  (id: overwrite)
//	POST /api/tests/sequence-rename  {id, name}
//	POST /api/tests/sequence-delete  {id}
//	POST /api/tests/run-start        {id, step?}
//	POST /api/tests/run-next | run-back | run-pause | run-resume | run-stop  {}
//	POST /api/tests/run-jump         {step}
//
// test = Rig Check's test shape (patternTestRequest: kind, rateHz, min, max,
// target, direction, value, on, waveform, offsetMin, offsetMax).
// step = {name, tests: [test], scope: {kind, group, layer}, fadeMs: n|null,
// advance: {mode: "manual"|"auto", seconds}}.
//
// Every route is behind the show guard (X-Benny-Show). Every response carries
// the tests revision in X-Benny-Tests; a POST that sends X-Benny-Tests is
// refused with 409 when it is not current. Every change — including an
// auto-advance and a scope that moved because the selection or the show
// changed — bumps the revision and broadcasts {"type":"tests","revision":N}.
//
// Rules (C5):
//   - SCOPE: "selection" (the default) is the programmer selection, fixtures
//     and cells, followed live: select something else and the tests move to
//     it. "group" is a stored group's members, "layer" a layout layer's
//     fixtures in reading order, "all" every patched fixture in patch order.
//     Group, layer and all are re-resolved whenever the show changes.
//   - Phases follow the scope's order.
//   - Nothing reaches the wire unless the master output is armed (C3).
//   - One sequence runs at a time. While it runs it owns the tests layer:
//     ad-hoc set is refused (409) and the running sequence cannot be edited
//     or deleted. Clear and run-stop release the layer.
//   - Next/Back clamp at the ends. The last step's auto-advance ends the run
//     (endedReason "finished") and releases the layer.
//   - Loading a saved test preset (Show tools, /api/patch/workspace/load-preset)
//     drives the Rig Check engine directly and takes the layer back for it: a
//     running sequence ends ("a loaded test preset took over"). (The retired
//     Rig Check screen's endpoints did the same until C7.)

const testsRevisionHeader = "X-Benny-Tests"

// Limits for stored sequences, per show.
const (
	maxTestSequences    = 50
	maxTestSteps        = 64
	maxTestsPerStep     = 32
	minAutoAdvanceSecs  = 0.5
	maxAutoAdvanceSecs  = 3600
	maxTestNameLen      = 80
	testScopeSelection  = "selection"
	testScopeGroup      = "group"
	testScopeLayer      = "layer"
	testScopeAll        = "all"
	testAdvanceManual   = "manual"
	testAdvanceAuto     = "auto"
	errTestsStaleString = "The tests changed in another browser since this one last looked; refresh and try again."
)

var errTestsStale = errors.New(errTestsStaleString)

type testScopeJSON struct {
	Kind  string `json:"kind"`
	Group string `json:"group"`
	Layer string `json:"layer"`
}

type testAdvanceJSON struct {
	Mode    string  `json:"mode"`
	Seconds float64 `json:"seconds"`
}

type testStepJSON struct {
	Name  string               `json:"name"`
	Tests []patternTestRequest `json:"tests"`
	Scope testScopeJSON        `json:"scope"`
	// FadeMS null keeps the fade time in effect; a number sets it as the
	// step starts.
	FadeMS  *int64          `json:"fadeMs"`
	Advance testAdvanceJSON `json:"advance"`
}

type testSequenceJSON struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Steps     []testStepJSON `json:"steps"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

type testsTargetJSON struct {
	EntryID string `json:"entryId"`
	Cell    string `json:"cell"`
	Name    string `json:"name"`
}

type testsEntryJSON struct {
	EntryID       string  `json:"entryId"`
	Cell          string  `json:"cell"`
	Applied       bool    `json:"applied"`
	Inferred      bool    `json:"inferred"`
	DetailMissing bool    `json:"detailMissing"`
	PhaseDegrees  float64 `json:"phaseDegrees"`
}

type testsTestJSON struct {
	patternTestStatusJSON
	Entries []testsEntryJSON `json:"entries"`
}

type testsContestedJSON struct {
	Universe uint16   `json:"universe"`
	Channel  uint16   `json:"channel"`
	EntryID  string   `json:"entryId"`
	Cell     string   `json:"cell"`
	Tests    []string `json:"tests"`
}

type testsRunJSON struct {
	Active       bool            `json:"active"`
	SequenceID   string          `json:"sequenceId"`
	SequenceName string          `json:"sequenceName"`
	Step         int             `json:"step"`
	StepCount    int             `json:"stepCount"`
	StepName     string          `json:"stepName"`
	Paused       bool            `json:"paused"`
	Advance      testAdvanceJSON `json:"advance"`
	// RemainingMs is the time left before an auto step advances; null on a
	// manual step (there is no such time) and when no run is active.
	RemainingMs *int64 `json:"remainingMs"`
	// EndedReason is why the last run ended: "" (none yet), "stopped",
	// "finished", "cleared", "show changed", "a loaded test preset took over".
	EndedReason string `json:"endedReason"`
}

type testsLimitsJSON struct {
	Sequences      int     `json:"sequences"`
	Steps          int     `json:"steps"`
	TestsPerStep   int     `json:"testsPerStep"`
	MinAutoSeconds float64 `json:"minAutoSeconds"`
	MaxAutoSeconds float64 `json:"maxAutoSeconds"`
	MaxFadeMs      int64   `json:"maxFadeMs"`
	NameLength     int     `json:"nameLength"`
}

type testsViewJSON struct {
	Revision uint64           `json:"revision"`
	Output   patch.ProgOutput `json:"output"`
	// Owner is "tests" while this API drives the tests layer, "rigcheck"
	// while the Rig Check engine is driven directly (a loaded test preset;
	// the retired Rig Check screen until C7), "none" otherwise.
	Owner   string              `json:"owner"`
	Scope   testScopeJSON       `json:"scope"`
	Targets []testsTargetJSON   `json:"targets"`
	Ignored []programmerIgnored `json:"ignored"`
	// Note says, in a sentence, why nothing is being tested when the scope
	// resolved to no fixture; "" otherwise.
	Note          string               `json:"note"`
	OutputEnabled bool                 `json:"outputEnabled"`
	FadeMs        int64                `json:"fadeMs"`
	ElapsedMs     int64                `json:"elapsedMs"`
	Available     []availableTestJSON  `json:"available"`
	AdHoc         []patternTestRequest `json:"adHoc"`
	Tests         []testsTestJSON      `json:"tests"`
	Contested     []testsContestedJSON `json:"contested"`
	BaseState     patternBaseStateJSON `json:"baseState"`
	Sequences     []testSequenceJSON   `json:"sequences"`
	Run           testsRunJSON         `json:"run"`
	Limits        testsLimitsJSON      `json:"limits"`
}

// testRun is the one running sequence.
type testRun struct {
	seq      testSequenceJSON // snapshot taken at run-start
	step     int
	paused   bool
	deadline time.Time     // auto step, not paused
	left     time.Duration // auto step, paused
}

// resolvedTarget is one fixture or cell a scope resolved to.
type resolvedTarget struct {
	json   testsTargetJSON
	target patch.TestTarget
}

// testsState is the Tests controller. Its mutex is taken before the
// programmer's, the patch store's and Rig Check's, never after them.
type testsState struct {
	mu       sync.Mutex
	revision uint64
	owns     bool // this API drove the tests layer last
	adHoc    bool
	adTests  []patternTestRequest
	adScope  testScopeJSON
	run      *testRun
	ended    string
	timer    session.Timer
	timerGen uint64
	pushed   string // key of the last push, so an unchanged scope is not re-pushed
	targets  []resolvedTarget
	ignored  []programmerIgnored
	note     string
	scope    testScopeJSON
}

func newTestsState() *testsState {
	return &testsState{revision: uint64(time.Now().UnixMilli()), adScope: testScopeJSON{Kind: testScopeSelection}, scope: testScopeJSON{Kind: testScopeSelection}}
}

// --- scope resolution -----------------------------------------------------------

func normalizeTestScope(sc testScopeJSON) testScopeJSON {
	if sc.Kind == "" {
		sc.Kind = testScopeSelection
	}
	if sc.Kind != testScopeGroup {
		sc.Group = ""
	}
	if sc.Kind != testScopeLayer {
		sc.Layer = ""
	}
	return sc
}

// checkTestScope refuses a scope that cannot be resolved in this show.
func checkTestScope(sc testScopeJSON, ws showWorkspace) error {
	switch sc.Kind {
	case testScopeSelection, testScopeAll:
		return nil
	case testScopeGroup:
		for _, g := range ws.Groups {
			if g.ID == sc.Group {
				return nil
			}
		}
		return errors.New("There is no stored group with that id; refresh the groups.")
	case testScopeLayer:
		l := ws.Layout
		normalizeLayout(&l)
		if findLayer(&l, sc.Layer) >= 0 {
			return nil
		}
		return errors.New("There is no layout layer with that id; refresh the layout.")
	}
	return fmt.Errorf("A test scope is selection, group, layer or all; %q is not one of them.", sc.Kind)
}

// resolveTestScope turns a scope into targets, in the scope's order.
func (s *Server) resolveTestScope(sc testScopeJSON) ([]resolvedTarget, []programmerIgnored, string) {
	p, _ := s.PatchStore.Get()
	ws := workspaceFor(p)
	ignored := make([]programmerIgnored, 0)
	var progTargets []patch.ProgTarget
	note := ""
	switch sc.Kind {
	case testScopeSelection:
		progTargets = s.Programmer.Selection()
		if len(progTargets) == 0 {
			note = "Nothing is selected on the Console, so no fixture is being tested."
		}
	case testScopeGroup:
		var err error
		progTargets, ignored, err = groupTargets(p, ws, sc.Group, s.Programmer.ValidTarget)
		if err != nil {
			return nil, make([]programmerIgnored, 0), "The stored group this scope names no longer exists, so no fixture is being tested."
		}
		if len(progTargets) == 0 {
			note = "The stored group has no fixture in the patch, so no fixture is being tested."
		}
	case testScopeLayer:
		var err error
		progTargets, ignored, err = layerTargets(p, ws, sc.Layer, s.Programmer.ValidTarget)
		if err != nil {
			return nil, make([]programmerIgnored, 0), "The layout layer this scope names no longer exists, so no fixture is being tested."
		}
		if len(progTargets) == 0 {
			note = "Nothing is placed on the layout layer, so no fixture is being tested."
		}
	case testScopeAll:
		for _, e := range p.Entries {
			progTargets = append(progTargets, patch.ProgTarget{EntryID: e.ID})
		}
		if len(progTargets) == 0 {
			note = "The patch is empty, so no fixture is being tested."
		}
	}
	out := make([]resolvedTarget, 0, len(progTargets))
	for _, t := range progTargets {
		i := p.IndexOf(t.EntryID)
		if i < 0 {
			continue
		}
		e := s.withRDMPhaseWeights([]patch.Entry{p.Entries[i]})[0]
		rt := resolvedTarget{json: testsTargetJSON{EntryID: e.ID, Cell: t.Cell, Name: patch.EntryLabel(e)}}
		if t.Cell == "" {
			rt.target = patch.TestTarget{Entry: e}
		} else {
			offs, ok := s.Programmer.CellOffsets(e.ID, t.Cell)
			if !ok {
				ignored = append(ignored, programmerIgnored{EntryID: t.EntryID, Cell: t.Cell, Reason: "This cell is not in the fixture's current profile."})
				continue
			}
			rt.json.Name += " " + cellDisplayName(t.Cell)
			rt.target = patch.CellTestTarget(e, t.Cell, offs)
		}
		out = append(out, rt)
	}
	return out, ignored, note
}

// cellDisplayName is the geometry name of a cell instance ("Beam 2:0" ->
// "Beam 2").
func cellDisplayName(cell string) string {
	if i := strings.LastIndex(cell, ":"); i >= 0 {
		return cell[:i]
	}
	return cell
}

// --- pushing to the engine -------------------------------------------------------

// activeLocked is what should be on the tests layer now.
func (st *testsState) activeLocked() (sc testScopeJSON, tests []patternTestRequest, on bool) {
	if st.run != nil {
		step := st.run.seq.Steps[st.run.step]
		return normalizeTestScope(step.Scope), step.Tests, true
	}
	if st.adHoc {
		return st.adScope, st.adTests, true
	}
	return st.adScope, nil, false
}

// applyTestsLocked resolves the active scope and pushes it to Rig Check when
// anything changed since the last push (or force). fade is applied first. It
// reports whether it pushed.
func (s *Server) applyTestsLocked(fade *time.Duration, force bool) bool {
	st := s.tests
	sc, tests, on := st.activeLocked()
	targets, ignored, note := s.resolveTestScope(sc)
	st.scope, st.targets, st.ignored, st.note = sc, targets, ignored, note
	if !on {
		st.note = ""
	}
	key := ""
	if on {
		b, _ := json.Marshal(tests)
		var k strings.Builder
		fmt.Fprintf(&k, "%v|%s|", s.PatchStore.Token(), b)
		for _, t := range targets {
			k.WriteString(t.target.Entry.ID + "\x00")
		}
		if st.run != nil {
			fmt.Fprintf(&k, "|run %s %d", st.run.seq.ID, st.run.step)
		}
		key = k.String()
	}
	if !force && key == st.pushed {
		return false
	}
	st.pushed = key
	if !on {
		if st.owns {
			s.RigCheck.ReleaseTests()
		}
		return true
	}
	specs := make([]patch.PatternSpec, 0, len(tests))
	for _, t := range tests {
		specs = append(specs, t.spec())
	}
	tt := make([]patch.TestTarget, 0, len(targets))
	for _, t := range targets {
		tt = append(tt, t.target)
	}
	// Specs were validated when they were stored or set; a fade out of
	// range was refused at the same time.
	_, _ = s.RigCheck.SetTests(tt, specs, fade)
	st.owns = true
	return true
}

func (s *Server) broadcastTests(rev uint64) {
	s.hub.broadcast(wsMessage{Type: "tests", At: time.Now(), Revision: &rev})
}

// refreshTests re-resolves the tests layer's scope after the selection or
// the show changed, and pushes and broadcasts only if it moved.
func (s *Server) refreshTests() {
	st := s.tests
	st.mu.Lock()
	if !st.owns {
		st.mu.Unlock()
		return
	}
	changed := s.applyTestsLocked(nil, false)
	if changed {
		st.revision++
	}
	rev := st.revision
	st.mu.Unlock()
	if changed {
		s.broadcastTests(rev)
	}
}

// testsShowBoundary ends everything on a show switch, reset or recover (the
// guard has already reset Rig Check's selection).
func (s *Server) testsShowBoundary() {
	st := s.tests
	st.mu.Lock()
	s.stopTestsTimerLocked()
	if st.run != nil {
		st.ended = "show changed"
	}
	st.run, st.adHoc, st.adTests, st.owns, st.pushed = nil, false, nil, false, ""
	st.adScope = testScopeJSON{Kind: testScopeSelection}
	st.revision++
	rev := st.revision
	st.mu.Unlock()
	s.broadcastTests(rev)
}

// testsLegacyTakeover: a loaded test preset (or a rehearsal) changed the
// test selection itself, so the Tests API no longer owns it.
func (s *Server) testsLegacyTakeover() {
	st := s.tests
	st.mu.Lock()
	if !st.owns && st.run == nil && !st.adHoc {
		st.mu.Unlock()
		return
	}
	s.stopTestsTimerLocked()
	if st.run != nil {
		st.ended = "a loaded test preset took over"
	}
	st.run, st.adHoc, st.adTests, st.owns, st.pushed = nil, false, nil, false, ""
	st.revision++
	rev := st.revision
	st.mu.Unlock()
	s.broadcastTests(rev)
}

// testsOwnLayer reports whether the Tests API drives the tests layer.
func (s *Server) testsOwnLayer() bool {
	s.tests.mu.Lock()
	defer s.tests.mu.Unlock()
	return s.tests.owns
}

// isLegacyRigCheckWrite: a route that drives the Rig Check engine directly,
// changing what it tests or whether it outputs: loading a saved test preset,
// or building a rehearsal. (The retired /api/patch/rigcheck/* routes were the
// rest of this list until C7.)
func isLegacyRigCheckWrite(r *http.Request) bool {
	if r.Method == http.MethodGet {
		return false
	}
	p := r.URL.Path
	return p == "/api/patch/workspace/load-preset" || p == "/api/patch/workspace/rehearse"
}

// --- the run --------------------------------------------------------------------

func (s *Server) stopTestsTimerLocked() {
	st := s.tests
	st.timerGen++
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
}

func stepDuration(step testStepJSON) time.Duration {
	return time.Duration(math.Round(step.Advance.Seconds * float64(time.Second)))
}

// enterStepLocked makes the run's current step live: one atomic swap of the
// tests layer, then its auto-advance timer (unless paused).
func (s *Server) enterStepLocked() {
	st := s.tests
	s.stopTestsTimerLocked()
	step := st.run.seq.Steps[st.run.step]
	var fade *time.Duration
	if step.FadeMS != nil {
		d := time.Duration(*step.FadeMS) * time.Millisecond
		fade = &d
	}
	s.applyTestsLocked(fade, true)
	st.run.deadline, st.run.left = time.Time{}, 0
	if step.Advance.Mode == testAdvanceAuto {
		st.run.left = stepDuration(step)
		if !st.run.paused {
			s.armTestsTimerLocked(st.run.left)
		}
	}
}

func (s *Server) armTestsTimerLocked(d time.Duration) {
	st := s.tests
	clock := s.DMX.Clock()
	st.timerGen++
	gen := st.timerGen
	st.run.deadline = clock.Now().Add(d)
	st.timer = clock.AfterFunc(d, func() { s.testsAutoAdvance(gen) })
}

// testsAutoAdvance is the auto step's timer: the next step, or — after the
// last — the end of the run.
func (s *Server) testsAutoAdvance(gen uint64) {
	st := s.tests
	st.mu.Lock()
	if gen != st.timerGen || st.run == nil || st.run.paused {
		st.mu.Unlock()
		return
	}
	st.timer = nil
	if st.run.step+1 >= len(st.run.seq.Steps) {
		s.endRunLocked("finished")
	} else {
		st.run.step++
		s.enterStepLocked()
	}
	st.revision++
	rev := st.revision
	st.mu.Unlock()
	s.broadcastTests(rev)
}

// endRunLocked ends the run and releases the tests layer.
func (s *Server) endRunLocked(reason string) {
	st := s.tests
	s.stopTestsTimerLocked()
	st.run, st.ended = nil, reason
	st.adHoc, st.adTests = false, nil
	s.applyTestsLocked(nil, true)
}

// --- validation -------------------------------------------------------------------

func testName(name, what string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxTestNameLen {
		return "", fmt.Errorf("A %s name is 1 to %d characters.", what, maxTestNameLen)
	}
	return name, nil
}

func validateTestSpecs(tests []patternTestRequest, where string) error {
	if len(tests) > maxTestsPerStep {
		return fmt.Errorf("%s has %d tests; at most %d can run together.", where, len(tests), maxTestsPerStep)
	}
	for _, t := range tests {
		if err := patch.ValidatePatternSpec(t.spec()); err != nil {
			return fmt.Errorf("%s: %s.", where, strings.TrimSuffix(strings.TrimPrefix(err.Error(), "patch: "), "."))
		}
	}
	return nil
}

func validateTestSteps(steps []testStepJSON, ws showWorkspace) ([]testStepJSON, error) {
	if len(steps) == 0 || len(steps) > maxTestSteps {
		return nil, fmt.Errorf("A sequence has 1 to %d steps.", maxTestSteps)
	}
	out := make([]testStepJSON, 0, len(steps))
	for i, step := range steps {
		where := fmt.Sprintf("Step %d", i+1)
		name, err := testName(step.Name, "step")
		if err != nil {
			return nil, fmt.Errorf("%s: %s", where, err.Error())
		}
		step.Name = name
		if len(step.Tests) == 0 {
			return nil, fmt.Errorf("%s needs at least one test.", where)
		}
		if err := validateTestSpecs(step.Tests, where); err != nil {
			return nil, err
		}
		step.Scope = normalizeTestScope(step.Scope)
		if err := checkTestScope(step.Scope, ws); err != nil {
			return nil, fmt.Errorf("%s: %s", where, err.Error())
		}
		if step.FadeMS != nil && (*step.FadeMS < 0 || *step.FadeMS > patch.MaxPatternFade.Milliseconds()) {
			return nil, fmt.Errorf("%s: a fade time is 0 to %d ms.", where, patch.MaxPatternFade.Milliseconds())
		}
		switch step.Advance.Mode {
		case "", testAdvanceManual:
			step.Advance = testAdvanceJSON{Mode: testAdvanceManual}
		case testAdvanceAuto:
			if !(step.Advance.Seconds >= minAutoAdvanceSecs && step.Advance.Seconds <= maxAutoAdvanceSecs) {
				return nil, fmt.Errorf("%s: an automatic step lasts %g to %g seconds.", where, minAutoAdvanceSecs, float64(maxAutoAdvanceSecs))
			}
		default:
			return nil, fmt.Errorf("%s: a step advances manually or automatically (manual or auto); %q is neither.", where, step.Advance.Mode)
		}
		step.Tests = append(make([]patternTestRequest, 0, len(step.Tests)), step.Tests...)
		out = append(out, step)
	}
	return out, nil
}

// --- view ---------------------------------------------------------------------------

func (s *Server) testsViewLocked(query testScopeJSON, haveQuery bool) testsViewJSON {
	st := s.tests
	p, _ := s.PatchStore.Get()
	ws := workspaceFor(p)
	v := testsViewJSON{
		Revision: st.revision, Output: s.Programmer.Output(), Owner: "none",
		Scope: st.scope, Targets: make([]testsTargetJSON, 0, len(st.targets)),
		Ignored: append(make([]programmerIgnored, 0), st.ignored...), Note: st.note,
		Available: make([]availableTestJSON, 0), AdHoc: append(make([]patternTestRequest, 0), st.adTests...),
		Tests: make([]testsTestJSON, 0), Contested: make([]testsContestedJSON, 0),
		BaseState: patternBaseStateJSON{ShutterUnknownEntries: make([]string, 0)},
		Sequences: ws.TestSequences,
		Run:       testsRunJSON{EndedReason: st.ended},
		Limits: testsLimitsJSON{Sequences: maxTestSequences, Steps: maxTestSteps, TestsPerStep: maxTestsPerStep,
			MinAutoSeconds: minAutoAdvanceSecs, MaxAutoSeconds: maxAutoAdvanceSecs, MaxFadeMs: patch.MaxPatternFade.Milliseconds(), NameLength: maxTestNameLen},
	}
	byID := map[string]testsTargetJSON{}
	targets := st.targets
	if !st.owns && st.run == nil && !st.adHoc {
		// Nothing pushed: the catalog is for the ad-hoc scope as it stands.
		targets, _, _ = s.resolveTestScope(st.adScope)
		v.Scope = st.adScope
	}
	if haveQuery {
		targets, _, _ = s.resolveTestScope(query)
	}
	entries := make([]patch.Entry, 0, len(targets))
	for _, t := range targets {
		entries = append(entries, t.target.Entry)
	}
	for _, t := range st.targets {
		v.Targets = append(v.Targets, t.json)
		byID[t.target.Entry.ID] = t.json
	}
	if haveQuery || (!st.owns && st.run == nil && !st.adHoc) {
		v.Targets = v.Targets[:0]
		for _, t := range targets {
			v.Targets = append(v.Targets, t.json)
		}
	}
	for _, a := range patch.AvailableTests(entries) {
		v.Available = append(v.Available, availableTestJSON{
			ID: string(a.ID), Kind: string(a.Kind), Group: string(a.Group), Target: a.Target,
			Label: a.Label, Attribute: a.Attribute, FixtureCount: a.FixtureCount, LabelFromGDTF: a.LabelFromGDTF,
		})
	}
	ps := s.RigCheck.PatternStatus()
	v.FadeMs = ps.FadeMS
	if st.owns {
		v.Owner = "tests"
		v.OutputEnabled = ps.OutputEnabled
		v.ElapsedMs = ps.ElapsedMS
		js := toPatternStatusJSON(ps)
		v.BaseState = js.BaseState
		for i, t := range js.Tests {
			tj := testsTestJSON{patternTestStatusJSON: t, Entries: make([]testsEntryJSON, 0, len(ps.Tests[i].Entries))}
			for _, e := range ps.Tests[i].Entries {
				tgt := byID[e.EntryID]
				tj.Entries = append(tj.Entries, testsEntryJSON{EntryID: tgt.EntryID, Cell: tgt.Cell, Applied: e.Applied,
					Inferred: e.Inferred, DetailMissing: e.DetailMissing, PhaseDegrees: e.PhaseDegrees})
			}
			v.Tests = append(v.Tests, tj)
		}
		for _, c := range js.Contested {
			tgt := byID[c.EntryID]
			v.Contested = append(v.Contested, testsContestedJSON{Universe: c.Universe, Channel: c.Channel, EntryID: tgt.EntryID, Cell: tgt.Cell, Tests: c.Tests})
		}
	} else if ps.OutputEnabled || ps.TotalScope > 0 {
		v.Owner = "rigcheck"
	}
	if r := st.run; r != nil {
		step := r.seq.Steps[r.step]
		v.Run = testsRunJSON{Active: true, SequenceID: r.seq.ID, SequenceName: r.seq.Name, Step: r.step, StepCount: len(r.seq.Steps),
			StepName: step.Name, Paused: r.paused, Advance: step.Advance, EndedReason: st.ended}
		if step.Advance.Mode == testAdvanceAuto {
			left := r.left
			if !r.paused {
				left = r.deadline.Sub(s.DMX.Clock().Now())
			}
			ms := left.Milliseconds()
			v.Run.RemainingMs = &ms
		}
	}
	return v
}

// --- handlers -----------------------------------------------------------------------

func testsExpected(r *http.Request) (*uint64, error) {
	h := r.Header.Get(testsRevisionHeader)
	if h == "" {
		return nil, nil
	}
	v, err := strconv.ParseUint(h, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be the tests revision number this browser last saw.", testsRevisionHeader)
	}
	return &v, nil
}

func (s *Server) writeTestsJSON(w http.ResponseWriter, status int, v testsViewJSON) {
	w.Header().Set(testsRevisionHeader, strconv.FormatUint(v.Revision, 10))
	writeJSON(w, status, v)
}

func (s *Server) handleGetTests(w http.ResponseWriter, r *http.Request) {
	s.syncProgrammer(false)
	s.refreshTests()
	q := r.URL.Query()
	query := normalizeTestScope(testScopeJSON{Kind: q.Get("kind"), Group: q.Get("group"), Layer: q.Get("layer")})
	have := q.Get("kind") != ""
	if have {
		p, _ := s.PatchStore.Get()
		if err := checkTestScope(query, workspaceFor(p)); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	s.tests.mu.Lock()
	v := s.testsViewLocked(query, have)
	s.tests.mu.Unlock()
	s.writeTestsJSON(w, http.StatusOK, v)
}

type testsRequest struct {
	Tests  []patternTestRequest `json:"tests"`
	Scope  *testScopeJSON       `json:"scope"`
	FadeMS *int64               `json:"fadeMs"`
	ID     string               `json:"id"`
	Name   string               `json:"name"`
	Steps  []testStepJSON       `json:"steps"`
	Step   *int                 `json:"step"`
}

// testsHTTPError carries a status out of a tests action.
type testsHTTPError struct {
	status int
	err    error
}

func (e testsHTTPError) Error() string { return e.err.Error() }

func testsErr(status int, format string, a ...any) error {
	return testsHTTPError{status: status, err: fmt.Errorf(format, a...)}
}

func (s *Server) handleTestsAction(w http.ResponseWriter, r *http.Request) {
	var req testsRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil && !strings.Contains(err.Error(), "EOF") {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	expected, err := testsExpected(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	action := r.PathValue("action")
	s.syncProgrammer(false)

	// Sequence storage writes the show (outside the tests lock: the store
	// write re-enters the guard's sync path only after this handler).
	switch action {
	case "sequence-save", "sequence-rename", "sequence-delete":
		s.tests.mu.Lock()
		stale := expected != nil && *expected != s.tests.revision
		running := ""
		if s.tests.run != nil {
			running = s.tests.run.seq.ID
		}
		s.tests.mu.Unlock()
		if stale {
			writeError(w, http.StatusConflict, errTestsStale)
			return
		}
		if err := s.storeTestSequence(action, req, running); err != nil {
			s.writeTestsError(w, err)
			return
		}
		s.tests.mu.Lock()
		s.tests.revision++
		v := s.testsViewLocked(testScopeJSON{}, false)
		s.tests.mu.Unlock()
		s.broadcastTests(v.Revision)
		s.writeTestsJSON(w, http.StatusOK, v)
		return
	}

	var seq *testSequenceJSON
	if action == "run-start" {
		p, _ := s.PatchStore.Get()
		ws, err := strictWorkspace(p)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		for i := range ws.TestSequences {
			if ws.TestSequences[i].ID == req.ID {
				seq = &ws.TestSequences[i]
			}
		}
		if seq == nil {
			writeError(w, http.StatusNotFound, errors.New("There is no stored test sequence with that id; refresh the sequences."))
			return
		}
	}
	if action == "set" {
		if err := validateTestSpecs(req.Tests, "The test selection"); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if req.Scope != nil {
			p, _ := s.PatchStore.Get()
			if err := checkTestScope(normalizeTestScope(*req.Scope), workspaceFor(p)); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
	}

	st := s.tests
	st.mu.Lock()
	if expected != nil && *expected != st.revision {
		st.mu.Unlock()
		writeError(w, http.StatusConflict, errTestsStale)
		return
	}
	err = s.testsActionLocked(action, req, seq)
	if err != nil {
		st.mu.Unlock()
		s.writeTestsError(w, err)
		return
	}
	st.revision++
	v := s.testsViewLocked(testScopeJSON{}, false)
	st.mu.Unlock()
	s.broadcastTests(v.Revision)
	s.writeTestsJSON(w, http.StatusOK, v)
}

func (s *Server) writeTestsError(w http.ResponseWriter, err error) {
	var he testsHTTPError
	var ss storeStatus
	switch {
	case errors.As(err, &he):
		writeError(w, he.status, he.err)
	case errors.As(err, &ss):
		writeError(w, ss.status, err)
	default:
		writePatchStoreError(w, err)
	}
}

func (s *Server) testsActionLocked(action string, req testsRequest, seq *testSequenceJSON) error {
	st := s.tests
	needRun := func() error {
		if st.run == nil {
			return testsErr(http.StatusConflict, "No test sequence is running.")
		}
		return nil
	}
	switch action {
	case "set":
		if st.run != nil {
			return testsErr(http.StatusConflict, "A test sequence is running; stop it before choosing tests by hand.")
		}
		if len(req.Tests) == 0 {
			st.adHoc, st.adTests = false, nil
		} else {
			st.adHoc, st.adTests = true, append(make([]patternTestRequest, 0, len(req.Tests)), req.Tests...)
		}
		if req.Scope != nil {
			st.adScope = normalizeTestScope(*req.Scope)
		}
		s.applyTestsLocked(nil, true)
	case "clear":
		if st.run != nil {
			s.endRunLocked("cleared")
			return nil
		}
		st.adHoc, st.adTests = false, nil
		s.applyTestsLocked(nil, true)
	case "fade":
		if req.FadeMS == nil || *req.FadeMS < 0 || *req.FadeMS > patch.MaxPatternFade.Milliseconds() {
			return testsErr(http.StatusBadRequest, "A fade time is 0 to %d ms.", patch.MaxPatternFade.Milliseconds())
		}
		if _, err := s.RigCheck.SetPatternFade(time.Duration(*req.FadeMS) * time.Millisecond); err != nil {
			return testsErr(http.StatusBadRequest, "A fade time is 0 to %d ms.", patch.MaxPatternFade.Milliseconds())
		}
	case "run-start":
		step := 0
		if req.Step != nil {
			step = *req.Step
		}
		if step < 0 || step >= len(seq.Steps) {
			return testsErr(http.StatusBadRequest, "That sequence has steps 1 to %d.", len(seq.Steps))
		}
		st.adHoc, st.adTests = false, nil
		st.run = &testRun{seq: *seq, step: step}
		st.ended = ""
		s.enterStepLocked()
	case "run-next", "run-back", "run-jump":
		if err := needRun(); err != nil {
			return err
		}
		next := st.run.step
		switch action {
		case "run-next":
			if next+1 < len(st.run.seq.Steps) {
				next++
			}
		case "run-back":
			if next > 0 {
				next--
			}
		default:
			if req.Step == nil || *req.Step < 0 || *req.Step >= len(st.run.seq.Steps) {
				return testsErr(http.StatusBadRequest, "That sequence has steps 1 to %d.", len(st.run.seq.Steps))
			}
			next = *req.Step
		}
		if next != st.run.step || action == "run-jump" {
			st.run.step = next
			s.enterStepLocked()
		}
	case "run-pause":
		if err := needRun(); err != nil {
			return err
		}
		if !st.run.paused {
			if st.timer != nil {
				st.run.left = st.run.deadline.Sub(s.DMX.Clock().Now())
			}
			s.stopTestsTimerLocked()
			st.run.paused = true
		}
	case "run-resume":
		if err := needRun(); err != nil {
			return err
		}
		if st.run.paused {
			st.run.paused = false
			if st.run.seq.Steps[st.run.step].Advance.Mode == testAdvanceAuto {
				s.armTestsTimerLocked(st.run.left)
			}
		}
	case "run-stop":
		if err := needRun(); err != nil {
			return err
		}
		s.endRunLocked("stopped")
	default:
		return testsErr(http.StatusNotFound, "Unknown tests action %q.", action)
	}
	return nil
}

// storeTestSequence writes one sequence change to the show's workspace.
func (s *Server) storeTestSequence(action string, req testsRequest, running string) error {
	return s.mutateWorkspace(func(ws *showWorkspace) error {
		find := func() (int, error) {
			for i, q := range ws.TestSequences {
				if q.ID == req.ID {
					return i, nil
				}
			}
			return -1, storeErr(http.StatusNotFound, "There is no stored test sequence with that id; refresh the sequences.")
		}
		now := time.Now().UTC()
		switch action {
		case "sequence-save":
			name, err := testName(req.Name, "sequence")
			if err != nil {
				return storeErr(http.StatusBadRequest, "%s", err.Error())
			}
			steps, err := validateTestSteps(req.Steps, *ws)
			if err != nil {
				return storeErr(http.StatusBadRequest, "%s", err.Error())
			}
			if req.ID == "" {
				if len(ws.TestSequences) >= maxTestSequences {
					return storeErr(http.StatusBadRequest, "This show already has %d test sequences, the limit; delete one first.", maxTestSequences)
				}
				ws.TestSequences = append(ws.TestSequences, testSequenceJSON{ID: fmt.Sprintf("ts-%d", time.Now().UnixNano()), Name: name, Steps: steps, CreatedAt: now, UpdatedAt: now})
				return nil
			}
			i, err := find()
			if err != nil {
				return err
			}
			if req.ID == running {
				return storeErr(http.StatusConflict, "This sequence is running; stop it before changing it.")
			}
			ws.TestSequences[i].Name, ws.TestSequences[i].Steps, ws.TestSequences[i].UpdatedAt = name, steps, now
		case "sequence-rename":
			i, err := find()
			if err != nil {
				return err
			}
			name, err := testName(req.Name, "sequence")
			if err != nil {
				return storeErr(http.StatusBadRequest, "%s", err.Error())
			}
			ws.TestSequences[i].Name, ws.TestSequences[i].UpdatedAt = name, now
		case "sequence-delete":
			i, err := find()
			if err != nil {
				return err
			}
			if req.ID == running {
				return storeErr(http.StatusConflict, "This sequence is running; stop it before deleting it.")
			}
			ws.TestSequences = append(ws.TestSequences[:i], ws.TestSequences[i+1:]...)
		}
		return nil
	})
}
