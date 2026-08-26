package capture

// This file adds decoded detail for Art-Net's node-discovery/node-
// configuration packet family: ArtPoll, ArtPollReply, ArtAddress, ArtInput,
// ArtIpProg, ArtIpProgReply (report task: two live bench bugs — every
// ArtPollReply port showing "n/a", and ArtAddress/ArtIpProg node
// configuration doing nothing — with no evidence in the disk log to
// diagnose either from, because none of these six opcodes previously
// reached it). Mirrors rdmdetail.go's idiom: lean on internal/artnet's
// existing decode (no re-parsing of bytes here), always show the raw
// byte/field the interpretation was derived from (never the interpretation
// alone — the whole point is to catch our own decode disagreeing with the
// wire), and attach detail during DecodeEntry regardless of whether
// --lognodes routes the entry to the RDM ring/disk log, so the Analyzer's
// general ring gets the same decoded view for free.

import (
	"fmt"

	"benny512/internal/artnet"
)

// PollDetail is the decoded view of one ArtPoll entry. Minimal by design —
// a poll carries little state of diagnostic interest on its own — but
// logging it alongside PollReply lets a bench log show what triggered a
// polling cycle (a manual re-poll from the UI sets Flags/DiagPriority
// differently than the periodic background poll).
type PollDetail struct {
	FlagsRaw     byte `json:"flagsRaw"`
	DiagPriority byte `json:"diagPriority"`
	// SendPollReplyOnChange decodes Flags bit 1, per the artnet.Poll type's
	// own doc comment ("bit 1 = send ArtPollReply on state change").
	SendPollReplyOnChange bool `json:"sendPollReplyOnChange"`
}

// PollReplyPort is one port's raw-plus-decoded field set from a single
// ArtPollReply, indexed 0-3 to match PortTypes/GoodInput/GoodOutputA/
// GoodOutputB/SwIn/SwOut. All four slots are always rendered regardless of
// NumPorts (see PollReplyDetail.NumPorts's doc comment) — a node reporting
// NumPorts=4 while only port 0's PortTypes is non-zero is itself exactly
// the kind of disagreement this log exists to catch.
type PollReplyPort struct {
	Index int `json:"index"`
	// PortTypesRaw is the byte the "n/a" bug report is about: bit 7 = output
	// supported, bit 6 = input supported (session.Port's own established
	// decode of this field — internal/session/artnetsession.go — reused
	// here rather than re-derived), bits 5-0 = protocol ID (not decoded
	// here: this app has never had a confirmed mapping for that sub-field,
	// and guessing one would be exactly the kind of unverified claim this
	// log must not print as fact).
	PortTypesRaw    byte `json:"portTypesRaw"`
	OutputSupported bool `json:"outputSupported"`
	InputSupported  bool `json:"inputSupported"`
	GoodInputRaw    byte `json:"goodInputRaw"`
	GoodOutputARaw  byte `json:"goodOutputARaw"`
	// GoodOutputBRaw's bit 7 (RDM disabled on this port) is Art-Net 4 only;
	// see session.Port.RDMEnabled's doc comment — a pre-Art-Net-4 node
	// reports zero here regardless of its actual RDM support, so this is
	// shown raw rather than as a confident "RDM enabled/disabled" claim.
	GoodOutputBRaw byte `json:"goodOutputBRaw"`
	SwInRaw        byte `json:"swInRaw"`
	SwOutRaw       byte `json:"swOutRaw"`
}

// PollReplyDetail is the decoded view of one ArtPollReply entry — the
// primary evidence for the "every port shows n/a" bug class (report task
// 1): every per-port field the UI derives its input/output labels from,
// raw byte alongside decoded interpretation, so a bench log can show
// whether the disagreement is in what the node actually sent or in how
// this app reads it.
type PollReplyDetail struct {
	IPAddress  string `json:"ipAddress"`
	ShortName  string `json:"shortName"`
	LongName   string `json:"longName"`
	NodeReport string `json:"nodeReport,omitempty"`
	// NumPorts is the node's own claimed port count. Ports below is always
	// all 4 physical slots regardless of this value — see that field's doc
	// comment.
	NumPorts     uint16           `json:"numPorts"`
	NetSwitchRaw byte             `json:"netSwitchRaw"`
	SubSwitchRaw byte             `json:"subSwitchRaw"`
	Status1Raw   byte             `json:"status1Raw"`
	Ports        [4]PollReplyPort `json:"ports"`
}

