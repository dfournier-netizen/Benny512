package params

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// This file implements report §1.1's generic, self-describing manufacturer
// PID editor: SUPPORTED_PARAMETERS -> PARAMETER_DESCRIPTION per unknown PID
// -> typed ParamDescriptor, plus a generic GetParam/SetParam pair that
// encodes/decodes according to a descriptor's DataType. Devices that NACK
// (or simply don't implement) PARAMETER_DESCRIPTION for a PID get a
// SelfDescribing:false descriptor so the UI can fall back to a raw hex
// editor — report §1.1 item 4's universal fallback.

// ParamDescriptor is the introspected shape of one PID, per report §1.1.
// When SelfDescribing is false, only PID is meaningful — every other field
// is the zero value and the UI should render/edit this PID as raw hex.
type ParamDescriptor struct {
	PID            rdm.ParameterID
	Label          string
	DataType       rdm.DataType
	CommandClass   rdm.PDCommandClass
	PDLSize        byte
	Unit           rdm.Unit
	Prefix         rdm.Prefix
	Min            int64
	Max            int64
	Default        int64
	SelfDescribing bool
}

func paramDescriptorFromPD(pd rdm.ParameterDescription) ParamDescriptor {
	return ParamDescriptor{
		PID: pd.PID, Label: pd.Description, DataType: pd.DataType, CommandClass: pd.CommandClass,
		PDLSize: pd.PDLSize, Unit: pd.Unit, Prefix: pd.Prefix,
		Min: pd.MinValue, Max: pd.MaxValue, Default: pd.DefaultValue, SelfDescribing: true,
	}
}

// knownDecodedESTAPIDs are ESTA-standard PIDs (< 0x8000) this package or
// package rdm already has a native, typed decoder/UI field for — Introspect
// does not give these a row in the generic parameter editor, since a
// dedicated field already covers them.
//
// IMPORTANT: this set has nothing to do with whether PARAMETER_DESCRIPTION
// may be sent for a PID — see isDescribable for that. E1.20 §10.4.2 defines
// PARAMETER_DESCRIPTION only for manufacturer-specific PIDs; a prior
// version of this file's doc comment claimed a responder was "technically
// allowed" to describe standard PIDs too and used that claim to justify
// PARAMETER_DESCRIPTION-probing any standard PID not in this set ("unknown
// ESTA PID"). RDM-LOG6 (a clean hard-line capture, zero timeouts, zero
// proxy refusals) shows that claim is simply wrong: 25/25 PARAMETER_
// DESCRIPTION requests for standard PIDs against a real, healthy responder
// (Elation KL Core IP) came back NACK UNKNOWN_PID. This set now only
// answers "does this PID already have a dedicated UI field" — see
// isEditorTarget.
//
// PIDProxiedDevices/PIDProxiedDeviceCount (0x0010/0x0011) used to be
// deliberately left OUT of this set (an earlier task's ask: "keep
// PROXIED_DEVICES/PROXIED_DEVICE_COUNT reachable via the generic parameter
// editor", because the UI had at the time dropped its dedicated
// proxy-status callout). Phase D task 1 reverses that: the owner wants them
// hidden as parameter rows again now that the callout is coming back as
// structured data (fixtureJSON.ProxiedDeviceCount et al., populated by
// registry.reclassify — see internal/registry/registry.go and
// internal/web/server.go). Rather than re-adding them to this set (which
// would only be correct for the "already has a dedicated field" reason this
// set exists for — proxy status doesn't, in the sense that no typed
// params.Client method reads it, only registry's independent ToD-driven
// cache), they're hidden via classification.go's Tier instead — see
// isEditorTarget below. That keeps "does X have a dedicated field" and
// "should X ever be a generic-editor row" as the two separate questions
// they actually are, rather than overloading this one map for both.
var knownDecodedESTAPIDs = map[rdm.ParameterID]bool{
	rdm.PIDDiscUniqueBranch: true, rdm.PIDDiscMute: true, rdm.PIDDiscUnMute: true,
	rdm.PIDQueuedMessage: true, rdm.PIDStatusMessages: true, rdm.PIDStatusIDDescription: true,
	rdm.PIDSupportedParameters: true, rdm.PIDParameterDescription: true, rdm.PIDDeviceInfo: true,
	rdm.PIDProductDetailIDList: true, rdm.PIDDeviceModelDescription: true, rdm.PIDManufacturerLabel: true,
	rdm.PIDDeviceLabel: true, rdm.PIDSoftwareVersionLabel: true, rdm.PIDDMXPersonality: true,
	rdm.PIDDMXPersonalityDescription: true, rdm.PIDDMXStartAddress: true,
	rdm.PIDSensorDefinition: true, rdm.PIDSensorValue: true, rdm.PIDRecordSensors: true,
	rdm.PIDIdentifyDevice: true,

	// E1.37-1 dimmer PIDs this package now has typed Client methods for
	// (see this file's dimmer-PID section below and internal/rdm/dimmer.go)
	// — excluded from the generic manufacturer-PID walk so a device exposing
	// these doesn't get a redundant raw-hex row alongside its dedicated
	// typed field, mirroring how DMX_PERSONALITY is excluded above.
	rdm.PIDCurve: true, rdm.PIDCurveDescription: true,
	rdm.PIDOutputResponseTime: true, rdm.PIDOutputResponseTimeDescription: true,
	rdm.PIDModulationFrequency: true, rdm.PIDModulationFrequencyDescription: true,
	rdm.PIDMinimumLevel: true, rdm.PIDMaximumLevel: true, rdm.PIDIdentifyMode: true,

	// Phase D task 2's "service life" family and destructive actions —
	// internal/params/servicelife.go now has typed Client methods for all
	// six, so none of them should get a redundant raw-hex editor row either.
	// RESET_DEVICE in particular MUST NOT reach the generic editor: it is
	// SET_COMMAND only (E1.20 §10.11.2 defines no GET form at all), and the
	// generic editor's bulk "GET every descriptor's current value" pass
	// (devicedetail.js, client-driven) would otherwise send it a doomed GET
	// on every device that advertises it — see getRaw's own defensive
	// RESET_DEVICE guard in params.go for the second, wire-level line of
	// defense against that same bug reaching the wire by any other path.
	rdm.PIDDeviceHours: true, rdm.PIDLampHours: true, rdm.PIDLampStrikes: true,
	rdm.PIDLampState: true, rdm.PIDDevicePowerCycles: true,
	rdm.PIDFactoryDefaults: true, rdm.PIDResetDevice: true,

	// SLOT_DESCRIPTION (0x0121) needs a request payload (a 2-byte slot
	// number, E1.20 §10.7.2) the generic editor's index-free GET can't
	// supply, the same reason CURVE_DESCRIPTION et al. are excluded above.
	// This package has no dedicated per-slot UI for it yet (SLOT_INFO would
	// supply the slot count a future pass could iterate), so excluding it
	// here is strictly a bug fix (stop the malformed PDL=0 GET), not a
	// feature — see params.go's getRaw for the matching defensive guard.
	rdm.PIDSlotDescription: true,
}

