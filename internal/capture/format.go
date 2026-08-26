package capture

import (
	"fmt"
	"strings"
)

// FormatEntryText renders one Entry as a human-readable annotated block:
// timestamp, direction, kind/peer, decoded fields (when available), and a
// full hex dump. Used by both the continuous disk logger (one block per
// RDM/ToD entry as it happens, now also every undecodable entry routed in
// via IsRDMLoggable) and the TXT export's per-exchange transcript (report
// task item 3: "human-readable annotated transcript... decoded fields, hex
// dump, so it can be pasted into a chat message"). An entry that failed to
// decode (DecodeErr != "") gets its decode error in place of decoded
// fields, plus its hex — see HexThreshold's doc comment for why that hex is
// never suppressed regardless of size.
func FormatEntryText(e Entry) string {
	var b strings.Builder
	if e.Dir == DirNote {
		// A note is not a datagram: peer= and size= would both be noise,
		// and the whole point of the line is that it reads at a glance
		// while scanning a log full of packets.
		fmt.Fprintf(&b, "[%s] NOTE %s\n", e.Time.Format("2006-01-02 15:04:05.000"), e.Key)
		return b.String()
	}
	peer := "-"
	if e.Peer.IsValid() {
		peer = e.Peer.String()
	}
	fmt.Fprintf(&b, "[%s] %s  %s  peer=%s  size=%d\n",
		e.Time.Format("2006-01-02 15:04:05.000"), dirLabel(e.Dir), e.Kind, peer, e.Size)

	switch {
	case e.RDM != nil:
		writeRDMDetailText(&b, e.RDM)
	case e.Tod != nil:
		writeTodDetailText(&b, e.Tod)
	case e.PollReply != nil:
		writePollReplyDetailText(&b, e.PollReply)
	case e.Poll != nil:
		writePollDetailText(&b, e.Poll)
	case e.NodeConfig != nil:
		writeNodeConfigDetailText(&b, e.NodeConfig)
	case e.DecodeErr != "":
		fmt.Fprintf(&b, "  DECODE ERROR: %s\n", e.DecodeErr)
	case e.Key != "":
		fmt.Fprintf(&b, "  %s\n", e.Key)
	}

	if e.Hex != "" {
		fmt.Fprintf(&b, "  hex: %s\n", e.Hex)
	} else if e.HexTrunc {
		fmt.Fprintf(&b, "  hex: (omitted, %d bytes > capture threshold)\n", e.Size)
	}
	return b.String()
}

func dirLabel(d Direction) string {
	switch d {
	case DirOut:
		return "OUT"
	case DirNote:
		return "NOTE"
	default:
		return "IN "
	}
}

func writeRDMDetailText(b *strings.Builder, d *RDMDetail) {
	if d.DecodeError != "" {
		fmt.Fprintf(b, "  DECODE ERROR: %s\n", d.DecodeError)
		return
	}
	dir := "REQUEST"
	if d.IsResponse {
		dir = "RESPONSE"
	}
	fmt.Fprintf(b, "  %s  %s  TN=%d  SubDevice=%d  checksum=%s\n",
		d.CommandClass, dir, d.TransactionNumber, d.SubDevice, validLabel(d.ChecksumValid))
	fmt.Fprintf(b, "  src=%s  dst=%s\n", d.SourceUID, d.DestUID)
	fmt.Fprintf(b, "  PID=0x%04X (%s)  PDL=%d\n", d.PID, d.PIDName, d.PDL)
	if d.IsResponse {
		fmt.Fprintf(b, "  responseType=%s", d.ResponseType)
		if d.MessageCount > 0 {
			fmt.Fprintf(b, "  messageCount=%d", d.MessageCount)
		}
		fmt.Fprintln(b)
		if d.ResponseType == "NACK_REASON" {
			fmt.Fprintf(b, "  NACK reason: 0x%04X (%s)\n", d.NackReasonCode, d.NackReasonName)
		}
		if d.ResponseType == "ACK_TIMER" {
			fmt.Fprintf(b, "  ACK_TIMER: raw=%d units -> %dms (10ms/unit per E1.20 §6.3.3)\n",
				d.AckTimerRawUnits, d.AckTimerMs)
		}
	} else {
		fmt.Fprintf(b, "  portId=%d\n", d.PortID)
	}
	if d.Decoded != "" {
		fmt.Fprintf(b, "  decoded: %s\n", d.Decoded)
	}
	fmt.Fprintf(b, "  paramData: %s\n", nonEmpty(d.ParamDataHex, "(none)"))
}

func writeTodDetailText(b *strings.Builder, d *TodDetail) {
	fmt.Fprintf(b, "  ArtTod%s  command=0x%02X (%s)  net=%d\n", d.SubKind, d.Command, d.CommandName, d.Net)
	if len(d.Addresses) > 0 {
		fmt.Fprintf(b, "  addresses: %v\n", d.Addresses)
	}
	if d.SubKind == "Data" {
		fmt.Fprintf(b, "  rdmVersion=%d port=%d uidTotal=%d blockCount=%d\n", d.RdmVersion, d.Port, d.UidTotal, d.BlockCount)
		if len(d.UIDs) > 0 {
			fmt.Fprintf(b, "  uids: %s\n", strings.Join(d.UIDs, ", "))
		} else {
			fmt.Fprintln(b, "  uids: (none in this block)")
		}
	}
}