// NodeConfigDetail is the decoded view of one ArtAddress, ArtInput,
// ArtIpProg, or ArtIpProgReply entry — the "rare, user-initiated, want a
// complete record" family --lognodes exists to always fully log. Grouped
// under one tagged struct exactly the way TodDetail groups
// ArtTodRequest/Data/Control: these four share a single diagnostic purpose
// (the "ArtAddress/ArtIpProg does nothing" bug class) and a caller wants
// "is there node-config detail here", not "which exact kind is it" as a
// prerequisite to reading anything. Only the fields relevant to SubKind are
// populated; see the field comments for which SubKind(s) set each one.
type NodeConfigDetail struct {
	// SubKind is "Address", "Input", "IpProg", or "IpProgReply".
	SubKind string `json:"subKind"`

	// --- ArtAddress ---
	NetSwitchRaw byte    `json:"netSwitchRaw,omitempty"` // Address only
	ShortName    string  `json:"shortName,omitempty"`    // Address only
	LongName     string  `json:"longName,omitempty"`     // Address only
	SwInRaw      [4]byte `json:"swInRaw,omitempty"`      // Address only
	SwOutRaw     [4]byte `json:"swOutRaw,omitempty"`     // Address only
	SubSwitchRaw byte    `json:"subSwitchRaw,omitempty"` // Address only
	CommandRaw   byte    `json:"commandRaw,omitempty"`   // Address only (AcCommand)
	CommandName  string  `json:"commandName,omitempty"`  // Address only

	// --- ArtAddress & ArtInput ---
	BindIndex byte `json:"bindIndex,omitempty"`

	// --- ArtInput ---
	InputRaw      [4]byte `json:"inputRaw,omitempty"`      // Input only
	InputDisabled [4]bool `json:"inputDisabled,omitempty"` // Input only

	// --- ArtIpProg ---
	IpProgCommandRaw  byte   `json:"ipProgCommandRaw,omitempty"`  // IpProg only
	Enable            bool   `json:"enable,omitempty"`            // IpProg only
	EnableDHCP        bool   `json:"enableDHCP,omitempty"`        // IpProg only
	SetDefault        bool   `json:"setDefault,omitempty"`        // IpProg only
	ProgramSubnetMask bool   `json:"programSubnetMask,omitempty"` // IpProg only
	ProgramIP         bool   `json:"programIP,omitempty"`         // IpProg only
	ProgIP            string `json:"progIP,omitempty"`            // IpProg only
	ProgSubnetMask    string `json:"progSubnetMask,omitempty"`    // IpProg only
	ProgPort          uint16 `json:"progPort,omitempty"`          // IpProg & IpProgReply

	// --- ArtIpProgReply ---
	CurrentIP     string `json:"currentIP,omitempty"`     // IpProgReply only
	CurrentSubnet string `json:"currentSubnet,omitempty"` // IpProgReply only
	StatusRaw     byte   `json:"statusRaw,omitempty"`     // IpProgReply only
	DHCPEnabled   bool   `json:"dhcpEnabled,omitempty"`   // IpProgReply only
}

// attachNodeConfigDetail populates e.Poll/e.PollReply/e.NodeConfig for the
// six node-discovery/node-configuration Art-Net kinds; a no-op for anything
// else (including the four RDM-family kinds attachRDMDetail already
// handles — the two functions' switches never overlap).
func attachNodeConfigDetail(e *Entry, pkt artnet.Packet) {
	switch pkt.Kind {
	case artnet.KindPoll:
		e.Poll = buildPollDetail(pkt.Poll)
	case artnet.KindPollReply:
		e.PollReply = buildPollReplyDetail(pkt.PollReply)
	case artnet.KindAddress:
		e.NodeConfig = buildAddressDetail(pkt.Address)
	case artnet.KindInput:
		e.NodeConfig = buildInputDetail(pkt.Input)
	case artnet.KindIpProg:
		e.NodeConfig = buildIpProgDetail(pkt.IpProg)
	case artnet.KindIpProgReply:
		e.NodeConfig = buildIpProgReplyDetail(pkt.IpProgReply)
	}
}

func buildPollDetail(p artnet.Poll) *PollDetail {
	return &PollDetail{
		FlagsRaw:              p.Flags,
		DiagPriority:          p.DiagPriority,
		SendPollReplyOnChange: p.Flags&0x02 != 0,
	}
}

func buildPollReplyDetail(p artnet.PollReply) *PollReplyDetail {
	d := &PollReplyDetail{
		IPAddress:    formatIPv4(p.IPAddress),
		ShortName:    p.ShortName,
		LongName:     p.LongName,
		NodeReport:   p.NodeReport,
		NumPorts:     p.NumPorts,
		NetSwitchRaw: p.NetSwitch,
		SubSwitchRaw: p.SubSwitch,
		Status1Raw:   p.Status1,
	}
	for i := 0; i < 4; i++ {
		d.Ports[i] = PollReplyPort{
			Index:           i,
			PortTypesRaw:    p.PortTypes[i],
			OutputSupported: p.PortTypes[i]&0x80 != 0,
			InputSupported:  p.PortTypes[i]&0x40 != 0,
			GoodInputRaw:    p.GoodInput[i],
			GoodOutputARaw:  p.GoodOutputA[i],
			GoodOutputBRaw:  p.GoodOutputB[i],
			SwInRaw:         p.SwIn[i],
			SwOutRaw:        p.SwOut[i],
		}
	}
	return d
}