// isDescribable reports whether PARAMETER_DESCRIPTION may legally be sent
// for pid at all. E1.20 §10.4.2 defines PARAMETER_DESCRIPTION only for
// manufacturer-specific PIDs (rdm.ParameterID.IsManufacturerSpecific,
// 0x8000-0xFFDF) — asking about anything in the standard range is out of
// spec, and RDM-LOG6 confirms a real, healthy responder NACKs UNKNOWN_PID
// for every standard PID asked about (25/25, zero exceptions). This is the
// ONLY predicate that may gate an actual GET PARAMETER_DESCRIPTION on the
// wire: resolveDescriptor below is the single chokepoint that calls it, and
// every caller of PARAMETER_DESCRIPTION (Introspect's walk, DescribeParam's
// on-demand path, and any future one) MUST go through resolveDescriptor
// rather than issuing the GET directly, so this rule cannot be bypassed by
// adding a new call site elsewhere.
func isDescribable(pid rdm.ParameterID) bool {
	return pid.IsManufacturerSpecific()
}

// isEditorTarget reports whether pid should get a row in the generic
// parameter editor: any manufacturer PID, or any standard PID this package
// doesn't already have a dedicated typed field for (report §1.1 item 5's
// "surface everything" goal). This is deliberately broader than
// isDescribable — a standard PID with no dedicated field (e.g.
// PROXIED_DEVICE_COUNT) still needs a row so the raw-hex editor can
// GET/SET it, it just never gets PARAMETER_DESCRIPTION-probed to find its
// shape (resolveDescriptor's isDescribable check turns that into a
// zero-wire-traffic non-self-describing descriptor instead).
func isEditorTarget(pid rdm.ParameterID) bool {
	if pid.IsManufacturerSpecific() {
		return true
	}
	if knownDecodedESTAPIDs[pid] {
		return false
	}
	// Phase D task 1: a PID classification.go marks TierHidden never gets a
	// generic-editor row, full stop — this is what actually implements
	// "hide them as rows" for PROXIED_DEVICES/PROXIED_DEVICE_COUNT and the
	// rest of the Hidden-tier list (DISC_*, the STATUS_* queue family,
	// SUPPORTED_PARAMETERS/PARAMETER_DESCRIPTION and their E1.20-2025
	// enhanced-introspection siblings, DEVICE_INFO, PRODUCT_DETAIL_ID_LIST,
	// SENSOR_DEFINITION/RECORD_SENSORS, the E1.37-2/E1.33/E1.37-7 PIDs —
	// see classification.go's pidTiers for the full, explicit list). Most
	// of those PIDs were already excluded above via knownDecodedESTAPIDs
	// for an unrelated reason (a dedicated field already exists); this
	// catches the rest, uniformly, from one table instead of growing this
	// function's own bespoke exclusion list every time the owner asks for
	// another PID hidden. TierPromoted and TierStandard PIDs are treated
	// identically here — Tier only affects ordering/prominence
	// (paramDescriptorJSON.Tier, internal/web/device.go), never whether a
	// row exists.
	return Tier(pid) != TierHidden
}

