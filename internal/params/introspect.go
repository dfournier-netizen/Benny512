package params

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"benny512/internal/rdm"
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
// package rdm already has a native decoder for — Introspect does not bother
// PARAMETER_DESCRIPTION-probing these even though report §2.2 notes a
// responder is technically allowed to describe them. Anything ESTA-range
// but NOT in this set is treated as "unknown ESTA PID" and is
// PARAMETER_DESCRIPTION-probed exactly like a manufacturer PID, per report
// §1.1 item 5's recommendation.
//
// PIDProxiedDevices/PIDProxiedDeviceCount (0x0010/0x0011) are deliberately
// NOT in this set (task ask, item 2: "keep PROXIED_DEVICES/
// PROXIED_DEVICE_COUNT reachable via the generic parameter editor" — the
// UI dropped its dedicated proxy-status callout, so the *only* remaining
// way to inspect either PID is Introspect's normal manufacturer/unknown-PID
// walk). A device that doesn't implement proxy behavior simply won't list
// either PID in SUPPORTED_PARAMETERS, so this is a no-op for it; a proxy
// that does will get it PARAMETER_DESCRIPTION-probed like any other PID,
// falling back to the raw-hex editor if the device NACKs the description
// (report §1.1 item 4's universal fallback) — no special-casing needed.
var knownDecodedESTAPIDs = map[rdm.ParameterID]bool{
	rdm.PIDDiscUniqueBranch: true, rdm.PIDDiscMute: true, rdm.PIDDiscUnMute: true,
	rdm.PIDQueuedMessage: true, rdm.PIDStatusMessages: true, rdm.PIDStatusIDDescription: true,
	rdm.PIDSupportedParameters: true, rdm.PIDParameterDescription: true, rdm.PIDDeviceInfo: true,
	rdm.PIDProductDetailIDList: true, rdm.PIDDeviceModelDescription: true, rdm.PIDManufacturerLabel: true,
	rdm.PIDDeviceLabel: true, rdm.PIDSoftwareVersionLabel: true, rdm.PIDDMXPersonality: true,
	rdm.PIDDMXPersonalityDescription: true, rdm.PIDDMXStartAddress: true,
	rdm.PIDSensorDefinition: true, rdm.PIDSensorValue: true, rdm.PIDRecordSensors: true,
	rdm.PIDIdentifyDevice: true,
}

// isIntrospectionTarget reports whether pid should be walked via
// PARAMETER_DESCRIPTION: any manufacturer PID, or any ESTA-range PID this
// package doesn't already natively decode (report §1.1 item 5).
func isIntrospectionTarget(pid rdm.ParameterID) bool {
	if pid.IsManufacturerSpecific() {
		return true
	}
	return !knownDecodedESTAPIDs[pid]
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

// Introspect issues GET SUPPORTED_PARAMETERS, then GET PARAMETER_DESCRIPTION
// for every manufacturer-range PID (and unknown ESTA PID — see
// isIntrospectionTarget) it finds, building a []ParamDescriptor. Devices
// that NACK PARAMETER_DESCRIPTION for a given PID get SelfDescribing:false
// for that PID rather than failing the whole call — report §1.1 item 4.
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
		if isIntrospectionTarget(pid) {
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
	data, err := c.getRaw(ctx, rdm.PIDParameterDescription, rdm.EncodeParameterDescriptionRequest(pid))
	var d ParamDescriptor
	if err != nil {
		d = ParamDescriptor{PID: pid, SelfDescribing: false}
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