func buildAddressDetail(p artnet.Address) *NodeConfigDetail {
	d := &NodeConfigDetail{
		SubKind:      "Address",
		NetSwitchRaw: byte(p.NetSwitch),
		BindIndex:    p.BindIndex,
		ShortName:    p.ShortName,
		LongName:     p.LongName,
		SubSwitchRaw: byte(p.SubSwitch),
		CommandRaw:   byte(p.Command),
		CommandName:  acCommandName(p.Command),
	}
	for i := 0; i < 4; i++ {
		d.SwInRaw[i] = byte(p.SwIn[i])
		d.SwOutRaw[i] = byte(p.SwOut[i])
	}
	return d
}

func buildInputDetail(p artnet.Input) *NodeConfigDetail {
	d := &NodeConfigDetail{SubKind: "Input", BindIndex: p.BindIndex}
	for i := 0; i < 4; i++ {
		d.InputRaw[i] = byte(p.InputStates[i])
		d.InputDisabled[i] = p.InputStates[i].Disabled()
	}
	return d
}

func buildIpProgDetail(p artnet.IpProg) *NodeConfigDetail {
	return &NodeConfigDetail{
		SubKind:           "IpProg",
		IpProgCommandRaw:  byte(p.Command),
		Enable:            p.Command&artnet.IpProgEnable != 0,
		EnableDHCP:        p.Command&artnet.IpProgEnableDHCP != 0,
		SetDefault:        p.Command&artnet.IpProgSetDefault != 0,
		ProgramSubnetMask: p.Command&artnet.IpProgProgramSubnetMask != 0,
		ProgramIP:         p.Command&artnet.IpProgProgramIP != 0,
		ProgIP:            formatIPv4(p.ProgIP),
		ProgSubnetMask:    formatIPv4(p.ProgSubnetMask),
		ProgPort:          p.ProgPort,
	}
}

func buildIpProgReplyDetail(p artnet.IpProgReply) *NodeConfigDetail {
	return &NodeConfigDetail{
		SubKind:       "IpProgReply",
		CurrentIP:     formatIPv4(p.CurrentIP),
		CurrentSubnet: formatIPv4(p.CurrentSubnet),
		ProgPort:      p.CurrentPort,
		StatusRaw:     byte(p.Status),
		DHCPEnabled:   p.Status.DHCPEnabled(),
	}
}

// acCommandName renders ArtAddress's Command byte's mnemonic for the values
// this package's artnet.AcCommand constants name; anything else (including
// AcNone) falls back to a plain hex label rather than guessing a name for a
// value this app doesn't have a confirmed mnemonic for.
func acCommandName(c artnet.AcCommand) string {
	if name, ok := acCommandNames[c]; ok {
		return name
	}
	return fmt.Sprintf("0x%02X", byte(c))
}

var acCommandNames = map[artnet.AcCommand]string{
	artnet.AcNone:         "None",
	artnet.AcCancelMerge:  "CancelMerge",
	artnet.AcLedNormal:    "LedNormal",
	artnet.AcLedMute:      "LedMute",
	artnet.AcLedLocate:    "LedLocate",
	artnet.AcResetRxFlags: "ResetRxFlags",
	artnet.AcMergeLTP0:    "MergeLTP[0]",
	artnet.AcMergeLTP1:    "MergeLTP[1]",
	artnet.AcMergeLTP2:    "MergeLTP[2]",
	artnet.AcMergeLTP3:    "MergeLTP[3]",
	artnet.AcMergeHTP0:    "MergeHTP[0]",
	artnet.AcMergeHTP1:    "MergeHTP[1]",
	artnet.AcMergeHTP2:    "MergeHTP[2]",
	artnet.AcMergeHTP3:    "MergeHTP[3]",
	artnet.AcArtNetSel0:   "ArtNetSel[0]",
	artnet.AcArtNetSel1:   "ArtNetSel[1]",
	artnet.AcArtNetSel2:   "ArtNetSel[2]",
	artnet.AcArtNetSel3:   "ArtNetSel[3]",
	artnet.AcAcnSel0:      "AcnSel[0]",
	artnet.AcAcnSel1:      "AcnSel[1]",
	artnet.AcAcnSel2:      "AcnSel[2]",
	artnet.AcAcnSel3:      "AcnSel[3]",
	artnet.AcClearOp0:     "ClearOp[0]",
	artnet.AcClearOp1:     "ClearOp[1]",
	artnet.AcClearOp2:     "ClearOp[2]",
	artnet.AcClearOp3:     "ClearOp[3]",
}

// formatIPv4 renders a raw 4-byte IPv4 address as dotted-quad text, without
// pulling in net/netip's stricter parsing (some of these bytes may be
// 0.0.0.0/unset, which this just prints as-is rather than treating as an
// error).
func formatIPv4(b [4]byte) string {
	return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
}