// --- shared descriptor cache -------------------------------------------
//
// Report §1.1 item 3: a manufacturer PID's shape is a firmware-scoped
// constant, not a per-device-instance fact, so it can be cached and shared
// across every discovered device from the same manufacturer. This process-
// lifetime cache is keyed on (manufacturer ID, PID) — coarser than
// (manufacturer, model, firmware version), which the report notes as the
// theoretically-correct key but DEVICE_INFO's device_model_id/
// software_version_id aren't threaded through here; a firmware update that
// silently changes a PID's shape (rare) would show stale data until
// restart. Documented as a known simplification.
type descCacheKey struct {
	manufacturerID uint16
	pid            rdm.ParameterID
}

var (
	descCacheMu sync.RWMutex
	descCache   = map[descCacheKey]ParamDescriptor{}
)

func descCacheGet(mfr uint16, pid rdm.ParameterID) (ParamDescriptor, bool) {
	descCacheMu.RLock()
	defer descCacheMu.RUnlock()
	d, ok := descCache[descCacheKey{mfr, pid}]
	return d, ok
}

func descCacheSet(mfr uint16, pid rdm.ParameterID, d ParamDescriptor) {
	descCacheMu.Lock()
	descCache[descCacheKey{mfr, pid}] = d
	descCacheMu.Unlock()
}

// ClearDescriptorCache empties the process-wide PARAMETER_DESCRIPTION
// cache — exposed for tests and for a UI "forget learned PIDs" action.
func ClearDescriptorCache() {
	descCacheMu.Lock()
	descCache = map[descCacheKey]ParamDescriptor{}
	descCacheMu.Unlock()
}

// --- per-UID introspection state ----------------------------------------

type uidState struct {
	mu          sync.RWMutex
	descriptors map[rdm.ParameterID]ParamDescriptor
	sensorDefs  []rdm.SensorDefinition
	// paramDescUnsupported is set once this UID's responder NACKs
	// PARAMETER_DESCRIPTION itself with UNKNOWN_PID. E1.20's UNKNOWN_PID
	// reason on a GET means "I do not implement this PID [0x0051, the
	// command being sent] at all" — a statement about the device, not about
	// whichever target PID happened to be in that request's payload. Once
	// set, resolveDescriptor short-circuits every later manufacturer-PID
	// probe for this UID without spending another transaction to learn the
	// same fact again. The per-(manufacturer,pid) descCache above already
	// avoids re-asking about one specific PID; this catches the case
	// descCache can't — a device with several *different* manufacturer PIDs
	// and no PARAMETER_DESCRIPTION support at all, which would otherwise pay
	// one wasted NACK per distinct PID before the per-PID cache had a chance
	// to help.
	paramDescUnsupported bool

	// --- Phase D task 3: speculative-probe gating ------------------------
	//
	// supportedSet/supportedKnown/supportedAttempted cache this UID's
	// SUPPORTED_PARAMETERS list so getRaw's speculative-PID gate (see
	// isSpeculativePID/ensureAdvertised below, consulted from params.go's
	// getRaw) doesn't re-fetch it for every gated GET, and so Introspect and
	// any other caller share one cache instead of each issuing their own
	// GET SUPPORTED_PARAMETERS. supportedAttempted is tracked separately
	// from supportedKnown so a device that NACKs/times out on
	// SUPPORTED_PARAMETERS itself (legal — some responders simply don't
	// implement even required PIDs correctly) is asked at most once per
	// process, not once per gated PID, while still falling back to "allow
	// the probe" (today's behavior) rather than ever blocking such a device
	// outright — see ensureAdvertised's doc comment.
	supportedSet       map[rdm.ParameterID]bool
	supportedKnown     bool
	supportedAttempted bool

	// unsupportedPIDs records, per speculative PID, that a live GET has
	// already come back NACK UNKNOWN_PID for this UID — independent of
	// supportedSet, so it still helps a device whose SUPPORTED_PARAMETERS
	// couldn't be resolved (unknown/NACKed) avoid repeating the exact same
	// failed probe on every subsequent call. Cleared by ForgetDevice like
	// every other per-UID cache here.
	unsupportedPIDs map[rdm.ParameterID]bool
}

var (
	uidStatesMu sync.Mutex
	uidStates   = map[rdm.UID]*uidState{}
)

func stateFor(uid rdm.UID) *uidState {
	uidStatesMu.Lock()
	defer uidStatesMu.Unlock()
	s, ok := uidStates[uid]
	if !ok {
		s = &uidState{descriptors: map[rdm.ParameterID]ParamDescriptor{}}
		uidStates[uid] = s
	}
	return s
}

// ForgetDevice drops all cached introspection/sensor-definition state for
// uid (a device that reappeared with new firmware, or the UI's "rescan"
// action).
func ForgetDevice(uid rdm.UID) {
	uidStatesMu.Lock()
	delete(uidStates, uid)
	uidStatesMu.Unlock()
}

// ClearAllDeviceState empties the process-wide per-UID introspection state
// for every device at once — ForgetDevice's whole-table counterpart,
// mirroring ClearDescriptorCache's relationship to descCache. Used by the
// full-reset flow (task ask: "everything"), which has no single UID to
// target; ForgetDevice remains the right call for a single device's
// "rescan" action.
func ClearAllDeviceState() {
	uidStatesMu.Lock()
	uidStates = map[rdm.UID]*uidState{}
	uidStatesMu.Unlock()
}