// writePollDetailText renders one ArtPoll entry's decoded detail.
func writePollDetailText(b *strings.Builder, d *PollDetail) {
	fmt.Fprintf(b, "  flags=0x%02X (sendPollReplyOnChange=%s)  diagPriority=%d\n",
		d.FlagsRaw, boolLabel(d.SendPollReplyOnChange), d.DiagPriority)
}

// writePollReplyDetailText renders one ArtPollReply entry's decoded
// detail — report task 1's primary evidence for the "every port shows n/a"
// bug: raw byte alongside decoded interpretation for every per-port field,
// never the interpretation alone.
func writePollReplyDetailText(b *strings.Builder, d *PollReplyDetail) {
	fmt.Fprintf(b, "  node=%q (%q)  ip=%s  numPorts=%d\n", d.ShortName, d.LongName, d.IPAddress, d.NumPorts)
	fmt.Fprintf(b, "  netSwitch=0x%02X  subSwitch=0x%02X  status1=0x%02X",
		d.NetSwitchRaw, d.SubSwitchRaw, d.Status1Raw)
	if d.NodeReport != "" {
		fmt.Fprintf(b, "  nodeReport=%q", d.NodeReport)
	}
	fmt.Fprintln(b)
	for _, p := range d.Ports {
		fmt.Fprintf(b, "  port[%d]: portTypes=0x%02X (input=%s output=%s)  goodInput=0x%02X  goodOutputA=0x%02X  goodOutputB=0x%02X  swIn=0x%02X  swOut=0x%02X\n",
			p.Index, p.PortTypesRaw, boolLabel(p.InputSupported), boolLabel(p.OutputSupported),
			p.GoodInputRaw, p.GoodOutputARaw, p.GoodOutputBRaw, p.SwInRaw, p.SwOutRaw)
	}
}

// writeNodeConfigDetailText renders one ArtAddress/ArtInput/ArtIpProg/
// ArtIpProgReply entry's decoded detail, per SubKind — report task 1's
// complete-record family for "ArtAddress/ArtIpProg does nothing".
func writeNodeConfigDetailText(b *strings.Builder, d *NodeConfigDetail) {
	switch d.SubKind {
	case "Address":
		fmt.Fprintf(b, "  ArtAddress  command=0x%02X (%s)  bindIndex=%d\n", d.CommandRaw, d.CommandName, d.BindIndex)
		fmt.Fprintf(b, "  shortName=%q  longName=%q\n", d.ShortName, d.LongName)
		fmt.Fprintf(b, "  netSwitch=0x%02X  subSwitch=0x%02X  swIn=%s  swOut=%s\n",
			d.NetSwitchRaw, d.SubSwitchRaw, hexBytesLabel(d.SwInRaw[:]), hexBytesLabel(d.SwOutRaw[:]))
	case "Input":
		fmt.Fprintf(b, "  ArtInput  bindIndex=%d\n", d.BindIndex)
		fmt.Fprintf(b, "  input=%s  disabled=%v\n", hexBytesLabel(d.InputRaw[:]), d.InputDisabled)
	case "IpProg":
		fmt.Fprintf(b, "  ArtIpProg  command=0x%02X (enable=%s enableDHCP=%s setDefault=%s programSubnetMask=%s programIP=%s)\n",
			d.IpProgCommandRaw, boolLabel(d.Enable), boolLabel(d.EnableDHCP), boolLabel(d.SetDefault),
			boolLabel(d.ProgramSubnetMask), boolLabel(d.ProgramIP))
		fmt.Fprintf(b, "  progIP=%s  progSubnetMask=%s  progPort=%d\n", d.ProgIP, d.ProgSubnetMask, d.ProgPort)
	case "IpProgReply":
		fmt.Fprintf(b, "  ArtIpProgReply  status=0x%02X (dhcpEnabled=%s)\n", d.StatusRaw, boolLabel(d.DHCPEnabled))
		fmt.Fprintf(b, "  currentIP=%s  currentSubnet=%s  currentPort=%d\n", d.CurrentIP, d.CurrentSubnet, d.ProgPort)
	default:
		fmt.Fprintf(b, "  unrecognized NodeConfig SubKind %q\n", d.SubKind)
	}
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func hexBytesLabel(bs []byte) string {
	out := make([]string, len(bs))
	for i, v := range bs {
		out[i] = fmt.Sprintf("0x%02X", v)
	}
	return "[" + strings.Join(out, " ") + "]"
}

func validLabel(ok bool) string {
	if ok {
		return "valid"
	}
	return "INVALID"
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