// --- Introspect -----------------------------------------------------------

// IntrospectProgress reports incremental progress during Introspect, meant
// to be forwarded over the WS as introspection proceeds (report task item:
// "expose progress events").
type IntrospectProgress struct {
	UID        rdm.UID
	Done       int
	Total      int
	PID        rdm.ParameterID
	Descriptor ParamDescriptor
}

// IntrospectResult is Introspect's return value.
type IntrospectResult struct {
	UID         rdm.UID
	Descriptors []ParamDescriptor
}

// Introspect issues GET SUPPORTED_PARAMETERS, then builds one ParamDescriptor
// per editor-target PID it finds (see isEditorTarget: any manufacturer PID,
// plus any standard PID without a dedicated typed field). Only
// manufacturer-specific PIDs (isDescribable) actually get a live GET
// PARAMETER_DESCRIPTION; standard editor-target PIDs resolve to a
// non-self-describing descriptor without any wire traffic, since E1.20
// never allows describing them. Devices that NACK PARAMETER_DESCRIPTION for
// a manufacturer PID get SelfDescribing:false for that PID rather than
// failing the whole call — report §1.1 item 4.
//
// Concurrency: each GET goes through the same c.ctrl (session.RDMController)
// as every other call on this Client, so requests are naturally serialized
// per the controller's configured SerializationScope — no extra locking is
// needed here. The whole walk is cancellable via ctx (checked between each
// PID); onProgress, if non-nil, is invoked synchronously after each
// resolved PID (including cache hits) so a caller streaming to a slow
// WebSocket client should not block inside it for long.
func (c *Client) Introspect(ctx context.Context, onProgress func(IntrospectProgress)) (IntrospectResult, error) {
	data, err := c.getRaw(ctx, rdm.PIDSupportedParameters, nil)
	if err != nil {
		return IntrospectResult{}, fmt.Errorf("params: introspect: SUPPORTED_PARAMETERS: %w", err)
	}
	pids, err := rdm.DecodeSupportedParameters(data)
	if err != nil {
		return IntrospectResult{}, fmt.Errorf("params: introspect: %w", err)
	}

	var targets []rdm.ParameterID
	for _, pid := range pids {
		if pid == 0 {
			// 0x0000 is not a valid RDM PID at all — real PID numbers start
			// at 0x0001 (rdm.PIDDiscUniqueBranch). RDM-LOG6 caught the
			// Elation KL Core IP listing a spurious trailing 0x0000 entry in
			// its own SUPPORTED_PARAMETERS response (a firmware quirk, not
			// anything we sent); asking any responder to describe or GET a
			// null PID is nonsensical regardless of range, so it's dropped
			// here at the boundary where a device's raw, untrusted PID list
			// becomes our internal target list — before it can become an
			// editor row, a PARAMETER_DESCRIPTION probe, or a GET.
			continue
		}
		if isEditorTarget(pid) {
			targets = append(targets, pid)
		}
	}

	st := stateFor(c.uid)
	out := make([]ParamDescriptor, 0, len(targets))
	for i, pid := range targets {
		select {
		case <-ctx.Done():
			return IntrospectResult{}, ctx.Err()
		default:
		}
		desc := c.resolveDescriptor(ctx, pid)
		st.mu.Lock()
		st.descriptors[pid] = desc
		st.mu.Unlock()
		out = append(out, desc)
		if onProgress != nil {
			onProgress(IntrospectProgress{UID: c.uid, Done: i + 1, Total: len(targets), PID: pid, Descriptor: desc})
		}
	}
	return IntrospectResult{UID: c.uid, Descriptors: out}, nil
}

// resolveDescriptor fetches (or serves from cache) one PID's
// PARAMETER_DESCRIPTION, translating any error (NACK, timeout, malformed
// response) into a non-self-describing descriptor rather than propagating
// the error — one uncooperative PID must not abort the whole introspection
// pass.
func (c *Client) resolveDescriptor(ctx context.Context, pid rdm.ParameterID) ParamDescriptor {
	if d, ok := descCacheGet(c.uid.ManufacturerID, pid); ok {
		return d
	}
	if !isDescribable(pid) {
		// We already know the answer, so don't spend a transaction asking.
		//
		// E1.20 §10.4.2 defines PARAMETER_DESCRIPTION only for
		// manufacturer-specific PIDs — ask about a standard PID and a
		// conforming device NACKs. RDM-LOG4 caught us doing exactly that
		// against real gear for three specific standard PIDs; RDM-LOG6 later
		// caught a broader version of the same bug (see isDescribable's doc
		// comment): the predicate this branch used to consult
		// (isIntrospectionTarget, now split into isDescribable/
		// isEditorTarget) only excluded a hardcoded list of standard PIDs
		// this package already decodes, so any *other* standard PID a
		// device happened to list in SUPPORTED_PARAMETERS — 25 of them, for
		// the KL Core IP in RDM-LOG6 — still fell through to the wire below
		// and got NACKed UNKNOWN_PID. Both Introspect's walk and this
		// on-demand path (DescribeParam) already funneled through this one
		// function; the bug was in what the shared predicate meant, not in
		// which callers consulted it. isDescribable now means exactly and
		// only "manufacturer-specific", so there is nothing left to gate
		// wrong.
		//
		// The returned descriptor is byte-for-byte what a NACK already
		// produced below, so nothing downstream changes shape: the editor
		// still falls back to the raw-hex field for these PIDs, as it did
		// before. The only difference is that no request is sent.
		d := ParamDescriptor{PID: pid, SelfDescribing: false}
		descCacheSet(c.uid.ManufacturerID, pid, d)
		return d
	}

	st := stateFor(c.uid)
	st.mu.RLock()
	givenUp := st.paramDescUnsupported
	st.mu.RUnlock()
	if givenUp {
		// This UID has already told us, via an UNKNOWN_PID NACK on
		// PARAMETER_DESCRIPTION itself, that it does not implement the
		// command at all. That fact doesn't depend on which manufacturer
		// PID we ask about next, so don't ask again — see uidState.
		// paramDescUnsupported's doc comment.
		d := ParamDescriptor{PID: pid, SelfDescribing: false}
		descCacheSet(c.uid.ManufacturerID, pid, d)
		return d
	}

	data, err := c.getRaw(ctx, rdm.PIDParameterDescription, rdm.EncodeParameterDescriptionRequest(pid))
	var d ParamDescriptor
	if err != nil {
		d = ParamDescriptor{PID: pid, SelfDescribing: false}
		var nackErr *session.NackError
		if errors.As(err, &nackErr) && nackErr.Reason == rdm.NackUnknownPID {
			st.mu.Lock()
			st.paramDescUnsupported = true
			st.mu.Unlock()
		}
	} else if pd, decErr := rdm.DecodeParameterDescription(data); decErr == nil {
		d = paramDescriptorFromPD(pd)
	} else {
		d = ParamDescriptor{PID: pid, SelfDescribing: false}
	}
	descCacheSet(c.uid.ManufacturerID, pid, d)
	return d
}

// DescribeParam returns pid's descriptor, resolving it on demand (via the
// shared cache, or a live PARAMETER_DESCRIPTION probe) if Introspect hasn't
// already populated it for this UID. Safe to call before Introspect.
func (c *Client) DescribeParam(ctx context.Context, pid rdm.ParameterID) ParamDescriptor {
	st := stateFor(c.uid)
	st.mu.RLock()
	d, ok := st.descriptors[pid]
	st.mu.RUnlock()
	if ok {
		return d
	}
	d = c.resolveDescriptor(ctx, pid)
	st.mu.Lock()
	st.descriptors[pid] = d
	st.mu.Unlock()
	return d
}

// Descriptors returns every descriptor Introspect (or DescribeParam) has
// resolved for this UID so far, without issuing any wire traffic.
func (c *Client) Descriptors() []ParamDescriptor {
	st := stateFor(c.uid)
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]ParamDescriptor, 0, len(st.descriptors))
	for _, d := range st.descriptors {
		out = append(out, d)
	}
	return out
}

// --- generic GetParam / SetParam ------------------------------------------

// ParamValueKind selects which field of ParamValue is populated.
type ParamValueKind int

// Kinds.
const (
	// ParamValueInt: the PID's DataType is a recognized numeric type
	// (byte/word/dword, signed or unsigned, or boolean).
	ParamValueInt ParamValueKind = iota
	// ParamValueString: DS_ASCII.
	ParamValueString
	// ParamValueRaw: not self-describing, or a DataType this package
	// doesn't have a typed encoding for (bit field, UID, IPv4/6, MAC,
	// group, manufacturer-specific range, ...) — report §1.1 item 4's raw
	// hex fallback.
	ParamValueRaw
)

// String renders the kind for logging/JSON tagging.
func (k ParamValueKind) String() string {
	switch k {
	case ParamValueInt:
		return "int"
	case ParamValueString:
		return "string"
	case ParamValueRaw:
		return "raw"
	default:
		return "unknown"
	}
}

// ParamValue is GetParam's decoded result: exactly one field is meaningful,
// selected by Kind.
type ParamValue struct {
	Kind ParamValueKind
	Int  int64
	Str  string
	Raw  []byte
}

var (
	// ErrParamNotSettable is returned by SetParam when the descriptor's
	// command_class is GET-only.
	ErrParamNotSettable = errors.New("params: PID's PARAMETER_DESCRIPTION command_class does not support SET")
	// ErrParamOutOfRange is returned by SetParam when a numeric value falls
	// outside [Min,Max] and the descriptor doesn't read as "unbounded"
	// (Min==Max==0).
	ErrParamOutOfRange = errors.New("params: value out of PARAMETER_DESCRIPTION range")
	// ErrParamTypeMismatch is returned when the Go value passed to SetParam
	// doesn't match what the PID's DataType needs.
	ErrParamTypeMismatch = errors.New("params: value type does not match PID's data type")
)

// GetParam issues a GET for pid and decodes it per DescribeParam's
// descriptor. Devices/PIDs with SelfDescribing==false decode as
// ParamValueRaw — the UI's hex-editor fallback path.
func (c *Client) GetParam(ctx context.Context, pid rdm.ParameterID) (ParamValue, ParamDescriptor, error) {
	desc := c.DescribeParam(ctx, pid)
	data, err := c.getRaw(ctx, pid, nil)
	if err != nil {
		return ParamValue{}, desc, err
	}
	if !desc.SelfDescribing {
		return ParamValue{Kind: ParamValueRaw, Raw: data}, desc, nil
	}
	v, err := decodeByDataType(data, desc.DataType)
	return v, desc, err
}

// SetParam issues a SET for pid, encoding value per DescribeParam's
// descriptor and validating it against the descriptor's declared range
// first. When the PID isn't self-describing, value must be a []byte (raw
// fallback SET, sent blind — report §1.1 item 4).
func (c *Client) SetParam(ctx context.Context, pid rdm.ParameterID, value any) error {
	desc := c.DescribeParam(ctx, pid)
	if !desc.SelfDescribing {
		raw, ok := value.([]byte)
		if !ok {
			return fmt.Errorf("%w: pid 0x%04X has no PARAMETER_DESCRIPTION; pass raw []byte", ErrParamTypeMismatch, uint16(pid))
		}
		return c.setRaw(ctx, pid, raw)
	}
	if !desc.CommandClass.SupportsSet() {
		return fmt.Errorf("%w: pid 0x%04X (%s)", ErrParamNotSettable, uint16(pid), desc.CommandClass)
	}
	if isNumericDataType(desc.DataType) {
		n, err := toInt64(value)
		if err != nil {
			return err
		}
		if err := validateRange(n, desc); err != nil {
			return err
		}
	}
	data, err := encodeByDataType(value, desc.DataType, desc.PDLSize)
	if err != nil {
		return err
	}
	return c.setRaw(ctx, pid, data)
}

func validateRange(n int64, desc ParamDescriptor) error {
	// A declared Min==Max==0 is the common "not bounded / not applicable"
	// shape (e.g. a boolean or a read-mostly PID whose author didn't bother
	// filling these in) — treat as unbounded rather than rejecting every
	// SET of 0. This is a pragmatic heuristic, not spec text; a PID whose
	// genuine legal range is exactly {0} cannot be distinguished from
	// "unbounded" by this rule alone.
	if desc.Min == 0 && desc.Max == 0 {
		return nil
	}
	if n < desc.Min || n > desc.Max {
		return fmt.Errorf("%w: %d not in [%d,%d]", ErrParamOutOfRange, n, desc.Min, desc.Max)
	}
	return nil
}

func isNumericDataType(dt rdm.DataType) bool {
	switch dt {
	case rdm.DSUnsignedByte, rdm.DSSignedByte, rdm.DSUnsignedWord, rdm.DSSignedWord,
		rdm.DSUnsignedDWord, rdm.DSSignedDWord, rdm.DSBoolean:
		return true
	default:
		return false
	}
}

func toInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("%w: got %T, want a number", ErrParamTypeMismatch, value)
	}
}

func decodeByDataType(data []byte, dt rdm.DataType) (ParamValue, error) {
	switch dt {
	case rdm.DSASCII:
		return ParamValue{Kind: ParamValueString, Str: string(data)}, nil
	case rdm.DSUnsignedByte:
		if len(data) < 1 {
			return ParamValue{}, fmt.Errorf("%w: DS_UNSIGNED_BYTE wants >=1 byte, got %d", ErrBadLength, len(data))
		}
		return ParamValue{Kind: ParamValueInt, Int: int64(data[0])}, nil
	case rdm.DSSignedByte:
		if len(data) < 1 {
			return ParamValue{}, fmt.Errorf("%w: DS_SIGNED_BYTE wants >=1 byte, got %d", ErrBadLength, len(data))
		}
		return ParamValue{Kind: ParamValueInt, Int: int64(int8(data[0]))}, nil
	case rdm.DSBoolean:
		if len(data) < 1 {
			return ParamValue{}, fmt.Errorf("%w: DS_BOOLEAN wants >=1 byte, got %d", ErrBadLength, len(data))
		}
		v := int64(0)
		if data[0] != 0 {
			v = 1
		}
		return ParamValue{Kind: ParamValueInt, Int: v}, nil
	case rdm.DSUnsignedWord:
		if len(data) < 2 {
			return ParamValue{}, fmt.Errorf("%w: DS_UNSIGNED_WORD wants >=2 bytes, got %d", ErrBadLength, len(data))
		}
		return ParamValue{Kind: ParamValueInt, Int: int64(binary.BigEndian.Uint16(data[:2]))}, nil
	case rdm.DSSignedWord:
		if len(data) < 2 {
			return ParamValue{}, fmt.Errorf("%w: DS_SIGNED_WORD wants >=2 bytes, got %d", ErrBadLength, len(data))
		}
		return ParamValue{Kind: ParamValueInt, Int: int64(int16(binary.BigEndian.Uint16(data[:2])))}, nil
	case rdm.DSUnsignedDWord:
		if len(data) < 4 {
			return ParamValue{}, fmt.Errorf("%w: DS_UNSIGNED_DWORD wants >=4 bytes, got %d", ErrBadLength, len(data))
		}
		return ParamValue{Kind: ParamValueInt, Int: int64(binary.BigEndian.Uint32(data[:4]))}, nil
	case rdm.DSSignedDWord:
		if len(data) < 4 {
			return ParamValue{}, fmt.Errorf("%w: DS_SIGNED_DWORD wants >=4 bytes, got %d", ErrBadLength, len(data))
		}
		return ParamValue{Kind: ParamValueInt, Int: int64(int32(binary.BigEndian.Uint32(data[:4])))}, nil
	default:
		// DS_BIT_FIELD, DS_GROUP, DS_UID, DS_URL, DS_MAC, DS_IPV4/6,
		// DS_ENUMERATION, manufacturer-specific range, DS_NOT_DEFINED: no
		// generic typed encoding, per report §1.1 — raw hex is the correct
		// fallback even when SelfDescribing is true, since PARAMETER_
		// DESCRIPTION told us the *shape* but not something this package
		// can render better than bytes.
		return ParamValue{Kind: ParamValueRaw, Raw: append([]byte(nil), data...)}, nil
	}
}

func encodeByDataType(value any, dt rdm.DataType, pdlSize byte) ([]byte, error) {
	switch dt {
	case rdm.DSASCII:
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: got %T, want string", ErrParamTypeMismatch, value)
		}
		b := []byte(s)
		if pdlSize > 0 && len(b) > int(pdlSize) {
			b = b[:pdlSize]
		} else if len(b) > 32 {
			b = b[:32]
		}
		return b, nil
	case rdm.DSUnsignedByte, rdm.DSSignedByte, rdm.DSBoolean:
		n, err := toInt64(value)
		if err != nil {
			return nil, err
		}
		return []byte{byte(n)}, nil
	case rdm.DSUnsignedWord, rdm.DSSignedWord:
		n, err := toInt64(value)
		if err != nil {
			return nil, err
		}
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(n))
		return b, nil
	case rdm.DSUnsignedDWord, rdm.DSSignedDWord:
		n, err := toInt64(value)
		if err != nil {
			return nil, err
		}
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(n))
		return b, nil
	default:
		raw, ok := value.([]byte)
		if !ok {
			return nil, fmt.Errorf("%w: PID's data type (%s) has no generic encoding; pass raw []byte", ErrParamTypeMismatch, dt)
		}
		return raw, nil
	}
}

// --- Phase D task 3: speculative-probe gating -----------------------------
//
// A bench capture against three real Elation Proteus Rayzor 1960 fixtures
// (516 responses, 223 NACKs) found the great majority of those NACKs came
// from Benny512 GET-probing PIDs the fixture never advertised in
// SUPPORTED_PARAMETERS at all: the E1.37-1 dimmer set, IDENTIFY_MODE,
// PROXIED_DEVICE_COUNT and PRODUCT_DETAIL_ID_LIST. Every one of those is a
// legitimately optional PID (none is in E1.20 Table A-3's "Required"
// columns), and per §10.4.1's own text, "PIDs that are included in the
// minimum support list ... shall not be reported [in SUPPORTED_PARAMETERS]"
// — the converse holds too: an optional PID a device implements MUST be
// listed. So once SUPPORTED_PARAMETERS is known, "not listed" is not a
// heuristic guess that a probe will fail, it is the device stating that
// fact outright, and asking anyway is just spending a transaction (and, on
// a bandwidth-constrained wireless link, real time) to relearn something
// already known.
//
// isSpeculativePID below names exactly the optional/probe-only PIDs this
// gate applies to — deliberately NOT every PID getRaw ever sends, so a
// device with incomplete or absent SUPPORTED_PARAMETERS support never has a
// CORE field (DEVICE_INFO, DEVICE_LABEL, DMX_START_ADDRESS, ...) silently
// blocked; see the isSpeculativePID doc comment for the exact set and why
// each member is in it. STATUS_MESSAGES is deliberately NOT included: its
// wire traffic goes through DeviceStatus's own repeat-GET drain loop
// (status.go), not through getRaw, and it's already client-gated (fetched
// only when the UI's Status tab is open, not on every device-detail load),
// so gating it here would touch a different, higher-risk code path for a
// PID that isn't part of the bench-observed problem.

// isSpeculativePID reports whether pid is one of the "ask only if
// advertised" probe-only PIDs getRaw gates via ensureAdvertised. Every
// member here is optional per E1.20 Table A-3 (never required), and every
// one appeared in the bench capture's blind-probe NACKs (see this section's
// doc comment) — RESET_DEVICE is deliberately NOT here: it needs its own
// unconditional getRaw guard (ErrResetDeviceHasNoGet in params.go) because
// it has no GET form at all, regardless of advertisement.
func isSpeculativePID(pid rdm.ParameterID) bool {
	switch pid {
	case rdm.PIDCurve, rdm.PIDCurveDescription,
		rdm.PIDOutputResponseTime, rdm.PIDOutputResponseTimeDescription,
		rdm.PIDModulationFrequency, rdm.PIDModulationFrequencyDescription,
		rdm.PIDMinimumLevel, rdm.PIDMaximumLevel, rdm.PIDIdentifyMode,
		rdm.PIDProductDetailIDList, rdm.PIDProxiedDeviceCount, rdm.PIDProxiedDevices,
		rdm.PIDDeviceHours, rdm.PIDLampHours, rdm.PIDLampStrikes, rdm.PIDLampState,
		rdm.PIDDevicePowerCycles, rdm.PIDFactoryDefaults:
		return true
	default:
		return false
	}
}

// resolveSupportedSet returns this UID's SUPPORTED_PARAMETERS set,
// resolving and caching it (at most once per UID per process, via
// supportedAttempted) if not already known. known=false means the device's
// support status could not be determined at all (SUPPORTED_PARAMETERS
// itself NACKed, timed out, or a prior attempt already failed) — callers
// must treat that as "don't know", never as "nothing is supported".
func (c *Client) resolveSupportedSet(ctx context.Context) (set map[rdm.ParameterID]bool, known bool) {
	st := stateFor(c.uid)
	st.mu.RLock()
	if st.supportedKnown {
		set, known = st.supportedSet, true
		st.mu.RUnlock()
		return set, known
	}
	attempted := st.supportedAttempted
	st.mu.RUnlock()
	if attempted {
		return nil, false
	}

	data, err := c.getRaw(ctx, rdm.PIDSupportedParameters, nil)

	st.mu.Lock()
	defer st.mu.Unlock()
	st.supportedAttempted = true
	if err == nil {
		if pids, decErr := rdm.DecodeSupportedParameters(data); decErr == nil {
			m := make(map[rdm.ParameterID]bool, len(pids))
			for _, p := range pids {
				m[p] = true
			}
			st.supportedSet = m
			st.supportedKnown = true
		}
	}
	return st.supportedSet, st.supportedKnown
}

// ensureAdvertised is getRaw's speculative-PID gate: it returns
// ErrPIDNotAdvertised when pid is a speculative PID this UID has already
// been confirmed NOT to support (either a fresh/cached SUPPORTED_PARAMETERS
// omits it, or a past live GET for it already NACKed UNKNOWN_PID), and nil
// otherwise — including, deliberately, when support could not be
// determined at all. A device that doesn't implement SUPPORTED_PARAMETERS
// (or NACKs/times out on it) must keep working exactly as it did before
// this gate existed: every speculative PID falls back to "go ahead and
// ask", never "assume unsupported and block".
func (c *Client) ensureAdvertised(ctx context.Context, pid rdm.ParameterID) error {
	st := stateFor(c.uid)
	st.mu.RLock()
	learnedUnsupported := st.unsupportedPIDs[pid]
	st.mu.RUnlock()
	if learnedUnsupported {
		return fmt.Errorf("%w: 0x%04X (a prior GET already came back NACK UNKNOWN_PID)", ErrPIDNotAdvertised, uint16(pid))
	}

	set, known := c.resolveSupportedSet(ctx)
	if known && !set[pid] {
		return fmt.Errorf("%w: 0x%04X", ErrPIDNotAdvertised, uint16(pid))
	}
	return nil
}

// rememberUnsupported records that a live GET for pid came back NACK
// UNKNOWN_PID for this UID, so ensureAdvertised can short-circuit any later
// call for the same PID without spending another transaction — the
// speculative-PID analogue of paramDescUnsupported above, generalized past
// PARAMETER_DESCRIPTION to every gated PID. Safe to call for a non-
// speculative PID too (getRaw only calls it for speculative ones, but
// nothing here depends on that).
func (c *Client) rememberUnsupported(pid rdm.ParameterID) {
	st := stateFor(c.uid)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.unsupportedPIDs == nil {
		st.unsupportedPIDs = map[rdm.ParameterID]bool{}
	}
	st.unsupportedPIDs[pid] = true
}

// IsAdvertised reports whether pid is present in this UID's
// SUPPORTED_PARAMETERS, resolving (and caching) the list on demand via the
// same cache getRaw's gate uses if it isn't already known. known=false
// means support could not be determined (SUPPORTED_PARAMETERS unanswered)
// — callers (e.g. the /api/device/{uid}/actions capability endpoint) MUST
// treat that as "don't know" and must never present it to a user as
// "confirmed unsupported". Exported for internal/web/device.go's
// destructive-action capability surface (Phase D task 2: "offer both [warm
// and cold reset] when 0x1001 is advertised; never claim to know more").
func (c *Client) IsAdvertised(ctx context.Context, pid rdm.ParameterID) (supported, known bool) {
	set, known := c.resolveSupportedSet(ctx)
	if !known {
		return false, false
	}
	return set[pid], true
}

// SupportedParameters returns this UID's full SUPPORTED_PARAMETERS set
// (resolving and caching it on demand, exactly like IsAdvertised), sorted
// ascending for a stable JSON encoding. known=false means the device's
// support status could not be determined — callers must return an empty/
// omitted list in that case, never an empty list that reads as "advertises
// nothing". Exported for internal/web/device.go's
// GET /api/device/{uid}/supported-parameters (Phase D task 3: "expose what
// the client needs" for a future client-side probe gate).
func (c *Client) SupportedParameters(ctx context.Context) (pids []rdm.ParameterID, known bool) {
	set, known := c.resolveSupportedSet(ctx)
	if !known {
		return nil, false
	}
	pids = make([]rdm.ParameterID, 0, len(set))
	for p := range set {
		pids = append(pids, p)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	return pids, true
}
